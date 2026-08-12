// bus/decoder.go

package bus

import "io"

// Decoder splits a byte stream into validated frames, resyncing byte-by-byte
// through garbage (the strategy infinitive's reader loop uses: if a candidate
// frame fails CRC, slide the window one byte and try again).
type Decoder struct {
	r       io.Reader
	pending []byte
	scratch []byte
	resyncs uint64
}

// NewDecoder wraps r in a frame splitter.
func NewDecoder(r io.Reader) *Decoder {
	return &Decoder{r: r, scratch: make([]byte, 1024)}
}

// Resyncs reports how many bytes have been skipped resynchronizing.
func (d *Decoder) Resyncs() uint64 { return d.resyncs }

// Next blocks until a validated frame is available or the reader errors.
func (d *Decoder) Next() (Frame, error) {
	zeroReads := 0
	for {
		for len(d.pending) >= 10 {
			// Cheap header gate: bytes 5-6 are always zero on this bus.
			// Reject before trusting the length byte, so garbage can't
			// stall delivery of a valid frame sitting one byte later,
			// and CRC-only false positives get a 2^16 haircut.
			if d.pending[5] != 0 || d.pending[6] != 0 {
				d.pending = d.pending[1:]
				d.resyncs++
				continue
			}
			l := int(d.pending[4]) + 10
			if len(d.pending) < l {
				break // need more bytes for this candidate
			}
			var f Frame
			if f.Decode(d.pending[:l]) {
				d.pending = d.pending[l:]
				return f, nil
			}
			// Corrupt or misaligned: slide one byte and retry.
			d.pending = d.pending[1:]
			d.resyncs++
		}
		n, err := d.r.Read(d.scratch)
		if n > 0 {
			zeroReads = 0
			d.pending = append(d.pending, d.scratch[:n]...)
			continue
		}
		if err != nil {
			// Stream ended (or errored) while a garbage length byte demanded
			// more bytes than exist: slide to salvage any complete frames
			// still buried in the buffer before surfacing the error.
			if len(d.pending) >= 10 {
				d.pending = d.pending[1:]
				d.resyncs++
				continue
			}
			return Frame{}, err
		}
		// (0, nil) is legal io.Reader behavior and the serial driver's
		// idle return; without this guard it busy-spins a full core.
		zeroReads++
		if zeroReads >= 100 {
			return Frame{}, io.ErrNoProgress
		}
	}
}
