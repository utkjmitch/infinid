package protocol

import (
	"testing"
	"time"

	"github.com/utkjmitch/infinid/bus"
)

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
	if len(got) != 3 {
		t.Fatalf("compressor_stage = %+v, want three 1s", got)
	}
	for i, r := range got {
		if r.Value != 1 {
			t.Errorf("compressor_stage[%d] = %v, want 1", i, r.Value)
		}
	}
}

func TestODUAnalog(t *testing.T) {
	rs := decodeAll(t)
	// 000625@5201 first fixture payload 0327xxxx: u16(0)=0x0327=807.
	got := find(rs, 0x5201, "odu_power_analog")
	if len(got) == 0 || got[0].Value != 807 {
		t.Fatalf("odu_power_analog = %+v, want first=807", got)
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

	// The first 000310@3e01 fixture frame has 7 KV rows (keys 0x23, 0x24,
	// 0x27, 0x28, 0x2b, 0x2d, 0x48) but only 4 are verified (0x23/0x24/0x2b/
	// 0x2d); decoding it directly must yield exactly 4 readings, not 7 — this
	// fails the moment an unverified key like 0x27/0x29/0x48 gets added.
	f := fixtures(t)[24] // 3e01, 000310, first sample: testdata/frames.jsonl line 25
	if f.F.Src != 0x3e01 {
		t.Fatalf("fixture[24] src = %04x, want 3e01 (fixture order changed?)", f.F.Src)
	}
	got, ok := Decode(f.F, f.TS)
	if !ok {
		t.Fatal("Decode on verified 000310@3e01 frame returned ok=false")
	}
	if len(got) != 4 {
		t.Errorf("readings from first 3e01/000310 frame = %d, want 4 (only verified keys)", len(got))
	}
}

// TestUndecodedOwnerNotOK pins ADR-0001 owner guards: these registers are
// byte-verified only on the owners exercised in the fixtures, so any other
// owner (or, for 000605, any op other than WRITE) must archive, not decode.
func TestUndecodedOwnerNotOK(t *testing.T) {
	cases := []struct {
		name string
		f    bus.Frame
	}{
		{
			name: "000604 from unverified owner 3e01",
			f: bus.Frame{Src: 0x3e01, Dst: 0x2001, Op: bus.OpAck06,
				Data: []byte{0x00, 0x06, 0x04, 0x04, 0xb0, 0x04, 0xb0}},
		},
		{
			name: "000605 ACK06 (not WRITE) from owner 5201",
			f: bus.Frame{Src: 0x5201, Dst: 0x2001, Op: bus.OpAck06,
				Data: []byte{0x00, 0x06, 0x05, 0x3f, 0x80, 0x00, 0x00, 0x01}},
		},
		{
			name: "000310 from unverified owner 6001, valid-looking row",
			f: bus.Frame{Src: 0x6001, Dst: 0x2001, Op: bus.OpAck06,
				Data: []byte{0x00, 0x03, 0x10, 0x23, 0x00, 0x19, 0xc6}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rs, ok := Decode(c.f, time.Now())
			if ok || rs != nil {
				t.Errorf("unverified owner/op decoded: ok=%v rs=%v", ok, rs)
			}
		})
	}
}

// TestODUMalformedInput pins no-panic, ok=true-with-zero-readings behavior
// for verified-owner frames whose payload is too short or misaligned.
func TestODUMalformedInput(t *testing.T) {
	t.Run("000605@5201 WRITE with 4-byte payload (no room for mode byte)", func(t *testing.T) {
		f := bus.Frame{Src: 0x2001, Dst: 0x5201, Op: bus.OpWrite,
			Data: []byte{0x00, 0x06, 0x05, 0x3f, 0x80, 0x00, 0x00}}
		rs, ok := Decode(f, time.Now())
		if !ok {
			t.Fatal("ok = false, want true")
		}
		if len(rs) != 0 {
			t.Errorf("readings = %d, want 0", len(rs))
		}
	})
	t.Run("000310@3e01 one full row plus 2 dangling bytes", func(t *testing.T) {
		// register(3) + [key=0x23 value=0x0019c6](4) + dangling(2)
		f := bus.Frame{Src: 0x3e01, Dst: 0x2001, Op: bus.OpAck06,
			Data: []byte{0x00, 0x03, 0x10, 0x23, 0x00, 0x19, 0xc6, 0xaa, 0xbb}}
		rs, ok := Decode(f, time.Now())
		if !ok {
			t.Fatal("ok = false, want true")
		}
		got := find(rs, 0x3e01, "heat_stage1_cycles")
		if len(got) != 1 || got[0].Value != 6598 {
			t.Errorf("heat_stage1_cycles = %+v, want [6598]", got)
		}
		if len(rs) != 1 {
			t.Errorf("readings = %d, want 1 (dangling bytes must not decode)", len(rs))
		}
	})
}

// TestCompressorStageCommandNaN pins that a NaN commanded-stage value is
// dropped rather than published (downstream JSON marshalling errors on
// NaN/Inf floats), while the frame itself still reports ok=true.
func TestCompressorStageCommandNaN(t *testing.T) {
	// float32 NaN (0x7fc00000) + mode byte.
	f := bus.Frame{Src: 0x2001, Dst: 0x5201, Op: bus.OpWrite,
		Data: []byte{0x00, 0x06, 0x05, 0x7f, 0xc0, 0x00, 0x00, 0x01}}
	rs, ok := Decode(f, time.Now())
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if len(rs) != 0 {
		t.Errorf("readings = %d, want 0 (NaN stage must be dropped)", len(rs))
	}
}
