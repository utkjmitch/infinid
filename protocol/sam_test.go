package protocol

import (
	"testing"
	"time"

	"github.com/utkjmitch/infinid/bus"
)

// samFrame wraps a payload as the wall control's ACK06 reply to a SAM read.
func samFrame(reg []byte, payload []byte) bus.Frame {
	return bus.Frame{Src: bus.DevWallControl, Dst: bus.DevSAM, Op: bus.OpAck06,
		Data: append(append([]byte{}, reg...), payload...)}
}

func TestSystemState3B02(t *testing.T) {
	// active_zones=0b0000_0111 (zones 1-3), °F, temps 74/72/70, RH 55/56/57,
	// OAT 88, byte22: high nibble stage 2, low nibble mode 1 (cool).
	p := make([]byte, 29)
	p[0] = 0x07
	p[1] = 0x00 // °F
	p[3], p[4], p[5] = 74, 72, 70
	p[11], p[12], p[13] = 55, 56, 57
	p[20] = 88
	p[22] = 0x21
	rs, ok := Decode(samFrame([]byte{0x00, 0x3b, 0x02}, p), time.Now())
	if !ok {
		t.Fatal("3B02 not decoded")
	}
	byKey := map[string]Reading{}
	for _, r := range rs {
		if r.Zone > 0 {
			byKey[r.Field+string(rune('0'+r.Zone))] = r
		} else {
			byKey[r.Field] = r
		}
	}
	if byKey["temp_sam1"].Value != 74 || byKey["temp_sam3"].Value != 70 {
		t.Errorf("zone temps wrong: %+v", byKey)
	}
	if byKey["humidity_sam1"].Value != 55 || byKey["humidity_sam3"].Value != 57 {
		t.Errorf("zone humidity wrong: %+v", byKey)
	}
	if _, exists := byKey["temp_sam4"]; exists {
		t.Error("zone 4 not in active_zones bitmask — must not decode")
	}
	if byKey["active_zones"].Value != 7 {
		t.Errorf("active_zones = %v, want 7", byKey["active_zones"].Value)
	}
	if byKey["metric_units"].Value != 0 {
		t.Errorf("metric_units = %v, want 0 (°F)", byKey["metric_units"].Value)
	}
	if byKey["outdoor_temp_sam"].Value != 88 {
		t.Errorf("OAT = %v, want 88", byKey["outdoor_temp_sam"].Value)
	}
	// Distinct stage/mode nibbles kills nibble-swap blindness: stage 2 in
	// the high nibble, mode "cool" in the low nibble, both from 0x21.
	if byKey["active_stages"].Value != 2 {
		t.Errorf("active_stages = %v, want 2", byKey["active_stages"].Value)
	}
	if byKey["system_mode"].Text != "cool" {
		t.Errorf("mode = %q, want cool", byKey["system_mode"].Text)
	}
}

func TestZoneSettings3B03(t *testing.T) {
	p := make([]byte, 150)
	p[0] = 0x05               // zones 1 and 3
	p[3], p[5] = 0, 2         // fan auto / med
	p[11] = 0x04              // zone 3 holding (permanent)
	p[12], p[14] = 68, 66     // heat setpoints
	p[20], p[22] = 73, 75     // cool setpoints
	p[38], p[39] = 0x02, 0x87 // zone 1 hold duration 647 min
	rs, ok := Decode(samFrame([]byte{0x00, 0x3b, 0x03}, p), time.Now())
	if !ok {
		t.Fatal("3B03 not decoded")
	}
	got := map[string]Reading{}
	for _, r := range rs {
		got[r.Field+string(rune('0'+r.Zone))] = r
	}
	if got["heat_setpoint_sam1"].Value != 68 || got["heat_setpoint_sam3"].Value != 66 {
		t.Errorf("heat setpoints: %+v", got)
	}
	if got["cool_setpoint_sam3"].Value != 75 {
		t.Errorf("cool setpoint z3: %+v", got["cool_setpoint_sam3"])
	}
	if got["fan_mode_sam3"].Text != "med" {
		t.Errorf("fan z3 = %q, want med", got["fan_mode_sam3"].Text)
	}
	if got["hold_permanent1"].Value != 0 || got["hold_permanent3"].Value != 1 {
		t.Errorf("holds: %+v", got)
	}
	if got["hold_remaining_min_sam1"].Value != 647 {
		t.Errorf("hold duration z1 = %v, want 647", got["hold_remaining_min_sam1"].Value)
	}
	// Zone 3 is holding but has no duration byte set — hold_remaining_min_sam
	// must still be emitted (unconditional pin), with value 0.
	if r, exists := got["hold_remaining_min_sam3"]; !exists || r.Value != 0 {
		t.Errorf("hold_remaining_min_sam3 = %+v exists=%v, want present with value 0", r, exists)
	}
	if _, exists := got["heat_setpoint_sam2"]; exists {
		t.Error("zone 2 absent from bitmask — must not decode")
	}
}

func TestFilterLife3B05(t *testing.T) {
	p := []byte{0x01, 0x00, 0x00, 42, 17, 10, 63, 1, 0, 0, 0}
	rs, ok := Decode(samFrame([]byte{0x00, 0x3b, 0x05}, p), time.Now())
	if !ok {
		t.Fatal("3B05 not decoded")
	}
	got := map[string]float64{}
	for _, r := range rs {
		got[r.Field] = r.Value
	}
	if got["filter_life_used"] != 42 {
		t.Errorf("filter = %v, want 42", got["filter_life_used"])
	}
	if got["uv_life_used"] != 17 {
		t.Errorf("uv = %v, want 17", got["uv_life_used"])
	}
	if got["humidifier_pad_life_used"] != 10 {
		t.Errorf("humidifier pad = %v, want 10", got["humidifier_pad_life_used"])
	}
	if got["vent_filter_life_used"] != 63 {
		t.Errorf("vent filter = %v, want 63", got["vent_filter_life_used"])
	}
}

func TestFaultHistory4202(t *testing.T) {
	// Entry layout: code, source addr, hour, minute, days-since-2013-01-01
	// (u16 BE), bit7-inverted-active + count. Entry 0 newest.
	// Fixed offset (not a named zone) keeps the test independent of the
	// machine's local TZ database while still exercising real loc injection.
	loc := time.FixedZone("TEST-4", -4*3600)
	p := make([]byte, 70)
	// Entry 0: code 171, source 0x20, 09:16, day 4956 (=2026-07-28), cleared, count 1.
	copy(p[0:7], []byte{171, 0x20, 9, 16, 0x13, 0x5c, 0x81})
	// Entry 1: code 12, source 0x40, 14:30, same day, ACTIVE (bit7=0), count 3.
	copy(p[7:14], []byte{12, 0x40, 14, 30, 0x13, 0x5c, 0x03})
	faults, ok := DecodeFaults(samFrame([]byte{0x00, 0x42, 0x02}, p), loc)
	if !ok || len(faults) != 2 {
		t.Fatalf("faults = %+v ok=%v, want 2 entries", faults, ok)
	}
	f0 := faults[0]
	if f0.Code != 171 || f0.Source != 0x20 || f0.Active || f0.Count != 1 {
		t.Errorf("entry 0 = %+v", f0)
	}
	if f0.Time.Format("2006-01-02 15:04") != "2026-07-28 09:16" {
		t.Errorf("entry 0 time = %v, want 2026-07-28 09:16", f0.Time)
	}
	if f0.Time.Location() != loc {
		t.Errorf("entry 0 location = %v, want injected loc", f0.Time.Location())
	}
	if !faults[1].Active || faults[1].Count != 3 {
		t.Errorf("entry 1 = %+v", faults[1])
	}
	// nil loc defaults to time.Local.
	local, ok := DecodeFaults(samFrame([]byte{0x00, 0x42, 0x02}, p), nil)
	if !ok || len(local) == 0 || local[0].Time.Location() != time.Local {
		t.Errorf("nil loc did not default to time.Local: %+v", local)
	}
}

// TestFaultHistory4202EmptyTable pins the panel-fault-reset path: a valid
// 4202 reply (right register, right op, right length — same shape as the
// TestFaultHistory4202 fixture above) whose 10 slots are all zeroed is a
// real, decodable "no faults currently" reply, not an undecodable frame.
// Before this fix DecodeFaults returned ok=len(out)>0, so this exact case
// returned ok=false and NoteFaults's fault_history_wiped/manually_cleared
// path (which needs an empty, ok=true slice to fire) was unreachable: a
// homeowner pressing "reset faults" on the wall control left stale active
// faults in the journal forever.
func TestFaultHistory4202EmptyTable(t *testing.T) {
	loc := time.UTC
	p := make([]byte, 70) // same 70-byte entry-table shape, every slot zero
	faults, ok := DecodeFaults(samFrame([]byte{0x00, 0x42, 0x02}, p), loc)
	if !ok {
		t.Fatal("valid-shape all-empty fault table must return ok=true")
	}
	if len(faults) != 0 {
		t.Errorf("faults = %+v, want none", faults)
	}

	// Companion: guard failures must still return false even though the
	// empty-but-valid case above now returns true — the fix only changes
	// what happens after the guards pass.
	if _, ok := DecodeFaults(samFrame([]byte{0x00, 0x42, 0x03}, p), loc); ok {
		t.Error("wrong-register frame must return ok=false")
	}
	if _, ok := DecodeFaults(samFrame([]byte{0x00, 0x42, 0x02}, make([]byte, 69)), loc); ok {
		t.Error("below-minimum-length frame must return ok=false")
	}
}

// TestSAMMalformedInput pins ok=true-with-zero-readings for verified-owner/op
// frames whose payload is too short for the table (found convention), and
// the DecodeFaults boundary cases: tail-anchored 72-byte payloads, below-
// minimum length, and wrong-register frames.
func TestSAMMalformedInput(t *testing.T) {
	t.Run("3B02 with 28-byte payload (short by 1)", func(t *testing.T) {
		rs, ok := Decode(samFrame([]byte{0x00, 0x3b, 0x02}, make([]byte, 28)), time.Now())
		if !ok {
			t.Fatal("ok = false, want true (register matched; decoder just yields no readings)")
		}
		if len(rs) != 0 {
			t.Errorf("readings = %d, want 0", len(rs))
		}
	})
	t.Run("3B05 with 10-byte payload (short by 1)", func(t *testing.T) {
		rs, ok := Decode(samFrame([]byte{0x00, 0x3b, 0x05}, make([]byte, 10)), time.Now())
		if !ok || len(rs) != 0 {
			t.Fatalf("short 3B05: ok=%v readings=%d, want ok=true with none", ok, len(rs))
		}
	})
	t.Run("3B03 with 149-byte payload (short by 1)", func(t *testing.T) {
		rs, ok := Decode(samFrame([]byte{0x00, 0x3b, 0x03}, make([]byte, 149)), time.Now())
		if !ok {
			t.Fatal("ok = false, want true (register matched; decoder just yields no readings)")
		}
		if len(rs) != 0 {
			t.Errorf("readings = %d, want 0", len(rs))
		}
	})
	t.Run("4202 with 72-byte payload (2-byte header + 70 tail-anchored entries)", func(t *testing.T) {
		p := make([]byte, 72)
		copy(p[2:9], []byte{171, 0x20, 9, 16, 0x13, 0x5c, 0x81})
		faults, ok := DecodeFaults(samFrame([]byte{0x00, 0x42, 0x02}, p), time.UTC)
		if !ok || len(faults) != 1 {
			t.Fatalf("faults = %+v ok=%v, want 1 entry", faults, ok)
		}
		if faults[0].Code != 171 {
			t.Errorf("entry 0 code = %v, want 171", faults[0].Code)
		}
	})
	t.Run("4202 with 69-byte payload (below the 70-byte minimum)", func(t *testing.T) {
		if _, ok := DecodeFaults(samFrame([]byte{0x00, 0x42, 0x02}, make([]byte, 69)), time.UTC); ok {
			t.Error("69-byte payload must return ok=false")
		}
	})
	t.Run("4202 wrong-register frame", func(t *testing.T) {
		p := make([]byte, 70)
		copy(p[0:7], []byte{171, 0x20, 9, 16, 0x13, 0x5c, 0x81})
		if _, ok := DecodeFaults(samFrame([]byte{0x00, 0x42, 0x03}, p), time.UTC); ok {
			t.Error("wrong-register frame must return ok=false")
		}
	})
	t.Run("4202-shaped ACK06 from a non-wall-control src", func(t *testing.T) {
		// Same register/op/length shape as a real fault-history reply, but
		// from a different bus address — must not be treated as an owned
		// SAM reply, matching the sibling dispatch guard in protocol.go's
		// 0x004202 case (owner == bus.DevWallControl).
		p := make([]byte, 70)
		copy(p[0:7], []byte{171, 0x20, 9, 16, 0x13, 0x5c, 0x81})
		f := bus.Frame{Src: bus.DevHeatPump, Dst: bus.DevSAM, Op: bus.OpAck06,
			Data: append([]byte{0x00, 0x42, 0x02}, p...)}
		if _, ok := DecodeFaults(f, time.UTC); ok {
			t.Error("non-wall-control src must return ok=false")
		}
	})
}
