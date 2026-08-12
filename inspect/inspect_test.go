package inspect

import (
	"encoding/hex"
	"strings"
	"testing"
	"time"
)

// fixture returns ~10 hand-written JSONL records matching the real capture
// schema (capture/recorder.go), covering: two ACK06 payload versions of
// register 000306 from src 3e01 at distinct timestamps (plus a third,
// unchanged, later reply), a READ poll pair for that same register/owner, an
// unrelated op 1e frame, a bare ACK06 with 1-byte data, a WRITE to a
// different register/owner, an ACK06 for the same register from a different
// owner, and one malformed line.
func fixture() string {
	lines := []string{
		`{"ts":"2026-08-01T12:00:00Z","src":"2001","dst":"3e01","op":"0b","data":"000306"}`,
		`{"ts":"2026-08-01T12:00:01Z","src":"3e01","dst":"2001","op":"06","data":"00030611223344","raw":"aa00030611223344bb"}`,
		`{"ts":"2026-08-01T12:00:30Z","src":"2001","dst":"3e01","op":"0b","data":"000306"}`,
		`{"ts":"2026-08-01T12:00:31Z","src":"3e01","dst":"2001","op":"06","data":"00030611ff3344"}`,
		`{"ts":"2026-08-01T12:00:40Z","src":"4001","dst":"0000","op":"1e","data":"0102"}`,
		`{"ts":"2026-08-01T12:00:41Z","src":"5001","dst":"2001","op":"06","data":"99"}`,
		`{"ts":"2026-08-01T12:00:42Z","src":"2001","dst":"6001","op":"0c","data":"000319aabbcc"}`,
		`{"ts":"2026-08-01T12:02:00Z","src":"3e01","dst":"2001","op":"06","data":"00030611ff3344"}`,
		`{"ts":"2026-08-01T12:02:01Z","src":"6001","dst":"2001","op":"06","data":"000306aaaaaaaa"}`,
		`{not valid json`,
	}
	return strings.Join(lines, "\n") + "\n"
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex fixture %q: %v", s, err)
	}
	return b
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("bad time fixture %q: %v", s, err)
	}
	return ts
}

func TestParseAll(t *testing.T) {
	recs, skipped, err := ParseAll(strings.NewReader(fixture()))
	if err != nil {
		t.Fatalf("ParseAll error: %v", err)
	}
	if skipped != 1 {
		t.Fatalf("skipped = %d, want 1", skipped)
	}
	if len(recs) != 9 {
		t.Fatalf("len(recs) = %d, want 9", len(recs))
	}

	want := recs[1] // second line: ACK06 v1
	if !want.TS.Equal(mustTime(t, "2026-08-01T12:00:01Z")) {
		t.Errorf("recs[1].TS = %v", want.TS)
	}
	if want.Src != 0x3e01 {
		t.Errorf("recs[1].Src = %04x, want 3e01", want.Src)
	}
	if want.Dst != 0x2001 {
		t.Errorf("recs[1].Dst = %04x, want 2001", want.Dst)
	}
	if want.Op != 0x06 {
		t.Errorf("recs[1].Op = %02x, want 06", want.Op)
	}
	if hex.EncodeToString(want.Data) != "00030611223344" {
		t.Errorf("recs[1].Data = %x", want.Data)
	}

	// Order preserved.
	if !recs[0].TS.Before(recs[1].TS) || !recs[1].TS.Before(recs[2].TS) {
		t.Errorf("records not in source order: %v", recs)
	}

	// Older-schema lines lacking "raw" must still parse (e.g. recs[0]).
	if len(recs[0].Raw) != 0 {
		t.Errorf("recs[0].Raw = %x, want empty (no raw field in fixture line)", recs[0].Raw)
	}
}

func TestKey(t *testing.T) {
	recs, _, err := ParseAll(strings.NewReader(fixture()))
	if err != nil {
		t.Fatalf("ParseAll error: %v", err)
	}

	// recs[1] = ACK06 reg 000306 from src 3e01.
	reg, owner, ok := Key(recs[1])
	if !ok || reg != "000306" || owner != 0x3e01 {
		t.Errorf("Key(ACK06) = (%q, %04x, %v), want (000306, 3e01, true)", reg, owner, ok)
	}

	// recs[0] = READ reg 000306 targeting dst 3e01.
	reg, owner, ok = Key(recs[0])
	if !ok || reg != "000306" || owner != 0x3e01 {
		t.Errorf("Key(READ) = (%q, %04x, %v), want (000306, 3e01, true)", reg, owner, ok)
	}

	// recs[5] = bare ACK06 with 1-byte data -> not keyable.
	_, _, ok = Key(recs[5])
	if ok {
		t.Errorf("Key(bare ACK06) ok = true, want false")
	}
}

func TestGroup(t *testing.T) {
	recs, _, err := ParseAll(strings.NewReader(fixture()))
	if err != nil {
		t.Fatalf("ParseAll error: %v", err)
	}
	groups := Group(recs)

	if len(groups) != 5 {
		t.Fatalf("len(groups) = %d, want 5: %+v", len(groups), groups)
	}

	// Owner 3e01, reg 000306: two READ polls (no payload) + three ACK06
	// replies where two payloads are distinct (v1, v2) and one repeats v2.
	st, ok := groups[GroupKey{Owner: 0x3e01, Reg: "000306"}]
	if !ok {
		t.Fatalf("missing group for owner 3e01 reg 000306")
	}
	if st.Count != 5 {
		t.Errorf("Count = %d, want 5", st.Count)
	}
	if !st.First.Equal(mustTime(t, "2026-08-01T12:00:00Z")) {
		t.Errorf("First = %v", st.First)
	}
	if !st.Last.Equal(mustTime(t, "2026-08-01T12:02:00Z")) {
		t.Errorf("Last = %v", st.Last)
	}
	if st.PayloadLen != 4 {
		t.Errorf("PayloadLen = %d, want 4", st.PayloadLen)
	}
	if len(st.Distinct) != 2 {
		t.Fatalf("Distinct = %d entries, want 2: %x", len(st.Distinct), st.Distinct)
	}
	if hex.EncodeToString(st.Distinct[0]) != "11223344" || hex.EncodeToString(st.Distinct[1]) != "11ff3344" {
		t.Errorf("Distinct = %x, want [11223344 11ff3344] in first-seen order", st.Distinct)
	}
	if len(st.ChangedOffsets) != 1 || st.ChangedOffsets[0] != 1 {
		t.Errorf("ChangedOffsets = %v, want [1]", st.ChangedOffsets)
	}

	// Owner 6001, reg 000306: single ACK06 from a different owner than 3e01.
	st, ok = groups[GroupKey{Owner: 0x6001, Reg: "000306"}]
	if !ok || st.Count != 1 {
		t.Fatalf("missing/wrong group for owner 6001 reg 000306: %+v ok=%v", st, ok)
	}

	// Owner 6001, reg 000319: the WRITE frame.
	st, ok = groups[GroupKey{Owner: 0x6001, Reg: "000319"}]
	if !ok || st.Count != 1 || hex.EncodeToString(st.Distinct[0]) != "aabbcc" {
		t.Fatalf("wrong group for owner 6001 reg 000319: %+v ok=%v", st, ok)
	}

	// Non-keyable frames bucket under Reg "-", owner from the frame's usual
	// owner-selection rule for its op (Src for op 1e / ACK06).
	st, ok = groups[GroupKey{Owner: 0x4001, Reg: "-"}]
	if !ok || st.Count != 1 || hex.EncodeToString(st.Distinct[0]) != "0102" {
		t.Fatalf("wrong bucket for op 1e frame: %+v ok=%v", st, ok)
	}
	st, ok = groups[GroupKey{Owner: 0x5001, Reg: "-"}]
	if !ok || st.Count != 1 || hex.EncodeToString(st.Distinct[0]) != "99" {
		t.Fatalf("wrong bucket for bare ACK06: %+v ok=%v", st, ok)
	}
}

func TestChanges(t *testing.T) {
	recs, _, err := ParseAll(strings.NewReader(fixture()))
	if err != nil {
		t.Fatalf("ParseAll error: %v", err)
	}
	changes := Changes(recs, 0x3e01, "000306")
	if len(changes) != 2 {
		t.Fatalf("len(changes) = %d, want 2: %+v", len(changes), changes)
	}

	c0 := changes[0]
	if !c0.TS.Equal(mustTime(t, "2026-08-01T12:00:01Z")) {
		t.Errorf("changes[0].TS = %v", c0.TS)
	}
	if hex.EncodeToString(c0.Payload) != "11223344" {
		t.Errorf("changes[0].Payload = %x", c0.Payload)
	}
	if len(c0.ChangedFrom) != 0 {
		t.Errorf("changes[0].ChangedFrom = %v, want empty (first sample)", c0.ChangedFrom)
	}

	c1 := changes[1]
	if !c1.TS.Equal(mustTime(t, "2026-08-01T12:00:31Z")) {
		t.Errorf("changes[1].TS = %v", c1.TS)
	}
	if hex.EncodeToString(c1.Payload) != "11ff3344" {
		t.Errorf("changes[1].Payload = %x", c1.Payload)
	}
	if len(c1.ChangedFrom) != 1 || c1.ChangedFrom[0] != 1 {
		t.Errorf("changes[1].ChangedFrom = %v, want [1]", c1.ChangedFrom)
	}

	// The third ACK06 (12:02:00) repeats the same payload as the second, so
	// it must not produce a third Change entry.
}

func TestDiffAt(t *testing.T) {
	recs, _, err := ParseAll(strings.NewReader(fixture()))
	if err != nil {
		t.Fatalf("ParseAll error: %v", err)
	}
	at := mustTime(t, "2026-08-01T12:00:15Z")
	diffs := DiffAt(recs, at, 20*time.Second)
	if len(diffs) != 1 {
		t.Fatalf("len(diffs) = %d, want 1: %+v", len(diffs), diffs)
	}
	d := diffs[0]
	if d.Owner != 0x3e01 || d.Reg != "000306" {
		t.Errorf("diff owner/reg = %04x/%s, want 3e01/000306", d.Owner, d.Reg)
	}
	if hex.EncodeToString(d.Before) != "11223344" {
		t.Errorf("Before = %x, want 11223344", d.Before)
	}
	if hex.EncodeToString(d.After) != "11ff3344" {
		t.Errorf("After = %x, want 11ff3344", d.After)
	}
	if len(d.Changed) != 1 || d.Changed[0] != 1 {
		t.Errorf("Changed = %v, want [1]", d.Changed)
	}
}
