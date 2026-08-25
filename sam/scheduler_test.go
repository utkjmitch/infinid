package sam

import (
	"bytes"
	"testing"
	"time"

	"github.com/utkjmitch/infinid/bus"
)

var t0 = time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)

func newTest() (*Scheduler, *bytes.Buffer) {
	var buf bytes.Buffer
	s := New(&buf, []Target{
		{Reg: [3]byte{0x00, 0x3b, 0x02}, Interval: 10 * time.Second},
		{Reg: [3]byte{0x00, 0x42, 0x02}, Interval: time.Hour},
	})
	return s, &buf
}

// readFrames decodes every frame written to the fake port.
func readFrames(t *testing.T, buf *bytes.Buffer) []bus.Frame {
	t.Helper()
	var out []bus.Frame
	b := buf.Bytes()
	for len(b) > 0 {
		var f bus.Frame
		// Request frames are fixed-size: 8 header + 3 reg + 2 crc = 13.
		if len(b) < 13 || !f.Decode(b[:13]) {
			t.Fatalf("undecodable frame bytes: %x", b)
		}
		out = append(out, f)
		b = b[13:]
	}
	return out
}

func TestSendsReadOnlyRequests(t *testing.T) {
	s, buf := newTest()
	s.Tick(t0)
	frames := readFrames(t, buf)
	if len(frames) != 1 {
		t.Fatalf("frames = %d, want 1 (one outstanding at a time)", len(frames))
	}
	f := frames[0]
	if f.Op != bus.OpRead {
		t.Fatalf("op = %02x — the scheduler must NEVER send anything but READ", f.Op)
	}
	if f.Src != bus.DevSAM || f.Dst != bus.DevWallControl {
		t.Errorf("addressing wrong: %04x -> %04x", f.Src, f.Dst)
	}
	if !bytes.Equal(f.Data, []byte{0x00, 0x3b, 0x02}) {
		t.Errorf("reg = %x", f.Data)
	}
}

func TestSingleOutstandingAndSpacing(t *testing.T) {
	s, buf := newTest()
	s.Tick(t0)
	s.Tick(t0.Add(100 * time.Millisecond)) // pending → no second send
	if got := len(readFrames(t, buf)); got != 1 {
		t.Fatalf("sent %d frames with one pending", got)
	}
	// ACK arrives; next target still waits for the 2s inter-send gap.
	s.NoteFrame(bus.Frame{Src: bus.DevWallControl, Dst: bus.DevSAM, Op: bus.OpAck06,
		Data: []byte{0x00, 0x3b, 0x02, 0x01}}, t0.Add(200*time.Millisecond))
	buf.Reset()
	s.Tick(t0.Add(500 * time.Millisecond))
	if got := len(readFrames(t, buf)); got != 0 {
		t.Fatal("must respect the 2s inter-send gap")
	}
	s.Tick(t0.Add(3 * time.Second))
	frames := readFrames(t, buf)
	if len(frames) != 1 || !bytes.Equal(frames[0].Data, []byte{0x00, 0x42, 0x02}) {
		t.Fatalf("second target not sent after gap: %+v", frames)
	}
}

func TestTimeoutBackoff(t *testing.T) {
	s, buf := newTest()
	s.Tick(t0)
	buf.Reset()
	// No reply. After the 2s pending timeout the target backs off (4s, 8s...).
	s.Tick(t0.Add(3 * time.Second))
	// 3B02 was due at t0+10s but is now backed off; 4202 sends instead.
	frames := readFrames(t, buf)
	if len(frames) != 1 || !bytes.Equal(frames[0].Data, []byte{0x00, 0x42, 0x02}) {
		t.Fatalf("expected 4202 after 3B02 timeout, got %+v", frames)
	}
	// Repeated failure marks the target failing.
	if s.Failures() == 0 {
		t.Error("timeout must count as a failure")
	}
}
