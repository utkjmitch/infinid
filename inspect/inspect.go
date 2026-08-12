// Package inspect turns a capture.jsonl stream (see capture/recorder.go)
// into decode leads: parsed records, register grouping by owning device,
// change timelines, and windowed before/after diffs. It has no knowledge of
// the bus wire format beyond the JSONL schema and the register-keying rule
// below; register semantics are Phase 2's job.
//
// Register keying: for ops READ (0x0b), WRITE (0x0c) and ACK06 (0x06) whose
// Data is at least 3 bytes, the register is the hex of Data[0:3]. The
// register's owner is Src for ACK06 replies (the device answering) and Dst
// for READ/WRITE (the device being addressed) — both name the same physical
// device that holds the register. Frames that don't meet the length
// requirement, and frames with any other op, are not keyable by register;
// Group buckets those under the sentinel register "-".
package inspect

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Bus ops relevant to register keying and alarms. Mirrors bus.Op* without
// importing the bus package (inspect is stdlib-only and decoupled from the
// wire codec).
const (
	opAck06 = uint8(0x06)
	opRead  = uint8(0x0b)
	opWrite = uint8(0x0c)
)

// Rec is one parsed capture record.
type Rec struct {
	TS   time.Time
	Src  uint16
	Dst  uint16
	Op   uint8
	Data []byte
	Raw  []byte // absent in older captures; may be nil
}

// jsonRec mirrors the on-disk JSONL schema written by capture/recorder.go.
type jsonRec struct {
	TS   string `json:"ts"`
	Src  string `json:"src"`
	Dst  string `json:"dst"`
	Op   string `json:"op"`
	Data string `json:"data"`
	Raw  string `json:"raw"`
}

// ParseAll reads newline-delimited capture JSON from r and returns the
// parsed records in file order, the count of lines skipped because they
// were blank, malformed JSON, or had unparseable fields, and any I/O error
// encountered while reading. The "raw" field is optional; its absence does
// not affect parsing.
func ParseAll(r io.Reader) ([]Rec, int, error) {
	var recs []Rec
	skipped := 0

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		rec, ok := parseLine(line)
		if !ok {
			skipped++
			continue
		}
		recs = append(recs, rec)
	}
	if err := sc.Err(); err != nil {
		return recs, skipped, err
	}
	return recs, skipped, nil
}

func parseLine(line string) (Rec, bool) {
	var jr jsonRec
	if err := json.Unmarshal([]byte(line), &jr); err != nil {
		return Rec{}, false
	}
	ts, err := time.Parse(time.RFC3339Nano, jr.TS)
	if err != nil {
		return Rec{}, false
	}
	src, err := parseHex16(jr.Src)
	if err != nil {
		return Rec{}, false
	}
	dst, err := parseHex16(jr.Dst)
	if err != nil {
		return Rec{}, false
	}
	op, err := parseHex8(jr.Op)
	if err != nil {
		return Rec{}, false
	}
	data, err := hex.DecodeString(jr.Data)
	if err != nil {
		return Rec{}, false
	}
	var raw []byte
	if jr.Raw != "" {
		raw, err = hex.DecodeString(jr.Raw)
		if err != nil {
			return Rec{}, false
		}
	}
	return Rec{TS: ts, Src: src, Dst: dst, Op: op, Data: data, Raw: raw}, true
}

func parseHex16(s string) (uint16, error) {
	v, err := strconv.ParseUint(s, 16, 16)
	if err != nil {
		return 0, err
	}
	return uint16(v), nil
}

func parseHex8(s string) (uint8, error) {
	v, err := strconv.ParseUint(s, 16, 8)
	if err != nil {
		return 0, err
	}
	return uint8(v), nil
}

// Key returns the register key and owning device for r, following the
// register-keying rule documented on the package. ok is false when r's op
// isn't one of READ/WRITE/ACK06, or when Data is shorter than 3 bytes.
func Key(r Rec) (reg string, owner uint16, ok bool) {
	switch r.Op {
	case opAck06:
		if len(r.Data) < 3 {
			return "", 0, false
		}
		return hex.EncodeToString(r.Data[:3]), r.Src, true
	case opRead, opWrite:
		if len(r.Data) < 3 {
			return "", 0, false
		}
		return hex.EncodeToString(r.Data[:3]), r.Dst, true
	default:
		return "", 0, false
	}
}

// GroupKey identifies one (owning device, register) pair, or the sentinel
// register "-" for frames Key can't resolve.
type GroupKey struct {
	Owner uint16
	Reg   string
}

// Stats summarizes all frames seen for one GroupKey. Count, First, and Last
// reflect every matching frame, including bare polls that carry no payload
// (e.g. a READ request). PayloadLen, Distinct, and ChangedOffsets are
// derived only from frames that carried a payload beyond the register bytes
// (register content for a keyed group, or the whole Data for the "-"
// bucket) — a bare READ contributes no payload sample.
type Stats struct {
	Count          int
	First, Last    time.Time
	PayloadLen     int
	Distinct       [][]byte // post-register payloads, first-seen order
	ChangedOffsets []int    // byte offsets that differ across any pair in Distinct
}

// Group buckets recs by (owner, register) per Key, tracking traffic counts,
// time span, and the distinct payloads observed for each bucket.
func Group(recs []Rec) map[GroupKey]*Stats {
	out := map[GroupKey]*Stats{}
	for _, r := range recs {
		reg, owner, ok := Key(r)
		var payload []byte
		if ok {
			payload = r.Data[3:]
		} else {
			reg = "-"
			owner = fallbackOwner(r)
			payload = r.Data
		}

		gk := GroupKey{Owner: owner, Reg: reg}
		st := out[gk]
		if st == nil {
			st = &Stats{First: r.TS, Last: r.TS}
			out[gk] = st
		}
		st.Count++
		if r.TS.Before(st.First) {
			st.First = r.TS
		}
		if r.TS.After(st.Last) {
			st.Last = r.TS
		}

		if len(payload) == 0 {
			continue
		}
		if len(payload) > st.PayloadLen {
			st.PayloadLen = len(payload)
		}
		if !containsBytes(st.Distinct, payload) {
			st.Distinct = append(st.Distinct, cloneBytes(payload))
		}
	}

	for _, st := range out {
		st.ChangedOffsets = changedOffsetsAcross(st.Distinct)
	}
	return out
}

// fallbackOwner picks the owner for a non-keyable frame, mirroring Key's
// owner-selection rule for its op where applicable, and defaulting to Src
// otherwise.
func fallbackOwner(r Rec) uint16 {
	switch r.Op {
	case opAck06:
		return r.Src
	case opRead, opWrite:
		return r.Dst
	default:
		return r.Src
	}
}

// Change is one payload sample in a register's timeline, recorded because
// it differs from the sample before it (or is the first sample seen).
type Change struct {
	TS          time.Time
	Payload     []byte
	ChangedFrom []int // byte offsets differing from the previous sample; empty for the first sample
}

// Changes returns, in chronological order, every payload sample for
// (owner, reg) that differs from the sample immediately before it — the
// first sample always starts the timeline. Frames with no payload (bare
// polls) are ignored.
func Changes(recs []Rec, owner uint16, reg string) []Change {
	var matched []Rec
	for _, r := range recs {
		rg, ow, ok := Key(r)
		if !ok || rg != reg || ow != owner || len(r.Data) <= 3 {
			continue
		}
		matched = append(matched, r)
	}
	sort.SliceStable(matched, func(i, j int) bool { return matched[i].TS.Before(matched[j].TS) })

	var out []Change
	var prev []byte
	havePrev := false
	for _, r := range matched {
		payload := r.Data[3:]
		if havePrev && bytes.Equal(prev, payload) {
			continue
		}
		var changedFrom []int
		if havePrev {
			changedFrom = diffOffsets(prev, payload)
		}
		cp := cloneBytes(payload)
		out = append(out, Change{TS: r.TS, Payload: cp, ChangedFrom: changedFrom})
		prev = cp
		havePrev = true
	}
	return out
}

// RegDiff is a before/after payload comparison for one (owner, reg) around
// a point in time.
type RegDiff struct {
	Owner   uint16
	Reg     string
	Before  []byte
	After   []byte
	Changed []int // byte offsets differing between Before and After
}

// DiffAt compares, for every (owner, reg) with payload-bearing traffic, the
// last payload sample in [at-window, at] against the first sample in
// (at, at+window]. A RegDiff is emitted only where both sides exist and
// differ — this is the labeled-experiment workhorse: fire an event at a
// known instant, then see which registers moved. Frames with no payload
// (bare polls) never contribute a sample.
func DiffAt(recs []Rec, at time.Time, window time.Duration) []RegDiff {
	start := at.Add(-window)
	end := at.Add(window)

	before := map[GroupKey]Rec{}
	after := map[GroupKey]Rec{}
	for _, r := range recs {
		reg, owner, ok := Key(r)
		if !ok || len(r.Data) <= 3 {
			continue
		}
		gk := GroupKey{Owner: owner, Reg: reg}

		switch {
		case !r.TS.Before(start) && !r.TS.After(at):
			if cur, exists := before[gk]; !exists || r.TS.After(cur.TS) {
				before[gk] = r
			}
		case r.TS.After(at) && !r.TS.After(end):
			if cur, exists := after[gk]; !exists || r.TS.Before(cur.TS) {
				after[gk] = r
			}
		}
	}

	keys := make([]GroupKey, 0, len(before))
	for gk := range before {
		keys = append(keys, gk)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Owner != keys[j].Owner {
			return keys[i].Owner < keys[j].Owner
		}
		return keys[i].Reg < keys[j].Reg
	})

	var out []RegDiff
	for _, gk := range keys {
		b := before[gk]
		a, ok := after[gk]
		if !ok {
			continue
		}
		bp, ap := b.Data[3:], a.Data[3:]
		if bytes.Equal(bp, ap) {
			continue
		}
		out = append(out, RegDiff{
			Owner:   gk.Owner,
			Reg:     gk.Reg,
			Before:  cloneBytes(bp),
			After:   cloneBytes(ap),
			Changed: diffOffsets(bp, ap),
		})
	}
	return out
}

// diffOffsets returns the byte offsets where a and b differ, including any
// trailing offsets covered only by the longer slice.
func diffOffsets(a, b []byte) []int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	var offs []int
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			offs = append(offs, i)
		}
	}
	max := len(a)
	if len(b) > max {
		max = len(b)
	}
	for i := n; i < max; i++ {
		offs = append(offs, i)
	}
	return offs
}

// changedOffsetsAcross returns the sorted union of diffOffsets across every
// pair of payloads.
func changedOffsetsAcross(payloads [][]byte) []int {
	set := map[int]bool{}
	for i := 0; i < len(payloads); i++ {
		for j := i + 1; j < len(payloads); j++ {
			for _, o := range diffOffsets(payloads[i], payloads[j]) {
				set[o] = true
			}
		}
	}
	if len(set) == 0 {
		return nil
	}
	offs := make([]int, 0, len(set))
	for o := range set {
		offs = append(offs, o)
	}
	sort.Ints(offs)
	return offs
}

func containsBytes(list [][]byte, b []byte) bool {
	for _, e := range list {
		if bytes.Equal(e, b) {
			return true
		}
	}
	return false
}

func cloneBytes(b []byte) []byte {
	cp := make([]byte, len(b))
	copy(cp, b)
	return cp
}
