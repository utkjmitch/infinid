// Package capture keeps a ring of recent frames and optionally appends each
// one as a JSONL line — the raw material Phase 2 turns into decoder fixtures.
package capture

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
)

// Record is one captured frame with receive timestamp.
type Record struct {
	TS   time.Time
	Src  uint16
	Dst  uint16
	Op   uint8
	Data []byte
	Raw  []byte
}

type jsonRecord struct {
	TS   string `json:"ts"`
	Src  string `json:"src"`
	Dst  string `json:"dst"`
	Op   string `json:"op"`
	Data string `json:"data"`
	Raw  string `json:"raw"`
}

// Recorder is a bounded ring of Records with optional JSONL append.
type Recorder struct {
	mu   sync.Mutex
	ring []Record
	max  int
	w    io.Writer
}

// New creates a Recorder holding the last max records; if w is non-nil each
// record is also appended to it as one JSON line.
func New(max int, w io.Writer) *Recorder {
	if max < 1 {
		max = 1
	}
	return &Recorder{max: max, w: w}
}

// Add appends rec to the ring (evicting oldest) and writes one JSONL line.
func (r *Recorder) Add(rec Record) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ring = append(r.ring, rec)
	if len(r.ring) > r.max {
		r.ring = r.ring[len(r.ring)-r.max:]
	}
	if r.w != nil {
		line, _ := json.Marshal(jsonRecord{
			TS:   rec.TS.UTC().Format(time.RFC3339Nano),
			Src:  fmt.Sprintf("%04x", rec.Src),
			Dst:  fmt.Sprintf("%04x", rec.Dst),
			Op:   fmt.Sprintf("%02x", rec.Op),
			Data: hex.EncodeToString(rec.Data),
			Raw:  hex.EncodeToString(rec.Raw),
		})
		r.w.Write(append(line, '\n'))
	}
}

// Snapshot returns a copy of the ring, oldest first.
// Records share underlying Data/Raw slices with the ring; callers must not mutate them.
func (r *Recorder) Snapshot() []Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Record, len(r.ring))
	copy(out, r.ring)
	return out
}
