package state

import (
	"testing"
	"time"

	"github.com/utkjmitch/infinid/protocol"
)

var t0 = time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)

func TestApplyAndSnapshot(t *testing.T) {
	s := New()
	s.Apply(protocol.Reading{Owner: 0x5201, Field: "suction_pressure", Value: 121, TS: t0})
	s.Apply(protocol.Reading{Owner: 0x2201, Zone: 2, Field: "temp", Value: 72.4, TS: t0})
	s.Apply(protocol.Reading{Owner: 0x2201, Zone: 2, Field: "fan_mode", Value: 0, Text: "auto", TS: t0})

	snap := s.Snapshot(t0.Add(10 * time.Second))
	if snap.Sys["suction_pressure"].Value != 121 {
		t.Errorf("sys field: %+v", snap.Sys["suction_pressure"])
	}
	if snap.Zones[2]["temp"].Value != 72.4 {
		t.Errorf("zone field: %+v", snap.Zones[2]["temp"])
	}
	if snap.Zones[2]["fan_mode"].Text != "auto" {
		t.Errorf("text field: %+v", snap.Zones[2]["fan_mode"])
	}
}

func TestStaleness(t *testing.T) {
	s := New()
	s.Apply(protocol.Reading{Field: "supply_cfm", Value: 500, TS: t0})
	fresh := s.Snapshot(t0.Add(1 * time.Minute))
	if fresh.Sys["supply_cfm"].Stale {
		t.Error("1 min old must not be stale (horizon 5 min)")
	}
	old := s.Snapshot(t0.Add(6 * time.Minute))
	if !old.Sys["supply_cfm"].Stale {
		t.Error("6 min old must be stale")
	}
	// SAM-sourced hourly fields get the long horizon.
	s.Apply(protocol.Reading{Field: "filter_life_used", Value: 42, TS: t0})
	if s.Snapshot(t0.Add(2 * time.Hour)).Sys["filter_life_used"].Stale {
		t.Error("filter at 2h must not be stale (horizon 3h)")
	}
}

func TestZonePresence(t *testing.T) {
	s := New()
	// Zone 1 always assumed (wall control's own zone). Damper readings alone
	// must NOT create zones (slot 4 read 0x00 on the reference 3-zone system).
	s.Apply(protocol.Reading{Owner: 0x6001, Zone: 4, Field: "damper_position", Value: 0, TS: t0})
	snap := s.Snapshot(t0)
	if _, ok := snap.Zones[4]; ok {
		t.Error("damper traffic alone must not establish a zone")
	}
	if _, ok := snap.Zones[1]; !ok {
		t.Error("zone 1 (wall control's own) must exist by default")
	}
	// Sensor traffic establishes a zone; its damper then attaches.
	s.Apply(protocol.Reading{Owner: 0x2201, Zone: 2, Field: "temp", Value: 71, TS: t0})
	s.Apply(protocol.Reading{Owner: 0x6001, Zone: 2, Field: "damper_position", Value: 7, TS: t0})
	snap = s.Snapshot(t0)
	if snap.Zones[2]["damper_position"].Value != 7 {
		t.Errorf("zone2 damper: %+v", snap.Zones[2])
	}
	// SAM active_zones bitmask is authoritative: 0b0111 adds zone 3.
	s.Apply(protocol.Reading{Owner: 0x2001, Field: "active_zones", Value: 7, TS: t0})
	snap = s.Snapshot(t0)
	if _, ok := snap.Zones[3]; !ok {
		t.Error("SAM bitmask must establish zone 3")
	}
}

// TestHoldClearRemovesCountdown pins the amendment behavior: the 00041F
// decoder only emits hold_remaining_min while a timed hold is active. When
// the hold ends, the wall control pushes hold=0 and the countdown field
// simply stops arriving on the bus (08-23 evidence: expiry zeroes-then-stops)
// — so without an explicit clearing rule, Snapshot would show a stale
// countdown forever. Apply must delete hold_remaining_min from the zone map
// when hold=0 is applied.
func TestHoldClearRemovesCountdown(t *testing.T) {
	s := New()
	s.Apply(protocol.Reading{Owner: 0x2201, Zone: 2, Field: "hold", Value: 1, TS: t0})
	s.Apply(protocol.Reading{Owner: 0x2201, Zone: 2, Field: "hold_remaining_min", Value: 647, TS: t0})
	s.Apply(protocol.Reading{Owner: 0x2201, Zone: 2, Field: "hold", Value: 0, TS: t0})

	snap := s.Snapshot(t0)
	if _, ok := snap.Zones[2]["hold_remaining_min"]; ok {
		t.Error("hold_remaining_min must be cleared when hold=0 is applied")
	}
	if snap.Zones[2]["hold"].Value != 0 {
		t.Errorf("hold field must still be present with value 0: %+v", snap.Zones[2]["hold"])
	}
}

// TestHoldPermanentDoesNotClearCountdown pins that hold_permanent (the SAM
// 3B03 bitmap field) is a different field from hold and must never trigger
// the countdown-clearing rule, even when its value is 0.
func TestHoldPermanentDoesNotClearCountdown(t *testing.T) {
	s := New()
	s.Apply(protocol.Reading{Owner: 0x2201, Zone: 2, Field: "hold_remaining_min", Value: 647, TS: t0})
	s.Apply(protocol.Reading{Owner: 0x2001, Zone: 2, Field: "hold_permanent", Value: 0, TS: t0})

	snap := s.Snapshot(t0)
	if _, ok := snap.Zones[2]["hold_remaining_min"]; !ok {
		t.Error("hold_permanent=0 must not clear hold_remaining_min")
	}
}
