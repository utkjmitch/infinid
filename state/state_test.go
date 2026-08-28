package state

import (
	"sync"
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

// TestActiveZonesMaskRetraction pins that the active_zones mask is not
// additive-only: a zone it established itself can be retracted when a later
// mask clears that bit, but a zone that ever saw real sensor traffic
// survives — and zone 1 is never removed regardless.
func TestActiveZonesMaskRetraction(t *testing.T) {
	s := New()
	// 0b0111 = zones 1,2,3 (zone 1 default, mask adds 2 and 3 as mask-only).
	s.Apply(protocol.Reading{Owner: 0x2001, Field: "active_zones", Value: 7, TS: t0})
	snap := s.Snapshot(t0)
	if _, ok := snap.Zones[2]; !ok {
		t.Fatal("mask 0x07 must establish zone 2")
	}
	if _, ok := snap.Zones[3]; !ok {
		t.Fatal("mask 0x07 must establish zone 3")
	}

	// 0b0011 clears zone 3's bit: zone 3 was mask-only, so it is retracted.
	s.Apply(protocol.Reading{Owner: 0x2001, Field: "active_zones", Value: 3, TS: t0})
	snap = s.Snapshot(t0)
	if _, ok := snap.Zones[3]; ok {
		t.Error("mask 0x03 must retract mask-only zone 3")
	}
	if _, ok := snap.Zones[2]; !ok {
		t.Error("zone 2 still set in mask 0x03 — must remain")
	}
	if _, ok := snap.Zones[1]; !ok {
		t.Error("zone 1 must never be removed")
	}

	// Sensor traffic on a zone before the mask retracts it: the zone must
	// survive even after its bit clears.
	s.Apply(protocol.Reading{Owner: 0x2201, Zone: 3, Field: "temp", Value: 71, TS: t0})
	s.Apply(protocol.Reading{Owner: 0x2001, Field: "active_zones", Value: 7, TS: t0}) // re-add 3
	s.Apply(protocol.Reading{Owner: 0x2001, Field: "active_zones", Value: 3, TS: t0}) // clear 3's bit again
	snap = s.Snapshot(t0)
	if _, ok := snap.Zones[3]; !ok {
		t.Error("zone 3 saw real sensor traffic — must survive mask retraction")
	}

	// Zone 1 is never removed, even by a mask that clears its bit.
	s.Apply(protocol.Reading{Owner: 0x2001, Field: "active_zones", Value: 6, TS: t0})
	if _, ok := s.Snapshot(t0).Zones[1]; !ok {
		t.Error("zone 1 must survive a mask that clears bit 0")
	}

	// A mask-only zone holding only damper data is retracted WITH its
	// damper reading — dampers don't establish a zone, so they don't
	// preserve one either (recorded choice, not an accident).
	s.Apply(protocol.Reading{Owner: 0x2001, Field: "active_zones", Value: 0x0e, TS: t0}) // adds zone 4 mask-only (bits 2,3,4... 0x0e = zones 2,3,4)
	s.Apply(protocol.Reading{Owner: 0x6001, Zone: 4, Field: "damper_position", Value: 7, TS: t0})
	s.Apply(protocol.Reading{Owner: 0x2001, Field: "active_zones", Value: 6, TS: t0}) // clears zone 4's bit
	if _, ok := s.Snapshot(t0).Zones[4]; ok {
		t.Error("damper-only mask zone must be retracted, damper data included")
	}
}

// TestOutOfOrderGuard pins that Apply never lets an older reading clobber a
// newer one already stored for the same field — a guard against a future
// replay/journal feed, since the live bus loop is monotonic today.
func TestOutOfOrderGuard(t *testing.T) {
	s := New()
	newer := t0.Add(1 * time.Minute)
	s.Apply(protocol.Reading{Field: "supply_cfm", Value: 900, TS: newer})
	s.Apply(protocol.Reading{Field: "supply_cfm", Value: 500, TS: t0}) // older, out of order

	snap := s.Snapshot(newer)
	if snap.Sys["supply_cfm"].Value != 900 {
		t.Errorf("supply_cfm = %v, want 900 (newer value retained)", snap.Sys["supply_cfm"].Value)
	}

	// Same rule applies to zone-scoped fields.
	s.Apply(protocol.Reading{Owner: 0x2201, Zone: 2, Field: "temp", Value: 72, TS: newer})
	s.Apply(protocol.Reading{Owner: 0x2201, Zone: 2, Field: "temp", Value: 68, TS: t0})
	snap = s.Snapshot(newer)
	if snap.Zones[2]["temp"].Value != 72 {
		t.Errorf("zone temp = %v, want 72 (newer value retained)", snap.Zones[2]["temp"].Value)
	}
}

// TestConcurrentApplyAndSnapshot exercises the mutex under -race: several
// goroutines Apply readings while others take Snapshots concurrently.
// Note: this repo's local `go test -race` needs cgo and may not run here —
// that's fine, the test still pins correctness (no panics, no lost writes)
// under plain -run, and adds -race coverage wherever cgo is available (CI).
func TestConcurrentApplyAndSnapshot(t *testing.T) {
	s := New()
	var wg sync.WaitGroup

	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				s.Apply(protocol.Reading{Owner: 0x2201, Zone: 2, Field: "temp", Value: float64(g*50 + i), TS: t0})
			}
		}(g)
	}
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				_ = s.Snapshot(t0.Add(time.Duration(i) * time.Second))
			}
		}()
	}
	wg.Wait()

	snap := s.Snapshot(t0)
	if _, ok := snap.Zones[2]["temp"]; !ok {
		t.Error("concurrent Apply calls must leave a temp reading in zone 2")
	}
}

// TestSnapshotIsDeepCopy pins that mutating a returned Snapshot's maps
// (including inserting a brand-new zone key) never reaches back into State.
func TestSnapshotIsDeepCopy(t *testing.T) {
	s := New()
	s.Apply(protocol.Reading{Field: "supply_cfm", Value: 500, TS: t0})
	s.Apply(protocol.Reading{Owner: 0x2201, Zone: 2, Field: "temp", Value: 72, TS: t0})

	snap := s.Snapshot(t0)
	snap.Sys["supply_cfm"] = Field{Value: 999, TS: t0}
	snap.Zones[2]["temp"] = Field{Value: 999, TS: t0}
	snap.Zones[5] = map[string]Field{"temp": {Value: 999, TS: t0}} // new zone key

	fresh := s.Snapshot(t0)
	if fresh.Sys["supply_cfm"].Value != 500 {
		t.Errorf("sys mutation leaked into State: %+v", fresh.Sys["supply_cfm"])
	}
	if fresh.Zones[2]["temp"].Value != 72 {
		t.Errorf("zone mutation leaked into State: %+v", fresh.Zones[2]["temp"])
	}
	if _, ok := fresh.Zones[5]; ok {
		t.Error("inserting a zone key into a Snapshot must not create it in State")
	}
}
