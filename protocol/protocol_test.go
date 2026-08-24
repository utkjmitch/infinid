package protocol

import (
	"os"
	"testing"
	"time"

	"github.com/utkjmitch/infinid/bus"
	"github.com/utkjmitch/infinid/inspect"
)

// fixtures loads the curated real-capture frames from testdata.
func fixtures(t *testing.T) []struct {
	F  bus.Frame
	TS time.Time
} {
	t.Helper()
	f, err := os.Open("testdata/frames.jsonl")
	if err != nil {
		t.Fatalf("open fixtures: %v", err)
	}
	defer f.Close()
	recs, skipped, err := inspect.ParseAll(f)
	if err != nil || skipped != 0 {
		t.Fatalf("parse fixtures: err=%v skipped=%d", err, skipped)
	}
	out := make([]struct {
		F  bus.Frame
		TS time.Time
	}, len(recs))
	for i, r := range recs {
		out[i].F = bus.Frame{Src: r.Src, Dst: r.Dst, Op: r.Op, Data: r.Data}
		out[i].TS = r.TS
	}
	return out
}

// decodeAll runs every fixture through Decode and returns all readings.
func decodeAll(t *testing.T) []Reading {
	t.Helper()
	var rs []Reading
	for _, fx := range fixtures(t) {
		got, _ := Decode(fx.F, fx.TS)
		rs = append(rs, got...)
	}
	return rs
}

// find returns readings matching owner+field, in fixture order.
func find(rs []Reading, owner uint16, field string) []Reading {
	var out []Reading
	for _, r := range rs {
		if r.Owner == owner && r.Field == field {
			out = append(out, r)
		}
	}
	return out
}

func TestTLVTemperaturesODU(t *testing.T) {
	rs := decodeAll(t)
	// Fixture 000302@5201 first sample:
	// 0x11 OAT 77.56, 0x12 coil 82.75, 0x30 suction 57.56,
	// 0x4a superheat 16.00, 0x45 discharge 106.19. Unverified id 0x4b must NOT appear.
	checks := map[string]float64{
		"outdoor_temp":      77.5625,
		"outdoor_coil_temp": 82.75,
		"suction_temp":      57.5625,
		"superheat":         16.0,
		"discharge_temp":    106.1875,
	}
	for field, want := range checks {
		got := find(rs, 0x5201, field)
		if len(got) == 0 {
			t.Fatalf("no %s readings", field)
		}
		if got[0].Value != want {
			t.Errorf("%s = %v, want %v", field, got[0].Value, want)
		}
	}
	if len(find(rs, 0x5201, "unknown_0x4b")) != 0 {
		t.Error("unverified TLV id 0x4b must not be decoded (ADR-0001)")
	}
}

func TestTLVSupplyAirIDU(t *testing.T) {
	rs := decodeAll(t)
	got := find(rs, 0x3e01, "supply_air_temp")
	if len(got) != 3 {
		t.Fatalf("supply_air_temp count = %d, want 3", len(got))
	}
	if got[0].Value != 71.3125 { // 0x0475/16
		t.Errorf("supply_air_temp = %v, want 71.3125", got[0].Value)
	}
	// Absent TLV entries (tag 0x00) must not decode.
	if len(find(rs, 0x3e01, "outdoor_temp")) != 0 {
		t.Error("tag-0x00 TLV entries must be skipped")
	}
}

func TestUndecodedFrameNotOK(t *testing.T) {
	// Register 000715 has no verified layout — must archive, not decode.
	f := bus.Frame{Src: 0x3e01, Dst: 0x2001, Op: bus.OpAck06,
		Data: []byte{0x00, 0x07, 0x15, 0x01, 0x02}}
	rs, ok := Decode(f, time.Now())
	if ok || rs != nil {
		t.Errorf("unverified register decoded: ok=%v rs=%v", ok, rs)
	}
}
