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
	// 00041F WRITE 2001→2201 (zone 2) first fixture: heat 68, cool 73, fan auto, no hold.
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
	if v := byField["heat_setpoint"]; len(v) == 0 || v[0].Value != 68 {
		t.Fatalf("heat_setpoint = %+v", v)
	}
	if v := byField["cool_setpoint"]; len(v) == 0 || v[0].Value != 73 {
		t.Fatalf("cool_setpoint = %+v", v)
	}
	if v := byField["fan_mode"]; len(v) == 0 || v[0].Text != "auto" {
		t.Fatalf("fan_mode = %+v", v)
	}
	// Second zone-2 fixture is the verified timed hold: 19410 ticks = 647 min.
	if v := byField["hold_remaining_min"]; len(v) == 0 || v[0].Value != 647 {
		t.Fatalf("hold_remaining_min = %+v, want 647", v)
	}
	if v := byField["hold"]; len(v) < 2 || v[0].Value != 0 || v[1].Value != 1 {
		t.Fatalf("hold = %+v, want [0 1 ...]", v)
	}
}

func TestZoneIndefiniteHold(t *testing.T) {
	rs := decodeAll(t)
	// 00041F 2001→2301 (zone 3) fixtures all carry flags bit7 = indefinite hold.
	for _, r := range rs {
		if r.Zone == 3 && r.Reg == 0x00041f && r.Field == "hold" && r.Value != 1 {
			t.Fatalf("zone3 hold = %v, want 1 (indefinite)", r.Value)
		}
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
