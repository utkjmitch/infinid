package capture

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func rec(src, dst uint16, op uint8, data string) Record {
	return Record{
		TS:  time.Date(2026, 8, 11, 21, 0, 0, 0, time.UTC),
		Src: src, Dst: dst, Op: op,
		Data: []byte(data),
		Raw:  []byte(data),
	}
}

func TestRecorderWritesJSONL(t *testing.T) {
	var buf bytes.Buffer
	r := New(8, &buf)
	r.Add(rec(0x2001, 0x3e01, 0x0b, "\x00\x03\x06"))
	r.Add(rec(0x3e01, 0x2001, 0x06, "\x00\x03\x06\xc8"))

	lines := []string{}
	sc := bufio.NewScanner(&buf)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if len(lines) != 2 {
		t.Fatalf("wrote %d lines, want 2", len(lines))
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(lines[0]), &m); err != nil {
		t.Fatalf("line 0 not JSON: %v", err)
	}
	if m["src"] != "2001" || m["dst"] != "3e01" || m["op"] != "0b" || m["data"] != "000306" || m["raw"] != "000306" {
		t.Fatalf("unexpected fields: %v", m)
	}
	if !strings.HasPrefix(m["ts"], "2026-08-11T21:00:00") {
		t.Fatalf("unexpected ts: %v", m["ts"])
	}
}

func TestRingKeepsLastN(t *testing.T) {
	r := New(3, nil) // nil writer: ring only
	for i := 0; i < 5; i++ {
		r.Add(rec(uint16(i), 0x2001, 0x06, "x"))
	}
	snap := r.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("snapshot len = %d, want 3", len(snap))
	}
	if snap[0].Src != 2 || snap[2].Src != 4 {
		t.Fatalf("ring kept wrong records: first=%d last=%d", snap[0].Src, snap[2].Src)
	}
}
