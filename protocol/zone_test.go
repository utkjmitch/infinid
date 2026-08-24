package protocol

import "testing"

func TestDamperFeedback(t *testing.T) {
	rs := decodeAll(t)
	var z1, z2, z5 []Reading
	for _, r := range rs {
		if r.Owner != 0x6001 || r.Field != "damper_position" || r.Reg != 0x000319 {
			continue
		}
		switch r.Zone {
		case 1:
			z1 = append(z1, r)
		case 2:
			z2 = append(z2, r)
		case 5:
			z5 = append(z5, r)
		}
	}
	// Fixtures: [0]=0x00/0x0f/0x0f, [1]=0x0f/0x0f/0x07, [4..7]=0xff (absent).
	if len(z1) != 3 || z1[1].Value != 15 {
		t.Fatalf("zone1 damper = %+v", z1)
	}
	if len(z2) != 3 || z2[2].Value != 7 {
		t.Fatalf("zone2 damper = %+v", z2)
	}
	if len(z5) != 0 {
		t.Error("0xFF (absent) damper slots must not produce readings")
	}
}

func TestZoneSetpointPush(t *testing.T) {
	rs := decodeAll(t)
	// 00041F WRITE 2001→2201 (zone 2): 3 fixtures. First: heat 68, cool 73,
	// fan auto, no hold. Second+third: verified timed holds (19410/19380
	// ticks = 647/646 min); only those two carry hold_remaining_min.
	var z2 []Reading
	for _, r := range rs {
		if r.Zone == 2 && r.Reg == 0x00041f {
			z2 = append(z2, r)
		}
	}
	byField := map[string][]Reading{}
	for _, r := range z2 {
		byField[r.Field] = append(byField[r.Field], r)
	}
	if v := byField["heat_setpoint"]; len(v) != 3 || v[0].Value != 68 {
		t.Fatalf("heat_setpoint = %+v, want 3 readings, first 68", v)
	}
	if v := byField["cool_setpoint"]; len(v) != 3 || v[0].Value != 73 {
		t.Fatalf("cool_setpoint = %+v, want 3 readings, first 73", v)
	}
	if v := byField["fan_mode"]; len(v) != 3 || v[0].Text != "auto" {
		t.Fatalf("fan_mode = %+v, want 3 readings, first auto", v)
	}
	if v := byField["hold"]; len(v) != 3 || v[0].Value != 0 || v[1].Value != 1 || v[2].Value != 1 {
		t.Fatalf("hold = %+v, want [0 1 1]", v)
	}
	if v := byField["hold_remaining_min"]; len(v) != 2 || v[0].Value != 647 || v[1].Value != 646 {
		t.Fatalf("hold_remaining_min = %+v, want [647 646]", v)
	}
}

// TestZoneThreeNoFalseHold pins the 08-23 longitudinal correction: zone 3's
// 00041F fixtures carry [0]=0x80 (steady-idle value, not a hold flag) and
// [1]!=0x18 (no timed-hold marker), so all three readings must decode as
// hold=0, not hold=1. See docs/experiments/2026-08-23-longitudinal-findings.md.
func TestZoneThreeNoFalseHold(t *testing.T) {
	rs := decodeAll(t)
	var z3 []Reading
	for _, r := range rs {
		if r.Zone == 3 && r.Reg == 0x00041f && r.Field == "hold" {
			z3 = append(z3, r)
		}
	}
	if len(z3) != 3 {
		t.Fatalf("zone3 hold readings = %+v, want 3", z3)
	}
	for i, r := range z3 {
		if r.Value != 0 {
			t.Errorf("zone3 hold[%d] = %v, want 0 (0x80 is steady-idle, not hold)", i, r.Value)
		}
	}
}

func TestDamperCommand(t *testing.T) {
	rs := decodeAll(t)
	var z1, z2 []Reading
	for _, r := range rs {
		if r.Owner != 0x6001 || r.Field != "damper_cmd" || r.Reg != 0x000308 {
			continue
		}
		switch r.Zone {
		case 1:
			z1 = append(z1, r)
		case 2:
			z2 = append(z2, r)
		}
	}
	// Fixtures: [0]=0x00/0x0f/0x0f, [1]=0x0f/0x07/0x0c.
	if len(z1) != 3 || z1[0].Value != 0 || z1[1].Value != 15 || z1[2].Value != 15 {
		t.Fatalf("zone1 damper_cmd = %+v", z1)
	}
	if len(z2) != 3 || z2[0].Value != 15 || z2[1].Value != 7 || z2[2].Value != 12 {
		t.Fatalf("zone2 damper_cmd = %+v", z2)
	}
}

func TestZoneSensorStatus(t *testing.T) {
	rs := decodeAll(t)
	var temp, rh []Reading
	for _, r := range rs {
		if r.Zone == 2 && r.Reg == 0x00041e {
			if r.Field == "temp" {
				temp = append(temp, r)
			}
			if r.Field == "humidity" {
				rh = append(rh, r)
			}
		}
	}
	if len(temp) != 2 || temp[0].Value != 72.375 {
		t.Fatalf("zone2 temp = %+v, want first 72.375", temp)
	}
	if len(rh) != 2 || rh[0].Value != 55 {
		t.Fatalf("zone2 humidity = %+v, want 55", rh)
	}
}
