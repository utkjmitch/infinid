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
	// The first 5201/000302 fixture frame has 6 tag-0x01 TLV rows (ids
	// 0x11, 0x12, 0x30, 0x4a, 0x4b, 0x45); 0x4b is unverified (ADR-0001) so
	// exactly 5 readings must come out of that one frame — not 6.
	f := fixtures(t)[7] // 5201, 000302, first sample: testdata/frames.jsonl line 8
	if f.F.Src != 0x5201 {
		t.Fatalf("fixture[7] src = %04x, want 5201 (fixture order changed?)", f.F.Src)
	}
	got, ok := Decode(f.F, f.TS)
	if !ok {
		t.Fatal("Decode on verified 000302@5201 frame returned ok=false")
	}
	if len(got) != 5 {
		t.Errorf("readings from first 5201/000302 frame = %d, want 5 (0x4b must be excluded)", len(got))
	}
}

// TestDecodeOKOnHappyPath pins that a known-good verified frame reports
// ok=true, not just a non-nil/non-empty slice.
func TestDecodeOKOnHappyPath(t *testing.T) {
	f := fixtures(t)[7] // 5201, 000302, first sample: testdata/frames.jsonl line 8
	if f.F.Src != 0x5201 {
		t.Fatalf("fixture[7] src = %04x, want 5201 (fixture order changed?)", f.F.Src)
	}
	_, ok := Decode(f.F, f.TS)
	if !ok {
		t.Error("Decode on verified 000302@5201 frame: ok = false, want true")
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

// TestDecodeMalformedInput pins no-panic behavior and reading counts for
// truncated or partial frames.
func TestDecodeMalformedInput(t *testing.T) {
	cases := []struct {
		name    string
		data    []byte
		wantOK  bool
		wantLen int
	}{
		{
			name:    "nil Data",
			data:    nil,
			wantOK:  false,
			wantLen: 0,
		},
		{
			name:    "3-byte Data (bare register, no payload)",
			data:    []byte{0x00, 0x03, 0x02},
			wantOK:  false,
			wantLen: 0,
		},
		{
			name: "000302 with one valid row plus 2 dangling trailing bytes",
			// register(3) + [tag=01 id=0x11 value=0x04d9](4) + dangling(2)
			data:    []byte{0x00, 0x03, 0x02, 0x01, 0x11, 0x04, 0xd9, 0xaa, 0xbb},
			wantOK:  true,
			wantLen: 1,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := bus.Frame{Src: 0x5201, Dst: 0x2001, Op: bus.OpAck06, Data: c.data}
			rs, ok := Decode(f, time.Now())
			if ok != c.wantOK {
				t.Errorf("ok = %v, want %v", ok, c.wantOK)
			}
			if len(rs) != c.wantLen {
				t.Errorf("len(readings) = %d, want %d", len(rs), c.wantLen)
			}
		})
	}
}

// TestDecodeVerifiedEmptyIsOK pins that a verified register whose decoder
// legitimately yields zero readings (e.g. an all-absent TLV set) still
// reports ok=true — it must not be misrouted into the archive-unknown path.
func TestDecodeVerifiedEmptyIsOK(t *testing.T) {
	// register 000302, one TLV row with tag=0x00 (absent) for a known id.
	f := bus.Frame{Src: 0x6001, Dst: 0x2001, Op: bus.OpAck06,
		Data: []byte{0x00, 0x03, 0x02, 0x00, 0x11, 0x04, 0xd9}}
	rs, ok := Decode(f, time.Now())
	if !ok {
		t.Fatal("verified register with zero present TLV rows: ok = false, want true")
	}
	if len(rs) != 0 {
		t.Errorf("readings = %d, want 0", len(rs))
	}
}

// TestTLVTemperatureNegative pins the int16 (signed) decode: 0xff60 = -160,
// /16 = -10.0 °F. Real range not yet live-verified (winter capture pending)
// but the layout is documented (docs/protocol-tables.md Conventions).
func TestTLVTemperatureNegative(t *testing.T) {
	f := bus.Frame{Src: 0x5201, Dst: 0x2001, Op: bus.OpAck06,
		Data: []byte{0x00, 0x03, 0x02, 0x01, 0x11, 0xff, 0x60}}
	rs, ok := Decode(f, time.Now())
	if !ok {
		t.Fatal("Decode returned ok=false")
	}
	got := find(rs, 0x5201, "outdoor_temp")
	if len(got) != 1 {
		t.Fatalf("outdoor_temp count = %d, want 1", len(got))
	}
	if got[0].Value != -10.0 {
		t.Errorf("outdoor_temp = %v, want -10.0", got[0].Value)
	}
}
