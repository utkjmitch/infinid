// bus/decoder_test.go
package bus

import (
	"bytes"
	"io"
	"testing"
)

// chunkReader returns bytes a few at a time to exercise partial reads.
type chunkReader struct {
	data  []byte
	chunk int
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if len(c.data) == 0 {
		return 0, io.EOF
	}
	n := c.chunk
	if n > len(c.data) {
		n = len(c.data)
	}
	copy(p, c.data[:n])
	c.data = c.data[n:]
	return n, nil
}

func testFrames(t *testing.T) []Frame {
	return []Frame{
		{Dst: 0x3e01, Src: 0x2001, Op: OpRead, Data: mustHex(t, "000306")},
		{Dst: 0x2001, Src: 0x3e01, Op: OpAck06, Data: mustHex(t, "000306000000000000c8089800")},
		{Dst: 0x2001, Src: 0x6001, Op: OpAck06, Data: mustHex(t, "0003190f0f0f0fffffffff")},
	}
}

func collect(t *testing.T, d *Decoder) []Frame {
	t.Helper()
	var out []Frame
	for {
		f, err := d.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		out = append(out, f)
	}
}

func TestDecoderSplitsCleanStream(t *testing.T) {
	frames := testFrames(t)
	var stream bytes.Buffer
	for _, f := range frames {
		stream.Write(f.Encode())
	}
	d := NewDecoder(&chunkReader{data: stream.Bytes(), chunk: 7})
	got := collect(t, d)
	if len(got) != len(frames) {
		t.Fatalf("decoded %d frames, want %d", len(got), len(frames))
	}
	for i := range got {
		if got[i].String() != frames[i].String() {
			t.Fatalf("frame %d = %s, want %s", i, got[i], frames[i])
		}
	}
}

func TestDecoderResyncsAfterGarbage(t *testing.T) {
	frames := testFrames(t)
	var stream bytes.Buffer
	stream.Write([]byte{0xde, 0xad, 0xbe, 0xef, 0x42}) // mid-frame join garbage
	for _, f := range frames {
		stream.Write(f.Encode())
	}
	d := NewDecoder(&chunkReader{data: stream.Bytes(), chunk: 3})
	got := collect(t, d)
	if len(got) != len(frames) {
		t.Fatalf("decoded %d frames after garbage, want %d", len(got), len(frames))
	}
	if d.Resyncs() == 0 {
		t.Fatal("expected resync counter > 0")
	}
}
