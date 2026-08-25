// Package sam sends active read-only register requests as the SAM address
// (0x92) and matches replies from the passive decode stream. This is the
// ONLY code in infinid that transmits on the bus, and it can only build
// OpRead frames — there is no write path by construction (v1 read-only).
package sam

import (
	"io"
	"time"

	"github.com/utkjmitch/infinid/bus"
)

// Target is one register polled from the wall control.
type Target struct {
	Reg      [3]byte
	Interval time.Duration
}

const (
	interSendGap   = 2 * time.Second // minimum spacing between any two requests
	pendingTimeout = 2 * time.Second
	maxBackoff     = 10 * time.Minute
	minInterval    = 5 * time.Second // floor: the bus is shared airtime
)

type target struct {
	Target
	due     time.Time
	backoff time.Duration
}

// nextBackoff computes the next backoff duration for a target that just
// failed (timeout or NACK): 4s on first failure, doubling thereafter,
// clamped to maxBackoff.
func nextBackoff(cur time.Duration) time.Duration {
	if cur == 0 {
		return 4 * time.Second
	}
	cur *= 2
	if cur > maxBackoff {
		cur = maxBackoff
	}
	return cur
}

// Scheduler paces read requests: one outstanding at a time, a global
// inter-send gap, exponential backoff per target on timeout or NACK.
// Callers drive it from the bus read loop: Tick after each frame (the
// natural inter-frame gap), NoteFrame for every decoded frame.
//
// Not safe for concurrent use: Tick, NoteFrame and Failures must all be
// called from the single bus read-loop goroutine.
type Scheduler struct {
	w        io.Writer
	targets  []*target
	pending  *target
	sentAt   time.Time
	lastSend time.Time
	failures int
}

// New builds a scheduler writing requests to w (the serial port). Each
// Target's Interval is floored to minInterval — community configs may
// specify aggressive intervals, but the bus is shared airtime.
func New(w io.Writer, targets []Target) *Scheduler {
	s := &Scheduler{w: w}
	for _, t := range targets {
		if t.Interval < minInterval {
			t.Interval = minInterval
		}
		s.targets = append(s.targets, &target{Target: t})
	}
	return s
}

// Tick sends the most-overdue target if none is pending and the inter-send
// gap has passed. A pending request past its timeout counts as a failure
// and applies backoff to that target. now should come from time.Now()
// (monotonic) — a backwards wall-clock step must not be able to stall the
// pending timeout.
func (s *Scheduler) Tick(now time.Time) {
	if s.pending != nil {
		if now.Sub(s.sentAt) < pendingTimeout {
			return
		}
		s.failures++
		p := s.pending
		p.backoff = nextBackoff(p.backoff)
		p.due = now.Add(p.backoff)
		s.pending = nil
	}
	if now.Sub(s.lastSend) < interSendGap && !s.lastSend.IsZero() {
		return
	}
	var pick *target
	for _, t := range s.targets {
		if now.Before(t.due) {
			continue
		}
		if pick == nil || t.due.Before(pick.due) {
			pick = t
		}
	}
	if pick == nil {
		return
	}
	f := bus.Frame{Dst: bus.DevWallControl, Src: bus.DevSAM, Op: bus.OpRead,
		Data: pick.Reg[:]}
	if _, err := s.w.Write(f.Encode()); err != nil {
		s.failures++
		pick.due = now.Add(interSendGap)
		// The inter-send gap is consumed by the attempt, not only by
		// success: a partial write may have hit the wire, so we must not
		// immediately retry and risk two overlapping requests on air.
		s.lastSend = now
		return
	}
	s.pending = pick
	s.sentAt = now
	s.lastSend = now
}

// NoteFrame observes a decoded frame; a wall-control reply addressed to the
// SAM completes the pending transaction. A successful ACK for the pending
// register resets backoff and reschedules the target by its Interval. A
// NACK completes the transaction as a failure immediately instead of
// burning the 2s timeout; the register cannot be matched against the
// pending target (a NACK payload is a reason code, not the register), which
// is acceptable because only one request is ever outstanding.
func (s *Scheduler) NoteFrame(f bus.Frame, now time.Time) {
	if s.pending == nil {
		return
	}
	if f.Src != bus.DevWallControl || f.Dst != bus.DevSAM {
		return
	}
	if f.Op == bus.OpNack {
		s.failures++
		p := s.pending
		p.backoff = nextBackoff(p.backoff)
		p.due = now.Add(p.backoff)
		s.pending = nil
		return
	}
	if f.Op != bus.OpAck06 || len(f.Data) < 3 {
		return
	}
	r := s.pending.Reg
	if f.Data[0] != r[0] || f.Data[1] != r[1] || f.Data[2] != r[2] {
		return
	}
	s.pending.backoff = 0
	s.pending.due = now.Add(s.pending.Interval)
	s.pending = nil
}

// Failures returns the cumulative timeout/NACK/write-failure count
// (published as daemon health).
func (s *Scheduler) Failures() int { return s.failures }
