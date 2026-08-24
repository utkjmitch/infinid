package protocol

import "testing"

func TestSuctionPressure(t *testing.T) {
	rs := decodeAll(t)
	got := find(rs, 0x5201, "suction_pressure")
	if len(got) != 2 || got[0].Value != 121.0 || got[1].Value != 122.0 {
		t.Fatalf("suction_pressure = %+v, want [121 122]", got)
	}
}

func TestLineVoltage(t *testing.T) {
	rs := decodeAll(t)
	got := find(rs, 0x5201, "line_voltage")
	if len(got) != 2 || got[0].Value != 245 || got[1].Value != 246 {
		t.Fatalf("line_voltage = %+v, want [245 246]", got)
	}
}

func TestCompressorRPM(t *testing.T) {
	rs := decodeAll(t)
	target := find(rs, 0x5201, "compressor_rpm_target")
	actual := find(rs, 0x5201, "compressor_rpm")
	if len(target) != 2 || target[0].Value != 1200 || target[1].Value != 0 {
		t.Fatalf("rpm_target = %+v, want [1200 0]", target)
	}
	if len(actual) != 2 || actual[0].Value != 1200 || actual[1].Value != 0 {
		t.Fatalf("rpm actual = %+v, want [1200 0]", actual)
	}
}

func TestCompressorStageCommand(t *testing.T) {
	rs := decodeAll(t)
	// 000605 WRITE fixtures: stage 1.0, 0.0, 0.0, 2.0 (float32 BE at [0..3]).
	got := find(rs, 0x5201, "compressor_stage_cmd")
	if len(got) != 4 {
		t.Fatalf("stage_cmd count = %d, want 4", len(got))
	}
	want := []float64{1, 0, 0, 2}
	for i, w := range want {
		if got[i].Value != w {
			t.Errorf("stage_cmd[%d] = %v, want %v", i, got[i].Value, w)
		}
	}
	// [4] mode flag: 01=cool on first fixture, 00=heat on third.
	mode := find(rs, 0x5201, "cool_mode_flag")
	if len(mode) != 4 || mode[0].Value != 1 || mode[2].Value != 0 {
		t.Fatalf("cool_mode_flag = %+v", mode)
	}
}

func TestCompressorActualStage(t *testing.T) {
	rs := decodeAll(t)
	got := find(rs, 0x5201, "compressor_stage")
	if len(got) != 3 || got[0].Value != 1 {
		t.Fatalf("compressor_stage = %+v, want three 1s", got)
	}
}

func TestCounters(t *testing.T) {
	rs := decodeAll(t)
	// 000310@3e01: 0x23=6598 heat1 cycles, 0x24=13 heat2, 0x2b=24 power, 0x2d=20470 blower.
	// Unverified keys (0x27, 0x29, 0x48...) must not decode.
	checks := []struct {
		owner uint16
		field string
		first float64
	}{
		{0x3e01, "heat_stage1_cycles", 6598},
		{0x3e01, "heat_stage2_cycles", 13},
		{0x3e01, "idu_power_cycles", 24},
		{0x3e01, "blower_cycles", 20470},
		{0x3e01, "heat_stage1_hours", 1035},
		{0x3e01, "heat_stage2_hours", 9},
		{0x3e01, "idu_power_hours", 13368},
		{0x3e01, "blower_hours", 4990},
		{0x5201, "cool_cycles", 12530},
		{0x5201, "odu_power_cycles", 6},
		{0x5201, "cool_hours", 4169},
		{0x5201, "odu_power_hours", 13805},
	}
	for _, c := range checks {
		got := find(rs, c.owner, c.field)
		if len(got) == 0 {
			t.Errorf("no readings for %04x %s", c.owner, c.field)
			continue
		}
		if got[0].Value != c.first {
			t.Errorf("%s = %v, want %v", c.field, got[0].Value, c.first)
		}
	}
	if len(find(rs, 0x3e01, "counter_0x27")) != 0 {
		t.Error("unverified counter key decoded")
	}
}
