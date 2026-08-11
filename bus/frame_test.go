// bus/frame_test.go
package bus

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex fixture %q: %v", s, err)
	}
	return b
}

// CRC-16/ARC published check value.
func TestCRC16ARCCheckValue(t *testing.T) {
	if got := crc16ARC([]byte("123456789")); got != 0xBB3D {
		t.Fatalf("crc16ARC check = %#04x, want 0xBB3D", got)
	}
}

// Round-trip real payloads captured from the Hunterhill bus (2026-08-11).
func TestFrameRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		f    Frame
	}{
		{"read-000306", Frame{Dst: 0x3e01, Src: 0x2001, Op: OpRead, Data: mustHex(t, "000306")}},
		{"ack-000306", Frame{Dst: 0x2001, Src: 0x3e01, Op: OpAck06, Data: mustHex(t, "000306000000000000c8089800")}},
		{"ack-dampers", Frame{Dst: 0x2001, Src: 0x6001, Op: OpAck06, Data: mustHex(t, "0003190f0f0f0fffffffff")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wire := tc.f.Encode()
			if len(wire) != 10+len(tc.f.Data) {
				t.Fatalf("wire length = %d, want %d", len(wire), 10+len(tc.f.Data))
			}
			var got Frame
			if !got.Decode(wire) {
				t.Fatal("Decode returned false on Encode output")
			}
			if got.Dst != tc.f.Dst || got.Src != tc.f.Src || got.Op != tc.f.Op || !bytes.Equal(got.Data, tc.f.Data) {
				t.Fatalf("round trip mismatch: got %+v want %+v", got, tc.f)
			}
		})
	}
}

func TestDecodeRejectsCorruptCRC(t *testing.T) {
	wire := (&Frame{Dst: 0x2001, Src: 0x3e01, Op: OpAck06, Data: mustHex(t, "000306000000000000c8089800")}).Encode()
	wire[len(wire)-1] ^= 0xff
	var f Frame
	if f.Decode(wire) {
		t.Fatal("Decode accepted a corrupt CRC")
	}
}

func TestDecodeRejectsAllZeros(t *testing.T) {
	var f Frame
	if f.Decode(make([]byte, 12)) {
		t.Fatal("Decode accepted an all-zero buffer")
	}
}

func TestOpString(t *testing.T) {
	if s := opString(OpRead); s != "READ" {
		t.Fatalf("opString(OpRead) = %q, want READ", s)
	}
	if s := opString(0xaa); s != "UNKNOWN(aa)" {
		t.Fatalf("opString(0xaa) = %q", s)
	}
}
