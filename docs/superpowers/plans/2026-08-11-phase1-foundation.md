# infinid Phase 1 (Foundation) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A running daemon on the Pi that reads the Carrier ABCD bus through the USB RS-485 dongle, validates frames, logs them, and records them to a JSONL capture file — the fixture source for Phase 2's table decoders.

**Architecture:** Three small packages: `bus` (CRC-16/ARC, frame encode/decode, stream splitter with resync, serial open), `capture` (ring buffer + JSONL recorder), `cmd/infinid` (flags + reconnect run loop). Codec semantics ported from acd/infinitive's `infinity` package (MIT, credited) with exported types and no logging deps in library code. Deployed as a HAOS local add-on using the pattern proven with `/addons/infinitive` on 2026-08-11.

**Tech Stack:** Go 1.22 (stdlib + go.bug.st/serial only), GitHub Actions CI, HAOS local add-on (multi-stage Docker build on the Pi).

**Reference:** spec at hunterhill-home `docs/superpowers/specs/2026-08-11-infinity-bus-daemon-design.md`. Ported source: `acd/infinitive/infinity/{frame.go,bus.go}` (fetched 2026-08-11, in scratchpad as `inf-frame.go` / `inf-bus.go`).

**Frame wire format** (from infinitive, verified against live captures tonight):
`dst(2,BE) src(2,BE) dataLen(1) 0x00 0x00 op(1) data(dataLen) crc(2,LE)` — CRC-16/ARC
(poly 0x8005 reflected → 0xA001, init 0, xorout 0, check("123456789") = 0xBB3D) over
all bytes before the CRC. Baud 38400 8N1.

**Live-capture payloads used as fixtures below** (from our bus, 2026-08-11):
- READ from wall control `0x2001` to air handler `0x3e01`, data `000306`
- ACK06 reply `0x3e01→0x2001`, data `000306000000000000c8089800`
- ACK06 damper reply `0x6001→0x2001`, data `0003190f0f0f0fffffffff`

---

### Task 1: CRC + Frame codec (`bus` package)

**Files:**
- Create: `bus/frame.go`
- Test: `bus/frame_test.go`

- [x] **Step 1: Write the failing tests**

```go
// bus/frame_test.go
package bus

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex fixture %q: %v", s, err)
	}
	return b
}

// CRC-16/ARC published check value.
func TestCRC16ARCCheckValue(t *testing.T) {
	if got := crc16ARC([]byte("123456789")); got != 0xBB3D {
		t.Fatalf("crc16ARC check = %#04x, want 0xBB3D", got)
	}
}

// Round-trip real payloads captured from the Hunterhill bus (2026-08-11).
func TestFrameRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		f    Frame
	}{
		{"read-000306", Frame{Dst: 0x3e01, Src: 0x2001, Op: OpRead, Data: mustHex(t, "000306")}},
		{"ack-000306", Frame{Dst: 0x2001, Src: 0x3e01, Op: OpAck06, Data: mustHex(t, "000306000000000000c8089800")}},
		{"ack-dampers", Frame{Dst: 0x2001, Src: 0x6001, Op: OpAck06, Data: mustHex(t, "0003190f0f0f0fffffffff")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wire := tc.f.Encode()
			if len(wire) != 10+len(tc.f.Data) {
				t.Fatalf("wire length = %d, want %d", len(wire), 10+len(tc.f.Data))
			}
			var got Frame
			if !got.Decode(wire) {
				t.Fatal("Decode returned false on Encode output")
			}
			if got.Dst != tc.f.Dst || got.Src != tc.f.Src || got.Op != tc.f.Op || !bytes.Equal(got.Data, tc.f.Data) {
				t.Fatalf("round trip mismatch: got %+v want %+v", got, tc.f)
			}
		})
	}
}

func TestDecodeRejectsCorruptCRC(t *testing.T) {
	wire := (&Frame{Dst: 0x2001, Src: 0x3e01, Op: OpAck06, Data: mustHex(t, "000306000000000000c8089800")}).Encode()
	wire[len(wire)-1] ^= 0xff
	var f Frame
	if f.Decode(wire) {
		t.Fatal("Decode accepted a corrupt CRC")
	}
}

func TestDecodeRejectsAllZeros(t *testing.T) {
	var f Frame
	if f.Decode(make([]byte, 12)) {
		t.Fatal("Decode accepted an all-zero buffer")
	}
}

func TestOpString(t *testing.T) {
	if s := opString(OpRead); s != "READ" {
		t.Fatalf("opString(OpRead) = %q, want READ", s)
	}
	if s := opString(0xaa); s != "UNKNOWN(aa)" {
		t.Fatalf("opString(0xaa) = %q", s)
	}
}
```

- [x] **Step 2: Run tests to verify they fail**

Run: `cd D:/Dev/Personal/infinid && go test ./bus/`
Expected: FAIL — `undefined: crc16ARC`, `undefined: Frame`, etc.

- [x] **Step 3: Write the implementation**

```go
// bus/frame.go
// Frame codec for the Carrier Infinity ABCD bus.
// Ported from acd/infinitive (MIT) infinity/frame.go with exported types,
// an inlined CRC-16/ARC, and no logging dependencies.
package bus

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// Device addresses observed on the bus.
const (
	DevWallControl = uint16(0x2001)
	DevAirHandler  = uint16(0x3e01)
	DevHeatPump    = uint16(0x5201)
	DevDCM1        = uint16(0x6001) // damper control module, zones 1-4
	DevDCM2        = uint16(0x6101) // second DCM, zones 5-8
	DevSAM         = uint16(0x9201) // the address infinid answers as (v2)
)

// Frame ops.
const (
	OpAck02 = uint8(0x02)
	OpAck06 = uint8(0x06)
	OpRead  = uint8(0x0b)
	OpWrite = uint8(0x0c)
	OpNack  = uint8(0x15)
	OpAlarm = uint8(0x1e)
)

var opNames = map[uint8]string{
	OpAck02: "ACK02",
	OpAck06: "ACK06",
	OpRead:  "READ",
	OpWrite: "WRITE",
	OpNack:  "NACK",
	OpAlarm: "ALARM",
}

func opString(op uint8) string {
	if s, ok := opNames[op]; ok {
		return s
	}
	return fmt.Sprintf("UNKNOWN(%02x)", op)
}

// crc16ARC computes CRC-16/ARC: poly 0x8005 reflected (0xA001), init 0,
// xorout 0. Matches the npat-efault/crc16 configuration infinitive uses.
func crc16ARC(data []byte) uint16 {
	var crc uint16
	for _, b := range data {
		crc ^= uint16(b)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0xA001
			} else {
				crc >>= 1
			}
		}
	}
	return crc
}

// Frame is one bus frame. Wire layout:
// dst(2,BE) src(2,BE) dataLen(1) 0x00 0x00 op(1) data crc(2,LE).
type Frame struct {
	Dst  uint16
	Src  uint16
	Op   uint8
	Data []byte
}

func (f Frame) String() string {
	return fmt.Sprintf("%04x -> %04x: %-8s %x", f.Src, f.Dst, opString(f.Op), f.Data)
}

// Encode renders the frame to wire bytes including CRC.
func (f Frame) Encode() []byte {
	if len(f.Data) > 255 {
		panic("frame data too large")
	}
	var b bytes.Buffer
	binary.Write(&b, binary.BigEndian, f.Dst)
	binary.Write(&b, binary.BigEndian, f.Src)
	b.WriteByte(byte(len(f.Data)))
	b.WriteByte(0)
	b.WriteByte(0)
	b.WriteByte(f.Op)
	b.Write(f.Data)
	crc := crc16ARC(b.Bytes())
	b.WriteByte(byte(crc))      // low byte first (little-endian on wire)
	b.WriteByte(byte(crc >> 8)) // high byte
	return b.Bytes()
}

// Decode parses one complete frame from buf (header + data + CRC exactly).
// Returns false on CRC mismatch, short buffer, or all-zero input.
func (f *Frame) Decode(buf []byte) bool {
	if len(buf) < 10 {
		return false
	}
	nonzero := false
	for _, c := range buf {
		if c != 0 {
			nonzero = true
			break
		}
	}
	if !nonzero {
		return false
	}
	l := len(buf) - 2
	want := crc16ARC(buf[:l])
	got := uint16(buf[l]) | uint16(buf[l+1])<<8
	if want != got {
		return false
	}
	f.Dst = binary.BigEndian.Uint16(buf[0:2])
	f.Src = binary.BigEndian.Uint16(buf[2:4])
	f.Op = buf[7]
	f.Data = append([]byte{}, buf[8:l]...)
	return true
}
```

- [x] **Step 4: Run tests to verify they pass**

Run: `go test ./bus/ -v`
Expected: PASS (5 tests).

- [x] **Step 5: Commit**

```bash
git add bus/frame.go bus/frame_test.go
git commit -m "feat(bus): frame codec + CRC-16/ARC, golden fixtures from live captures"
```

---

### Task 2: Stream splitter with resync (`bus.Decoder`)

**Files:**
- Create: `bus/decoder.go`
- Test: `bus/decoder_test.go`

- [ ] **Step 1: Write the failing tests**

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./bus/`
Expected: FAIL — `undefined: NewDecoder`.

- [ ] **Step 3: Write the implementation**

```go
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

func NewDecoder(r io.Reader) *Decoder {
	return &Decoder{r: r, scratch: make([]byte, 1024)}
}

// Resyncs reports how many bytes have been skipped resynchronizing.
func (d *Decoder) Resyncs() uint64 { return d.resyncs }

// Next blocks until a validated frame is available or the reader errors.
func (d *Decoder) Next() (Frame, error) {
	for {
		for len(d.pending) >= 10 {
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
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./bus/ -v`
Expected: PASS (7 tests total).

- [ ] **Step 5: Commit**

```bash
git add bus/decoder.go bus/decoder_test.go
git commit -m "feat(bus): stream splitter with byte-wise resync"
```

---

### Task 3: Serial open (`bus.OpenSerial`)

**Files:**
- Create: `bus/serial.go`
- Modify: `go.mod` (adds go.bug.st/serial)

- [ ] **Step 1: Write the implementation** (thin, hardware-backed — no unit test; exercised by the add-on smoke test in Task 6)

```go
// bus/serial.go
package bus

import (
	"io"

	"go.bug.st/serial"
)

// OpenSerial opens the RS-485 device at the Carrier bus rate (38400 8N1).
func OpenSerial(device string) (io.ReadWriteCloser, error) {
	return serial.Open(device, &serial.Mode{
		BaudRate: 38400,
		DataBits: 8,
		Parity:   serial.NoParity,
		StopBits: serial.OneStopBit,
	})
}
```

- [ ] **Step 2: Fetch the dependency and build**

Run: `go get go.bug.st/serial@latest && go build ./...`
Expected: builds clean; `go.mod`/`go.sum` updated.

- [ ] **Step 3: Commit**

```bash
git add bus/serial.go go.mod go.sum
git commit -m "feat(bus): serial open at Carrier bus parameters"
```

---

### Task 4: Capture recorder (`capture` package)

**Files:**
- Create: `capture/recorder.go`
- Test: `capture/recorder_test.go`

- [ ] **Step 1: Write the failing tests**

```go
// capture/recorder_test.go
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
	if m["src"] != "2001" || m["dst"] != "3e01" || m["op"] != "0b" || m["data"] != "000306" {
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./capture/`
Expected: FAIL — `undefined: Record`, `undefined: New`.

- [ ] **Step 3: Write the implementation**

```go
// capture/recorder.go
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./capture/ -v`
Expected: PASS (2 tests).

- [ ] **Step 5: Commit**

```bash
git add capture/recorder.go capture/recorder_test.go
git commit -m "feat(capture): frame ring buffer + JSONL recorder"
```

---

### Task 5: Daemon main (`cmd/infinid`)

**Files:**
- Create: `cmd/infinid/main.go`

- [ ] **Step 1: Write the implementation** (wiring only — logic already tested; smoke-tested on hardware in Task 6)

```go
// cmd/infinid/main.go
package main

import (
	"flag"
	"log"
	"os"
	"time"

	"github.com/utkjmitch/infinid/bus"
	"github.com/utkjmitch/infinid/capture"
)

func main() {
	serialDev := flag.String("serial", "", "RS-485 serial device (required)")
	capturePath := flag.String("capture", "", "append frames as JSONL to this file (optional)")
	ringSize := flag.Int("ring", 4096, "frames kept in the in-memory ring")
	flag.Parse()
	if *serialDev == "" {
		log.Fatal("-serial is required")
	}

	var w *os.File
	if *capturePath != "" {
		var err error
		w, err = os.OpenFile(*capturePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			log.Fatalf("open capture file: %v", err)
		}
		defer w.Close()
	}
	var rec *capture.Recorder
	if w != nil {
		rec = capture.New(*ringSize, w)
	} else {
		rec = capture.New(*ringSize, nil)
	}

	for {
		if err := run(*serialDev, rec); err != nil {
			log.Printf("bus error: %v — reopening in 5s", err)
			time.Sleep(5 * time.Second)
		}
	}
}

func run(device string, rec *capture.Recorder) error {
	port, err := bus.OpenSerial(device)
	if err != nil {
		return err
	}
	defer port.Close()
	log.Printf("listening on %s", device)

	d := bus.NewDecoder(port)
	for {
		f, err := d.Next()
		if err != nil {
			return err
		}
		log.Printf("frame: %s", f)
		rec.Add(capture.Record{
			TS: time.Now(), Src: f.Src, Dst: f.Dst, Op: f.Op, Data: f.Data,
		})
	}
}
```

- [ ] **Step 2: Build**

Run: `go build ./... && go vet ./...`
Expected: clean.

- [ ] **Step 3: Commit**

```bash
git add cmd/infinid/main.go
git commit -m "feat(cmd): daemon main — serial reconnect loop, frame log, JSONL capture"
```

---

### Task 6: HAOS local add-on + hardware smoke test

**Files (on the Pi via SSH `root@192.168.86.48`, NOT in this repo):**
- Create: `/addons/infinid/config.yaml`, `/addons/infinid/Dockerfile`, `/addons/infinid/run.sh`
- Reference copies in-repo: `deploy/haos-addon/{config.yaml,Dockerfile,run.sh}` (identical content, so the repo documents the add-on)

- [ ] **Step 1: Write the add-on files (repo copies first)**

`deploy/haos-addon/config.yaml`:
```yaml
name: infinid
version: "0.1.0"
slug: infinid
description: "Carrier Infinity ABCD bus daemon (read-only capture phase)"
arch:
  - aarch64
startup: services
boot: auto
uart: true
map:
  - share:rw
```

`deploy/haos-addon/Dockerfile`:
```dockerfile
FROM golang:1.22-alpine AS build
RUN apk add --no-cache git
RUN git clone https://github.com/utkjmitch/infinid.git /src \
 && cd /src && CGO_ENABLED=0 go build -o /infinid ./cmd/infinid

FROM alpine:3.20
COPY --from=build /infinid /infinid
COPY run.sh /run.sh
RUN chmod +x /run.sh
CMD ["/run.sh"]
```

`deploy/haos-addon/run.sh`:
```sh
#!/bin/sh
mkdir -p /share/infinid
exec /infinid \
  -serial /dev/serial/by-id/usb-FTDI_FT232R_USB_UART_BH002W7M-if00-port0 \
  -capture /share/infinid/capture.jsonl
```
(Note: the by-id path is the Hunterhill dongle; a future add-on option makes it configurable — YAGNI for Phase 1.)

- [ ] **Step 2: Commit the reference copies**

```bash
git add deploy/haos-addon/
git commit -m "feat(deploy): HAOS local add-on wrapper (capture phase)"
git push
```
(Push REQUIRED before the Pi build — the Dockerfile clones from GitHub.)

- [ ] **Step 3: Install on the Pi — stop the stopgap first (serial port is exclusive)**

```bash
ssh root@192.168.86.48 'ha addons stop local_infinitive && mkdir -p /addons/infinid'
scp deploy/haos-addon/config.yaml deploy/haos-addon/Dockerfile deploy/haos-addon/run.sh root@192.168.86.48:/addons/infinid/
ssh root@192.168.86.48 'ha store reload && sleep 3 && ha addons install local_infinid && ha addons start local_infinid'
```
Expected: install builds (~2-4 min on the Pi), start succeeds.

- [ ] **Step 4: Smoke-verify frames + capture file**

```bash
ssh root@192.168.86.48 'ha addons logs local_infinid | tail -5'
```
Expected: `frame: 2001 -> 3e01: READ ...` lines (live bus traffic).

```bash
ssh root@192.168.86.48 'wc -l /share/infinid/capture.jsonl && tail -2 /share/infinid/capture.jsonl'
```
Expected: growing line count; JSONL lines with ts/src/dst/op/data hex fields.

- [ ] **Step 5: Commit any fixes discovered during smoke** (if none, skip)

---

### Task 7: CI

**Files:**
- Create: `.github/workflows/ci.yml`

- [ ] **Step 1: Write the workflow**

```yaml
name: ci
on:
  push:
    branches: [main]
  pull_request:
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "1.22"
      - run: go vet ./...
      - run: go test ./...
```

- [ ] **Step 2: Commit, push, verify**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: vet + test on push/PR"
git push
gh run watch --exit-status || gh run list --limit 1
```
Expected: green run.

---

## Deferred to Phase 2 (explicitly NOT in this plan)

Table decoders (`protocol`), `state` assembly, MQTT discovery, REST debug surface,
active SAM reads, ha_carrier validation harness, add-on config options, nebulous
outreach issue. Phase 2's plan gets written once ~24h of capture.jsonl exists —
fixtures first, decoders second.
