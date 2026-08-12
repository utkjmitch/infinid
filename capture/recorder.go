// Recorder keeps a ring of recent frames and optionally appends each one as a
// JSONL line — the raw material Phase 2 turns into decoder fixtures.
package capture

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
)

type Record struct {
	TS   time.Time
	Src  uint16
	Dst  uint16
	Op   uint8
	Data []byte
}

type jsonRecord struct {
	TS   string `json:"ts"`
	Src  string `json:"src"`
	Dst  string `json:"dst"`
	Op   string `json:"op"`
	Data string `json:"data"`
}

type Recorder struct {
	mu   sync.Mutex
	ring []Record
	max  int
	w    io.Writer
}

// New creates a Recorder holding the last max records; if w is non-nil each
// record is also appended to it as one JSON line.
func New(max int, w io.Writer) *Recorder {
	return &Recorder{max: max, w: w}
}

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
		})
		r.w.Write(append(line, '\n'))
	}
}

// Snapshot returns a copy of the ring, oldest first.
func (r *Recorder) Snapshot() []Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Record, len(r.ring))
	copy(out, r.ring)
	return out
}
