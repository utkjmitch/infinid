package sam

import (
	"bytes"
	"errors"
	"math/rand"
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

// TestBackoffClampsAtMax drives one target through many consecutive
// timeouts and asserts the backoff never exceeds the documented 10m cap.
func TestBackoffClampsAtMax(t *testing.T) {
	var buf bytes.Buffer
	s := New(&buf, []Target{
		{Reg: [3]byte{0x00, 0x3b, 0x02}, Interval: 10 * time.Second},
	})
	tgt := s.targets[0]
	now := t0
	for i := 0; i < 12; i++ {
		if tgt.due.After(now) {
			now = tgt.due
		}
		s.Tick(now) // send (or first-ever send)
		now = now.Add(pendingTimeout)
		s.Tick(now) // observe timeout, bump backoff
	}
	if tgt.backoff > maxBackoff {
		t.Fatalf("backoff = %v, want <= %v (clamp)", tgt.backoff, maxBackoff)
	}
	if tgt.backoff != maxBackoff {
		t.Fatalf("backoff = %v, want exactly %v after enough failures (clamp not reached)", tgt.backoff, maxBackoff)
	}
	if got := tgt.due.Sub(now); got > maxBackoff {
		t.Fatalf("due = now+%v, want <= now+%v", got, maxBackoff)
	}
}

// errWriter always fails, simulating a serial port write error.
type errWriter struct{}

func (errWriter) Write(p []byte) (int, error) { return 0, errors.New("boom") }

// TestWriteFailureConsumesGap: with a writer that always errors and several
// due targets, attempts must still be spaced by the inter-send gap — the
// gap is consumed by the attempt itself, not only by a successful write.
func TestWriteFailureConsumesGap(t *testing.T) {
	s := New(errWriter{}, []Target{
		{Reg: [3]byte{0x00, 0x01, 0x00}, Interval: time.Second},
		{Reg: [3]byte{0x00, 0x02, 0x00}, Interval: time.Second},
		{Reg: [3]byte{0x00, 0x03, 0x00}, Interval: time.Second},
	})
	now := t0
	var attemptTimes []time.Time
	prevFailures := 0
	for i := 0; i < 50; i++ {
		s.Tick(now)
		if s.Failures() > prevFailures {
			attemptTimes = append(attemptTimes, now)
			prevFailures = s.Failures()
		}
		now = now.Add(200 * time.Millisecond)
	}
	if len(attemptTimes) < 2 {
		t.Fatalf("expected multiple write attempts across 10s, got %d", len(attemptTimes))
	}
	for i := 1; i < len(attemptTimes); i++ {
		if gap := attemptTimes[i].Sub(attemptTimes[i-1]); gap < interSendGap {
			t.Fatalf("attempts %d and %d only %v apart, want >= %v", i-1, i, gap, interSendGap)
		}
	}
}

// TestNackFastFails: a NACK completes the pending transaction immediately
// as a failure (rather than waiting out the 2s timeout), backs off the
// NACKed target, and lets a due sibling target send on the next tick past
// the inter-send gap.
func TestNackFastFails(t *testing.T) {
	s, buf := newTest()
	s.Tick(t0) // sends 3B02, now pending
	buf.Reset()

	s.NoteFrame(bus.Frame{Src: bus.DevWallControl, Dst: bus.DevSAM, Op: bus.OpNack,
		Data: []byte{0x04}}, t0.Add(100*time.Millisecond))
	if s.Failures() != 1 {
		t.Fatalf("failures = %d, want 1", s.Failures())
	}
	if s.pending != nil {
		t.Fatal("pending must be cleared immediately after a NACK")
	}

	s.Tick(t0.Add(2100 * time.Millisecond)) // past the inter-send gap
	frames := readFrames(t, buf)
	if len(frames) != 1 || !bytes.Equal(frames[0].Data, []byte{0x00, 0x42, 0x02}) {
		t.Fatalf("sibling target not sent after NACK: %+v", frames)
	}

	nacked := s.targets[0]
	if nacked.backoff == 0 {
		t.Error("NACKed target must be backed off, not immediately re-due")
	}
}

// TestBackoffResetsOnSuccessAndReschedules: after a timeout+backoff, a
// successful ACK on the retry clears backoff and reschedules the target by
// its normal Interval (not by the backoff duration).
func TestBackoffResetsOnSuccessAndReschedules(t *testing.T) {
	var buf bytes.Buffer
	s := New(&buf, []Target{
		{Reg: [3]byte{0x00, 0x3b, 0x02}, Interval: 10 * time.Second},
	})
	tgt := s.targets[0]

	s.Tick(t0)
	buf.Reset()
	s.Tick(t0.Add(pendingTimeout)) // times out -> backoff
	if tgt.backoff != 4*time.Second {
		t.Fatalf("backoff = %v, want 4s", tgt.backoff)
	}

	retryAt := tgt.due // t0 + pendingTimeout + 4s
	s.Tick(retryAt)    // retry send
	frames := readFrames(t, &buf)
	if len(frames) != 1 {
		t.Fatalf("expected a retry send, got %d frames", len(frames))
	}
	buf.Reset()

	ackAt := retryAt.Add(50 * time.Millisecond)
	s.NoteFrame(bus.Frame{Src: bus.DevWallControl, Dst: bus.DevSAM, Op: bus.OpAck06,
		Data: []byte{0x00, 0x3b, 0x02, 0x01}}, ackAt)
	if tgt.backoff != 0 {
		t.Fatalf("backoff = %v, want 0 after success", tgt.backoff)
	}
	wantDue := ackAt.Add(tgt.Interval)
	if !tgt.due.Equal(wantDue) {
		t.Fatalf("due = %v, want %v (now+Interval, not now+backoff)", tgt.due, wantDue)
	}

	s.Tick(wantDue.Add(-time.Second))
	if got := len(readFrames(t, &buf)); got != 0 {
		t.Fatalf("sent %d frames before its rescheduled Interval elapsed", got)
	}

	s.Tick(wantDue)
	frames = readFrames(t, &buf)
	if len(frames) != 1 || !bytes.Equal(frames[0].Data, []byte{0x00, 0x3b, 0x02}) {
		t.Fatalf("expected send at Interval, got %+v", frames)
	}
}

// TestNoteFrameIgnoresWrongReplies: a reply with the wrong register, wrong
// op, or wrong source must not complete the pending transaction — it can
// only ever be cleared by its own timeout.
func TestNoteFrameIgnoresWrongReplies(t *testing.T) {
	cases := []struct {
		name string
		f    bus.Frame
	}{
		{"wrong register", bus.Frame{Src: bus.DevWallControl, Dst: bus.DevSAM, Op: bus.OpAck06,
			Data: []byte{0x00, 0x42, 0x02, 0x01}}},
		{"wrong op", bus.Frame{Src: bus.DevWallControl, Dst: bus.DevSAM, Op: bus.OpWrite,
			Data: []byte{0x00, 0x3b, 0x02, 0x01}}},
		{"wrong src", bus.Frame{Src: bus.DevAirHandler, Dst: bus.DevSAM, Op: bus.OpAck06,
			Data: []byte{0x00, 0x3b, 0x02, 0x01}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newTest()
			s.Tick(t0)
			orig := s.pending
			if orig == nil {
				t.Fatal("setup: expected a pending request")
			}
			s.NoteFrame(tc.f, t0.Add(100*time.Millisecond))
			if s.pending != orig {
				t.Fatal("a wrong reply must not complete the pending request")
			}
			prevFailures := s.Failures()
			// Tick may immediately pick up the next due target after the
			// timeout clears orig, so assert on orig's own state rather
			// than the blanket s.pending — it can be non-nil again here,
			// just pointing at a different target.
			s.Tick(t0.Add(pendingTimeout))
			if s.Failures() != prevFailures+1 {
				t.Error("timeout should have counted as a failure")
			}
			if orig.backoff == 0 {
				t.Error("the original pending target should have been backed off via its own timeout")
			}
		})
	}
}

// flakyWriter forwards to an in-memory buffer but fails writes at random,
// simulating a lossy serial port for the safety sweep below.
type flakyWriter struct {
	buf   bytes.Buffer
	rng   *rand.Rand
	failP float64
}

func (w *flakyWriter) Write(p []byte) (int, error) {
	if w.rng.Float64() < w.failP {
		return 0, errors.New("flaky write")
	}
	return w.buf.Write(p)
}

// TestSafetySweep pins the core safety claim over a large, deterministic,
// randomized sequence of ticks and frames (successes, NACKs, wrong-register
// ACKs, foreign-device noise, and write failures): every single byte
// sequence the scheduler ever puts on the wire decodes as an OpRead frame
// addressed SAM -> wall control. Nothing else may ever be transmitted.
func TestSafetySweep(t *testing.T) {
	rng := rand.New(rand.NewSource(20260825))
	w := &flakyWriter{rng: rand.New(rand.NewSource(7)), failP: 0.2}
	regs := [][3]byte{{0x00, 0x3b, 0x02}, {0x00, 0x42, 0x02}, {0x00, 0x07, 0x15}}
	var targets []Target
	for _, r := range regs {
		targets = append(targets, Target{Reg: r, Interval: time.Duration(1+rng.Intn(30)) * time.Second})
	}
	s := New(w, targets)

	now := t0
	for i := 0; i < 5000; i++ {
		now = now.Add(time.Duration(50+rng.Intn(500)) * time.Millisecond)
		s.Tick(now)
		switch rng.Intn(4) {
		case 0: // plausible or garbage ACK
			reg := regs[rng.Intn(len(regs))]
			s.NoteFrame(bus.Frame{Src: bus.DevWallControl, Dst: bus.DevSAM, Op: bus.OpAck06,
				Data: []byte{reg[0], reg[1], reg[2], byte(rng.Intn(256))}}, now)
		case 1: // NACK
			s.NoteFrame(bus.Frame{Src: bus.DevWallControl, Dst: bus.DevSAM, Op: bus.OpNack,
				Data: []byte{byte(rng.Intn(256))}}, now)
		case 2: // unrelated bus noise
			s.NoteFrame(bus.Frame{Src: bus.DevAirHandler, Dst: bus.DevDCM1, Op: bus.OpWrite,
				Data: []byte{0x01, 0x02, 0x03}}, now)
		default: // no frame this tick
		}
	}

	b := w.buf.Bytes()
	frameCount := 0
	for len(b) >= 13 {
		var f bus.Frame
		if !f.Decode(b[:13]) {
			t.Fatalf("undecodable frame in sweep output at frame %d: %x", frameCount, b[:13])
		}
		if f.Op != bus.OpRead {
			t.Fatalf("SAFETY VIOLATION at frame %d: op = %02x, want %02x (OpRead)", frameCount, f.Op, bus.OpRead)
		}
		if f.Src != bus.DevSAM || f.Dst != bus.DevWallControl {
			t.Fatalf("SAFETY VIOLATION at frame %d: addressing %04x -> %04x", frameCount, f.Src, f.Dst)
		}
		frameCount++
		b = b[13:]
	}
	if len(b) != 0 {
		t.Fatalf("trailing undecodable bytes after sweep: %x", b)
	}
	if frameCount == 0 {
		t.Fatal("sweep produced no frames at all — test is vacuous")
	}
}
