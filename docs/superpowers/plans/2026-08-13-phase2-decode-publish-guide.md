# infinid Phase 2 — Decode, Publish, Guide — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the verified register map into live HA entities (MQTT discovery), an event journal with outage classification, a SAM read scheduler, and the community "decode your own system" guide + agent skill.

**Architecture:** `serial → bus → protocol (verified-only decoders) → state (assembly + staleness + journal) → {mqtt, rest}`, plus a `sam` package that transmits read-only register requests when enabled. Spec: `docs/superpowers/specs/2026-08-13-phase2-decode-publish-guide-design.md`. Decoder source of truth: the two verification sections of `docs/protocol-tables.md`. ADR-0001: registers without byte-verified layouts are archived, never speculatively decoded.

**Tech Stack:** Go 1.25 (toolchain NOT on PATH — every `go` command needs `export PATH="$PATH:/c/Users/jimmy/.go-toolchain/go/bin"` first), stdlib + go.bug.st/serial (existing) + github.com/eclipse/paho.mqtt.golang (new, Task 9 only). Tests: `go test ./...` from repo root `D:\Dev\Personal\infinid` (Git Bash: `/d/Dev/Personal/infinid`).

**Repo facts you need:** module `github.com/utkjmitch/infinid`. Existing packages: `bus` (Frame{Dst,Src,Op,Data,Raw uint16/uint16/uint8/[]byte/[]byte}, ops `bus.OpAck06`=0x06 `bus.OpRead`=0x0b `bus.OpWrite`=0x0c, addresses `bus.DevWallControl`=0x2001 `bus.DevSAM`=0x9201, `Frame.Encode()`, `OpenSerial`), `capture` (Recorder ring + JSONL), `inspect` (ParseAll reads capture JSONL → `[]Rec{TS,Src,Dst,Op,Data,Raw}`). `protocol/testdata/frames.jsonl` already contains 60 real captured frames curated per register (committed with this plan) — golden tests parse it with `inspect.ParseAll`. This is a PUBLIC repo: no real hostnames, IPs, or credentials anywhere, including tests and docs.

**Register keying rule (matches `inspect.Key`):** for ops ACK06/WRITE with ≥4 data bytes, register = `Data[0:3]`, payload = `Data[3:]`; owner = Src for ACK06 (device answering), Dst for WRITE (device addressed). READs are bare polls — never decoded. Zone-sensor addresses `0x2101..0x2801` map to zone index `(addr>>8)-0x20` (0x2201 → zone 2).

---

### Task 1: `protocol` core — Reading type, dispatch, TLV temperatures

**Files:**
- Create: `protocol/protocol.go`, `protocol/protocol_test.go`

- [ ] **Step 1: Write the failing test**

`protocol/protocol_test.go`:

```go
package protocol

import (
	"os"
	"testing"
	"time"

	"github.com/utkjmitch/infinid/bus"
	"github.com/utkjmitch/infinid/inspect"
)

// fixtures loads the curated real-capture frames from testdata.
func fixtures(t *testing.T) []struct {
	F  bus.Frame
	TS time.Time
} {
	t.Helper()
	f, err := os.Open("testdata/frames.jsonl")
	if err != nil {
		t.Fatalf("open fixtures: %v", err)
	}
	defer f.Close()
	recs, skipped, err := inspect.ParseAll(f)
	if err != nil || skipped != 0 {
		t.Fatalf("parse fixtures: err=%v skipped=%d", err, skipped)
	}
	out := make([]struct {
		F  bus.Frame
		TS time.Time
	}, len(recs))
	for i, r := range recs {
		out[i].F = bus.Frame{Src: r.Src, Dst: r.Dst, Op: r.Op, Data: r.Data}
		out[i].TS = r.TS
	}
	return out
}

// decodeAll runs every fixture through Decode and returns all readings.
func decodeAll(t *testing.T) []Reading {
	t.Helper()
	var rs []Reading
	for _, fx := range fixtures(t) {
		got, _ := Decode(fx.F, fx.TS)
		rs = append(rs, got...)
	}
	return rs
}

// find returns readings matching owner+field, in fixture order.
func find(rs []Reading, owner uint16, field string) []Reading {
	var out []Reading
	for _, r := range rs {
		if r.Owner == owner && r.Field == field {
			out = append(out, r)
		}
	}
	return out
}

func TestTLVTemperaturesODU(t *testing.T) {
	rs := decodeAll(t)
	// Fixture 000302@5201 first sample:
	// 0x11 OAT 77.56, 0x12 coil 82.75, 0x30 suction 57.56,
	// 0x4a superheat 16.00, 0x45 discharge 106.19. Unverified id 0x4b must NOT appear.
	checks := map[string]float64{
		"outdoor_temp":      77.5625,
		"outdoor_coil_temp": 82.75,
		"suction_temp":      57.5625,
		"superheat":         16.0,
		"discharge_temp":    106.1875,
	}
	for field, want := range checks {
		got := find(rs, 0x5201, field)
		if len(got) == 0 {
			t.Fatalf("no %s readings", field)
		}
		if got[0].Value != want {
			t.Errorf("%s = %v, want %v", field, got[0].Value, want)
		}
	}
	if len(find(rs, 0x5201, "unknown_0x4b")) != 0 {
		t.Error("unverified TLV id 0x4b must not be decoded (ADR-0001)")
	}
}

func TestTLVSupplyAirIDU(t *testing.T) {
	rs := decodeAll(t)
	got := find(rs, 0x3e01, "supply_air_temp")
	if len(got) != 3 {
		t.Fatalf("supply_air_temp count = %d, want 3", len(got))
	}
	if got[0].Value != 71.3125 { // 0x0475/16
		t.Errorf("supply_air_temp = %v, want 71.3125", got[0].Value)
	}
	// Absent TLV entries (tag 0x00) must not decode.
	if len(find(rs, 0x3e01, "outdoor_temp")) != 0 {
		t.Error("tag-0x00 TLV entries must be skipped")
	}
}

func TestUndecodedFrameNotOK(t *testing.T) {
	// Register 000715 has no verified layout — must archive, not decode.
	f := bus.Frame{Src: 0x3e01, Dst: 0x2001, Op: bus.OpAck06,
		Data: []byte{0x00, 0x07, 0x15, 0x01, 0x02}}
	rs, ok := Decode(f, time.Now())
	if ok || rs != nil {
		t.Errorf("unverified register decoded: ok=%v rs=%v", ok, rs)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /d/Dev/Personal/infinid && export PATH="$PATH:/c/Users/jimmy/.go-toolchain/go/bin" && go test ./protocol/
```
Expected: FAIL — `Reading`/`Decode` undefined.

- [ ] **Step 3: Write minimal implementation**

`protocol/protocol.go`:

```go
// Package protocol turns verified bus frames into typed readings. Only
// registers whose byte layouts are verified in docs/protocol-tables.md get
// decoders; everything else returns ok=false so callers archive it
// (ADR-0001: the fix is verification, not transcription).
package protocol

import (
	"time"

	"github.com/utkjmitch/infinid/bus"
)

// Reading is one decoded field observation.
type Reading struct {
	Owner uint16    // bus address of the register's owner
	Reg   uint32    // register, e.g. 0x000302
	Zone  int       // 1-based zone index; 0 = not zone-scoped
	Field string    // stable snake_case identifier
	Value float64
	Text  string    // set for enum fields; Value carries the raw number
	TS    time.Time
}

func u16(p []byte, i int) uint16 { return uint16(p[i])<<8 | uint16(p[i+1]) }

// Decode returns readings for f when (owner, register) has a verified
// decoder. ok=false means "not decodable" — the caller counts and archives.
func Decode(f bus.Frame, ts time.Time) ([]Reading, bool) {
	var owner uint16
	switch f.Op {
	case bus.OpAck06:
		owner = f.Src
	case bus.OpWrite:
		owner = f.Dst
	default:
		return nil, false
	}
	if len(f.Data) < 4 {
		return nil, false
	}
	reg := uint32(f.Data[0])<<16 | uint32(f.Data[1])<<8 | uint32(f.Data[2])
	p := f.Data[3:]

	var rs []Reading
	switch reg {
	case 0x000302:
		rs = tlvTemps(owner, p)
	default:
		return nil, false
	}
	if len(rs) == 0 {
		return nil, false
	}
	for i := range rs {
		rs[i].Owner = owner
		rs[i].Reg = reg
		rs[i].TS = ts
	}
	return rs, true
}

// tlvNames maps verified TLV temperature ids (000302, any owner) to fields.
// Verified: 0x11/0x12/0x30/0x4a/0x45 on the ODU, 0x14 (supply air / LAT) on
// the IDU. Id 0x4b is observed but unidentified — deliberately absent.
var tlvNames = map[byte]string{
	0x11: "outdoor_temp",
	0x12: "outdoor_coil_temp",
	0x14: "supply_air_temp",
	0x30: "suction_temp",
	0x45: "discharge_temp",
	0x4a: "superheat",
}

// tlvTemps decodes the 4-byte TLV rows: tag(01=present) id value(u16 BE /16 °F).
func tlvTemps(_ uint16, p []byte) []Reading {
	var rs []Reading
	for i := 0; i+4 <= len(p); i += 4 {
		if p[i] != 0x01 {
			continue
		}
		field, known := tlvNames[p[i+1]]
		if !known {
			continue
		}
		rs = append(rs, Reading{Field: field, Value: float64(u16(p, i+2)) / 16.0})
	}
	return rs
}
```

- [ ] **Step 4: Run test to verify it passes**

Same command. Expected: PASS (all three tests).

- [ ] **Step 5: Commit**

```bash
cd /d/Dev/Personal/infinid && git add protocol/ && git commit -m "feat(protocol): Reading type, verified-only dispatch, TLV temperature decoder"
```

---

### Task 2: ODU decoders — 000303/000304/000604/000605/00060E/000625 + KV counters 000310/000311

**Files:**
- Modify: `protocol/protocol.go` (extend the `switch reg`)
- Create: `protocol/odu.go`, `protocol/odu_test.go`

- [ ] **Step 1: Write the failing test**

`protocol/odu_test.go`:

```go
package protocol

import "testing"

func TestSuctionPressure(t *testing.T) {
	rs := decodeAll(t)
	got := find(rs, 0x5201, "suction_pressure")
	if len(got) != 2 || got[0].Value != 121.0 || got[1].Value != 122.0 {
		t.Fatalf("suction_pressure = %+v, want [121 122]", got)
	}
}

func TestLineVoltage(t *testing.T) {
	rs := decodeAll(t)
	got := find(rs, 0x5201, "line_voltage")
	if len(got) != 2 || got[0].Value != 245 || got[1].Value != 246 {
		t.Fatalf("line_voltage = %+v, want [245 246]", got)
	}
}

func TestCompressorRPM(t *testing.T) {
	rs := decodeAll(t)
	target := find(rs, 0x5201, "compressor_rpm_target")
	actual := find(rs, 0x5201, "compressor_rpm")
	if len(target) != 2 || target[0].Value != 1200 || target[1].Value != 0 {
		t.Fatalf("rpm_target = %+v, want [1200 0]", target)
	}
	if len(actual) != 2 || actual[0].Value != 1200 || actual[1].Value != 0 {
		t.Fatalf("rpm actual = %+v, want [1200 0]", actual)
	}
}

func TestCompressorStageCommand(t *testing.T) {
	rs := decodeAll(t)
	// 000605 WRITE fixtures: stage 1.0, 0.0, 0.0, 2.0 (float32 BE at [0..3]).
	got := find(rs, 0x5201, "compressor_stage_cmd")
	if len(got) != 4 {
		t.Fatalf("stage_cmd count = %d, want 4", len(got))
	}
	want := []float64{1, 0, 0, 2}
	for i, w := range want {
		if got[i].Value != w {
			t.Errorf("stage_cmd[%d] = %v, want %v", i, got[i].Value, w)
		}
	}
	// [4] mode flag: 01=cool on first fixture, 00=heat on third.
	mode := find(rs, 0x5201, "cool_mode_flag")
	if len(mode) != 4 || mode[0].Value != 1 || mode[2].Value != 0 {
		t.Fatalf("cool_mode_flag = %+v", mode)
	}
}

func TestCompressorActualStage(t *testing.T) {
	rs := decodeAll(t)
	got := find(rs, 0x5201, "compressor_stage")
	if len(got) != 3 || got[0].Value != 1 {
		t.Fatalf("compressor_stage = %+v, want three 1s", got)
	}
}

func TestCounters(t *testing.T) {
	rs := decodeAll(t)
	// 000310@3e01: 0x23=6598 heat1 cycles, 0x24=13 heat2, 0x2b=24 power, 0x2d=20470 blower.
	// Unverified keys (0x27, 0x29, 0x48...) must not decode.
	checks := []struct {
		owner uint16
		field string
		first float64
	}{
		{0x3e01, "heat_stage1_cycles", 6598},
		{0x3e01, "heat_stage2_cycles", 13},
		{0x3e01, "idu_power_cycles", 24},
		{0x3e01, "blower_cycles", 20470},
		{0x3e01, "heat_stage1_hours", 1035},
		{0x3e01, "heat_stage2_hours", 9},
		{0x3e01, "idu_power_hours", 13368},
		{0x3e01, "blower_hours", 4990},
		{0x5201, "cool_cycles", 12530},
		{0x5201, "odu_power_cycles", 6},
		{0x5201, "cool_hours", 4169},
		{0x5201, "odu_power_hours", 13805},
	}
	for _, c := range checks {
		got := find(rs, c.owner, c.field)
		if len(got) == 0 {
			t.Errorf("no readings for %04x %s", c.owner, c.field)
			continue
		}
		if got[0].Value != c.first {
			t.Errorf("%s = %v, want %v", c.field, got[0].Value, c.first)
		}
	}
	if len(find(rs, 0x3e01, "counter_0x27")) != 0 {
		t.Error("unverified counter key decoded")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

`go test ./protocol/` → FAIL (fields missing).

- [ ] **Step 3: Write implementation**

`protocol/odu.go`:

```go
package protocol

import (
	"encoding/binary"
	"math"
)

func f32(p []byte, i int) float64 {
	return float64(math.Float32frombits(binary.BigEndian.Uint32(p[i : i+4])))
}

// oduShortStatus — 000303: [2..3] u16/16 suction pressure PSIG.
func oduShortStatus(p []byte) []Reading {
	if len(p) < 4 {
		return nil
	}
	return []Reading{{Field: "suction_pressure", Value: float64(u16(p, 2)) / 16.0}}
}

// oduStatus — 000304: [7] line voltage.
func oduStatus(p []byte) []Reading {
	if len(p) < 8 {
		return nil
	}
	return []Reading{{Field: "line_voltage", Value: float64(p[7])}}
}

// compressorRPM — 000604: [0..1] target RPM, [2..3] actual RPM.
// [4..23] are static per-stage tables — constants, not published.
func compressorRPM(p []byte) []Reading {
	if len(p) < 4 {
		return nil
	}
	return []Reading{
		{Field: "compressor_rpm_target", Value: float64(u16(p, 0))},
		{Field: "compressor_rpm", Value: float64(u16(p, 2))},
	}
}

// compressorStageCmd — 000605 WRITE: [0..3] float32 commanded stage,
// [4] mode flag 01=cool 00=heat.
func compressorStageCmd(p []byte) []Reading {
	if len(p) < 5 {
		return nil
	}
	return []Reading{
		{Field: "compressor_stage_cmd", Value: f32(p, 0)},
		{Field: "cool_mode_flag", Value: float64(p[4])},
	}
}

// compressorStage — 00060E: [0] actual stage index (u8, 0=off 1-5).
func compressorStage(p []byte) []Reading {
	if len(p) < 1 {
		return nil
	}
	return []Reading{{Field: "compressor_stage", Value: float64(p[0])}}
}

// oduAnalog — 000625: [0..1] u16 power-like analog (units unverified —
// decoded for the comparator/REST view, not published to MQTT).
func oduAnalog(p []byte) []Reading {
	if len(p) < 2 {
		return nil
	}
	return []Reading{{Field: "odu_power_analog", Value: float64(u16(p, 0))}}
}

// counterName maps verified KV counter keys to fields, per owner and
// register (000310 = cycle counts, 000311 = lifetime hours). Byte-verified
// against the panel's counter pages 2026-08-13. Unverified keys → "".
func counterName(reg uint32, owner uint16, key byte) string {
	switch {
	case reg == 0x000310 && owner == 0x3e01:
		switch key {
		case 0x23:
			return "heat_stage1_cycles"
		case 0x24:
			return "heat_stage2_cycles"
		case 0x2b:
			return "idu_power_cycles"
		case 0x2d:
			return "blower_cycles"
		}
	case reg == 0x000311 && owner == 0x3e01:
		switch key {
		case 0x25:
			return "heat_stage1_hours"
		case 0x26:
			return "heat_stage2_hours"
		case 0x2c:
			return "idu_power_hours"
		case 0x2e:
			return "blower_hours"
		}
	case reg == 0x000310 && owner == 0x5201:
		switch key {
		case 0x28:
			return "cool_cycles"
		case 0x2b:
			return "odu_power_cycles"
		}
	case reg == 0x000311 && owner == 0x5201:
		switch key {
		case 0x2a:
			return "cool_hours"
		case 0x2c:
			return "odu_power_hours"
		}
	}
	return ""
}

// counters — 000310/000311: rows of key(u8) value(u24 BE) pad(0x00)... —
// observed row stride is 4 bytes: key + 3-byte value.
func counters(reg uint32, owner uint16, p []byte) []Reading {
	var rs []Reading
	for i := 0; i+4 <= len(p); i += 4 {
		field := counterName(reg, owner, p[i])
		if field == "" {
			continue
		}
		v := uint32(p[i+1])<<16 | uint32(p[i+2])<<8 | uint32(p[i+3])
		rs = append(rs, Reading{Field: field, Value: float64(v)})
	}
	return rs
}
```

In `protocol/protocol.go`, extend the dispatch switch (keep the existing 0x000302 case):

```go
	case 0x000303:
		if owner == 0x5201 {
			rs = oduShortStatus(p)
		}
	case 0x000304:
		if owner == 0x5201 {
			rs = oduStatus(p)
		}
	case 0x000604:
		rs = compressorRPM(p)
	case 0x000605:
		if f.Op == bus.OpWrite {
			rs = compressorStageCmd(p)
		}
	case 0x00060e:
		rs = compressorStage(p)
	case 0x000625:
		rs = oduAnalog(p)
	case 0x000310, 0x000311:
		rs = counters(reg, owner, p)
```

- [ ] **Step 4: Run test to verify it passes**

`go test ./protocol/` → PASS.

- [ ] **Step 5: Commit**

```bash
git add protocol/ && git commit -m "feat(protocol): ODU decoders — pressures, compressor, KV runtime counters"
```

---

### Task 3: IDU, zone, and clock decoders — 000305/000306/000413/000308/000319/00041F/00041E/000420/000202/000203

**Files:**
- Modify: `protocol/protocol.go` (extend switch)
- Create: `protocol/idu.go`, `protocol/zone.go`, `protocol/idu_test.go`, `protocol/zone_test.go`

- [ ] **Step 1: Write the failing tests**

`protocol/idu_test.go`:

```go
package protocol

import (
	"math"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 0.01 }

func TestAirflowCommand(t *testing.T) {
	rs := decodeAll(t)
	cfm := find(rs, 0x3e01, "commanded_cfm")
	if len(cfm) != 4 || cfm[0].Value != 514 || cfm[3].Value != 512 {
		t.Fatalf("commanded_cfm = %+v", cfm)
	}
	heat := find(rs, 0x3e01, "heat_stage")
	if len(heat) != 4 || heat[0].Value != 0 {
		t.Fatalf("heat_stage = %+v", heat)
	}
	cool := find(rs, 0x3e01, "cool_demand")
	// First three fixtures 0x02 (cooling), last 0x00.
	if len(cool) != 4 || cool[0].Value != 1 || cool[3].Value != 0 {
		t.Fatalf("cool_demand = %+v", cool)
	}
}

func TestBlowerTelemetry(t *testing.T) {
	rs := decodeAll(t)
	cfm := find(rs, 0x3e01, "supply_cfm")
	rpm := find(rs, 0x3e01, "blower_rpm")
	sp := find(rs, 0x3e01, "static_pressure")
	w := find(rs, 0x3e01, "blower_watts")
	if len(cfm) != 3 || cfm[0].Value != 495 {
		t.Fatalf("supply_cfm = %+v", cfm)
	}
	if len(rpm) != 3 || rpm[0].Value != 495 || rpm[2].Value != 505 {
		t.Fatalf("blower_rpm = %+v", rpm)
	}
	if !near(sp[0].Value, 0.1871) {
		t.Errorf("static_pressure = %v, want ~0.1871", sp[0].Value)
	}
	if !near(w[0].Value, 55.22) {
		t.Errorf("blower_watts = %v, want ~55.22", w[0].Value)
	}
}

func TestBusClock(t *testing.T) {
	rs := decodeAll(t)
	h := find(rs, 0xf1f1, "bus_time_minutes")
	if len(h) != 2 || h[0].Value != 6*60+10 {
		t.Fatalf("bus_time_minutes = %+v, want first 370", h)
	}
	y := find(rs, 0xf1f1, "bus_year")
	if len(y) != 2 || y[0].Value != 2026 {
		t.Fatalf("bus_year = %+v", y)
	}
}
```

`protocol/zone_test.go`:

```go
package protocol

import "testing"

func TestDamperFeedback(t *testing.T) {
	rs := decodeAll(t)
	var z1, z2, z5 []Reading
	for _, r := range rs {
		if r.Owner != 0x6001 || r.Field != "damper_position" || r.Reg != 0x000319 {
			continue
		}
		switch r.Zone {
		case 1:
			z1 = append(z1, r)
		case 2:
			z2 = append(z2, r)
		case 5:
			z5 = append(z5, r)
		}
	}
	// Fixtures: [0]=0x00/0x0f/0x0f, [1]=0x0f/0x0f/0x07, [4..7]=0xff (absent).
	if len(z1) != 3 || z1[1].Value != 15 {
		t.Fatalf("zone1 damper = %+v", z1)
	}
	if len(z2) != 3 || z2[2].Value != 7 {
		t.Fatalf("zone2 damper = %+v", z2)
	}
	if len(z5) != 0 {
		t.Error("0xFF (absent) damper slots must not produce readings")
	}
}

func TestZoneSetpointPush(t *testing.T) {
	rs := decodeAll(t)
	// 00041F WRITE 2001→2201 (zone 2) first fixture: heat 68, cool 73, fan auto, no hold.
	var z2 []Reading
	for _, r := range rs {
		if r.Zone == 2 && r.Reg == 0x00041f {
			z2 = append(z2, r)
		}
	}
	byField := map[string][]Reading{}
	for _, r := range z2 {
		byField[r.Field] = append(byField[r.Field], r)
	}
	if v := byField["heat_setpoint"]; len(v) == 0 || v[0].Value != 68 {
		t.Fatalf("heat_setpoint = %+v", v)
	}
	if v := byField["cool_setpoint"]; len(v) == 0 || v[0].Value != 73 {
		t.Fatalf("cool_setpoint = %+v", v)
	}
	if v := byField["fan_mode"]; len(v) == 0 || v[0].Text != "auto" {
		t.Fatalf("fan_mode = %+v", v)
	}
	// Second zone-2 fixture is the verified timed hold: 19410 ticks = 647 min.
	if v := byField["hold_remaining_min"]; len(v) == 0 || v[0].Value != 647 {
		t.Fatalf("hold_remaining_min = %+v, want 647", v)
	}
	if v := byField["hold"]; len(v) < 2 || v[0].Value != 0 || v[1].Value != 1 {
		t.Fatalf("hold = %+v, want [0 1 ...]", v)
	}
}

func TestZoneIndefiniteHold(t *testing.T) {
	rs := decodeAll(t)
	// 00041F 2001→2301 (zone 3) fixtures all carry flags bit7 = indefinite hold.
	for _, r := range rs {
		if r.Zone == 3 && r.Reg == 0x00041f && r.Field == "hold" && r.Value != 1 {
			t.Fatalf("zone3 hold = %v, want 1 (indefinite)", r.Value)
		}
	}
}

func TestZoneSensorStatus(t *testing.T) {
	rs := decodeAll(t)
	var temp, rh []Reading
	for _, r := range rs {
		if r.Zone == 2 && r.Reg == 0x00041e {
			if r.Field == "temp" {
				temp = append(temp, r)
			}
			if r.Field == "humidity" {
				rh = append(rh, r)
			}
		}
	}
	if len(temp) != 2 || temp[0].Value != 72.375 {
		t.Fatalf("zone2 temp = %+v, want first 72.375", temp)
	}
	if len(rh) != 2 || rh[0].Value != 55 {
		t.Fatalf("zone2 humidity = %+v, want 55", rh)
	}
}
```

- [ ] **Step 2: Run to verify FAIL** — `go test ./protocol/`

- [ ] **Step 3: Write implementation**

`protocol/idu.go`:

```go
package protocol

// airflowCmd — 000305 WRITE 2001→IDU: [0] commanded heat stage (gas furnace
// 01 low / 02 high), [2] cool-demand flag (0x02 cooling), [4..5] u16 BE
// commanded CFM (0 cedes airflow control to the IDU).
func airflowCmd(p []byte) []Reading {
	if len(p) < 6 {
		return nil
	}
	cool := 0.0
	if p[2] == 0x02 {
		cool = 1
	}
	return []Reading{
		{Field: "heat_stage", Value: float64(p[0])},
		{Field: "cool_demand", Value: cool},
		{Field: "commanded_cfm", Value: float64(u16(p, 4))},
	}
}

// blowerStatus — 000306: [1..2] blower RPM, [3..4] CFM echo of the 000305
// command. Redundant with 000413 (measured) — decoded for the comparator
// and REST view, not published.
func blowerStatus(p []byte) []Reading {
	if len(p) < 5 {
		return nil
	}
	return []Reading{
		{Field: "blower_rpm_echo", Value: float64(u16(p, 1))},
		{Field: "commanded_cfm_echo", Value: float64(u16(p, 3))},
	}
}

// blowerTelemetry — 000413: [0..1] measured CFM, [2..3] RPM,
// [4..7] float32 static pressure in-wc, [8..11] float32 blower watts.
func blowerTelemetry(p []byte) []Reading {
	if len(p) < 12 {
		return nil
	}
	return []Reading{
		{Field: "supply_cfm", Value: float64(u16(p, 0))},
		{Field: "blower_rpm", Value: float64(u16(p, 2))},
		{Field: "static_pressure", Value: f32(p, 4)},
		{Field: "blower_watts", Value: f32(p, 8)},
	}
}

// busTime — 000202 broadcast: hour, minute, weekday.
func busTime(p []byte) []Reading {
	if len(p) < 3 {
		return nil
	}
	return []Reading{
		{Field: "bus_time_minutes", Value: float64(int(p[0])*60 + int(p[1]))},
		{Field: "bus_weekday", Value: float64(p[2])},
	}
}

// busDate — 000203 broadcast: day, month, year-2000.
func busDate(p []byte) []Reading {
	if len(p) < 3 {
		return nil
	}
	return []Reading{
		{Field: "bus_day", Value: float64(p[0])},
		{Field: "bus_month", Value: float64(p[1])},
		{Field: "bus_year", Value: float64(2000 + int(p[2]))},
	}
}
```

`protocol/zone.go`:

```go
package protocol

// fanModes — 00041F[5] / 3B03 fan enum. Acts as a floor, not a direct
// blower command.
var fanModes = []string{"auto", "low", "med", "high"}

func fanText(v byte) string {
	if int(v) < len(fanModes) {
		return fanModes[v]
	}
	return "unknown"
}

// zoneIndex maps a zone-sensor bus address (0x2101..0x2801) to its 1-based
// zone index; 0 when addr is not a zone sensor.
func zoneIndex(addr uint16) int {
	hi := int(addr >> 8)
	if hi >= 0x21 && hi <= 0x28 && addr&0xff == 0x01 {
		return hi - 0x20
	}
	return 0
}

// dampers — 000308 (command) / 000319 (feedback): one byte per zone index,
// 0x00-0x0F open fraction; 0xFF marks an absent slot (0x00 does NOT prove a
// zone absent — see the zone-presence rule in state).
func dampers(reg uint32, p []byte) []Reading {
	field := "damper_cmd"
	if reg == 0x000319 {
		field = "damper_position"
	}
	var rs []Reading
	for i := 0; i < len(p) && i < 8; i++ {
		if p[i] == 0xff {
			continue
		}
		rs = append(rs, Reading{Zone: i + 1, Field: field, Value: float64(p[i])})
	}
	return rs
}

// zoneConfigPush — 00041F WRITE 2001→sensor: [0] bit7 indefinite hold,
// [1]=0x18 timed-hold marker with [3..4] u16 BE remaining 2-second ticks,
// [5] fan enum, [6] heat setpoint °F, [7] cool setpoint °F.
func zoneConfigPush(zone int, p []byte) []Reading {
	if zone == 0 || len(p) < 8 {
		return nil
	}
	hold := 0.0
	if p[0]&0x80 != 0 || p[1] == 0x18 {
		hold = 1
	}
	rs := []Reading{
		{Zone: zone, Field: "hold", Value: hold},
		{Zone: zone, Field: "fan_mode", Value: float64(p[5]), Text: fanText(p[5])},
		{Zone: zone, Field: "heat_setpoint", Value: float64(p[6])},
		{Zone: zone, Field: "cool_setpoint", Value: float64(p[7])},
	}
	if p[1] == 0x18 {
		rs = append(rs, Reading{Zone: zone, Field: "hold_remaining_min",
			Value: float64(u16(p, 3)) / 30.0}) // 2-second ticks → minutes
	}
	return rs
}

// zoneSensorStatus — 00041E from sensor: [9..10] u16 BE temp ×16, [12] RH %.
func zoneSensorStatus(zone int, p []byte) []Reading {
	if zone == 0 || len(p) < 13 {
		return nil
	}
	return []Reading{
		{Zone: zone, Field: "temp", Value: float64(u16(p, 9)) / 16.0},
		{Zone: zone, Field: "humidity", Value: float64(p[12])},
	}
}
```

Extend the dispatch switch in `protocol/protocol.go`:

```go
	case 0x000305:
		if f.Op == bus.OpWrite {
			rs = airflowCmd(p)
		}
	case 0x000306:
		rs = blowerStatus(p)
	case 0x000413:
		rs = blowerTelemetry(p)
	case 0x000308, 0x000319:
		rs = dampers(reg, p)
	case 0x00041f:
		if f.Op == bus.OpWrite {
			rs = zoneConfigPush(zoneIndex(f.Dst), p)
		}
	case 0x00041e:
		rs = zoneSensorStatus(zoneIndex(owner), p)
	case 0x000202:
		rs = busTime(p)
	case 0x000203:
		rs = busDate(p)
```

(000420 is deliberately NOT decoded: its OAT/RH duplicate verified fields and its layout confidence is LOW-MED — archive path per ADR-0001.)

- [ ] **Step 4: Run to verify PASS** — `go test ./protocol/`

- [ ] **Step 5: Commit**

```bash
git add protocol/ && git commit -m "feat(protocol): IDU airflow/blower, dampers, zone setpoint/hold/sensor, bus clock"
```

---

### Task 4: SAM table decoders — 3B02/3B03/3B05 + 4202 fault history

These registers never appear passively (the wall control serves them only to an active SAM reader), so there are no captured fixtures yet. Layouts come from prior art (three sources agree; see `docs/protocol-tables.md` §3B02/3B03/3B05/4202); tests use synthetic payloads built byte-for-byte from those tables. **Live validation against the real wall control is the deploy-time gate (Task 16) — until it passes, these decoders are compiled but the SAM scheduler defaults off.**

**Files:**
- Modify: `protocol/protocol.go` (extend switch)
- Create: `protocol/sam.go`, `protocol/sam_test.go`

- [ ] **Step 1: Write the failing test**

`protocol/sam_test.go`:

```go
package protocol

import (
	"testing"
	"time"

	"github.com/utkjmitch/infinid/bus"
)

// samFrame wraps a payload as the wall control's ACK06 reply to a SAM read.
func samFrame(reg []byte, payload []byte) bus.Frame {
	return bus.Frame{Src: bus.DevWallControl, Dst: bus.DevSAM, Op: bus.OpAck06,
		Data: append(append([]byte{}, reg...), payload...)}
}

func TestSystemState3B02(t *testing.T) {
	// active_zones=0b0000_0111 (zones 1-3), °F, temps 74/72/70, RH 55,
	// OAT 88, byte22: high nibble stage 1, low nibble mode 1 (cool).
	p := make([]byte, 29)
	p[0] = 0x07
	p[3], p[4], p[5] = 74, 72, 70
	p[11], p[12], p[13] = 55, 55, 55
	p[20] = 88
	p[22] = 0x11
	rs, ok := Decode(samFrame([]byte{0x00, 0x3b, 0x02}, p), time.Now())
	if !ok {
		t.Fatal("3B02 not decoded")
	}
	byKey := map[string]Reading{}
	for _, r := range rs {
		if r.Zone > 0 {
			byKey[r.Field+string(rune('0'+r.Zone))] = r
		} else {
			byKey[r.Field] = r
		}
	}
	if byKey["temp1"].Value != 74 || byKey["temp3"].Value != 70 {
		t.Errorf("zone temps wrong: %+v", byKey)
	}
	if _, exists := byKey["temp4"]; exists {
		t.Error("zone 4 not in active_zones bitmask — must not decode")
	}
	if byKey["active_zones"].Value != 7 {
		t.Errorf("active_zones = %v, want 7", byKey["active_zones"].Value)
	}
	if byKey["outdoor_temp_sam"].Value != 88 {
		t.Errorf("OAT = %v, want 88", byKey["outdoor_temp_sam"].Value)
	}
	if byKey["system_mode"].Text != "cool" {
		t.Errorf("mode = %q, want cool", byKey["system_mode"].Text)
	}
}

func TestZoneSettings3B03(t *testing.T) {
	p := make([]byte, 150)
	p[0] = 0x05                      // zones 1 and 3
	p[3], p[5] = 0, 2                // fan auto / med
	p[11] = 0x04                     // zone 3 holding
	p[12], p[14] = 68, 66            // heat setpoints
	p[20], p[22] = 73, 75            // cool setpoints
	p[38], p[39] = 0x02, 0x87        // zone 1 hold duration 647 min
	rs, ok := Decode(samFrame([]byte{0x00, 0x3b, 0x03}, p), time.Now())
	if !ok {
		t.Fatal("3B03 not decoded")
	}
	got := map[string]Reading{}
	for _, r := range rs {
		got[r.Field+string(rune('0'+r.Zone))] = r
	}
	if got["heat_setpoint1"].Value != 68 || got["heat_setpoint3"].Value != 66 {
		t.Errorf("heat setpoints: %+v", got)
	}
	if got["cool_setpoint3"].Value != 75 {
		t.Errorf("cool setpoint z3: %+v", got["cool_setpoint3"])
	}
	if got["fan_mode3"].Text != "med" {
		t.Errorf("fan z3 = %q, want med", got["fan_mode3"].Text)
	}
	if got["hold1"].Value != 0 || got["hold3"].Value != 1 {
		t.Errorf("holds: %+v", got)
	}
	if got["hold_remaining_min1"].Value != 647 {
		t.Errorf("hold duration z1 = %v, want 647", got["hold_remaining_min1"].Value)
	}
	if _, exists := got["heat_setpoint2"]; exists {
		t.Error("zone 2 absent from bitmask — must not decode")
	}
}

func TestFilterLife3B05(t *testing.T) {
	p := []byte{0x01, 0x00, 0x00, 42, 0, 10, 0, 1, 0, 0, 0}
	rs, ok := Decode(samFrame([]byte{0x00, 0x3b, 0x05}, p), time.Now())
	if !ok {
		t.Fatal("3B05 not decoded")
	}
	got := map[string]float64{}
	for _, r := range rs {
		got[r.Field] = r.Value
	}
	if got["filter_life_used"] != 42 {
		t.Errorf("filter = %v, want 42", got["filter_life_used"])
	}
	if got["humidifier_pad_life_used"] != 10 {
		t.Errorf("humidifier pad = %v, want 10", got["humidifier_pad_life_used"])
	}
}

func TestFaultHistory4202(t *testing.T) {
	// Entry layout: code, source addr, hour, minute, days-since-2013-01-01
	// (u16 BE), bit7-inverted-active + count. Entry 0 newest.
	p := make([]byte, 70)
	// Entry 0: code 171, source 0x20, 09:16, day 4956 (=2026-07-28), cleared, count 1.
	copy(p[0:7], []byte{171, 0x20, 9, 16, 0x13, 0x5c, 0x81})
	// Entry 1: code 12, source 0x40, 14:30, same day, ACTIVE (bit7=0), count 3.
	copy(p[7:14], []byte{12, 0x40, 14, 30, 0x13, 0x5c, 0x03})
	faults, ok := DecodeFaults(samFrame([]byte{0x00, 0x42, 0x02}, p))
	if !ok || len(faults) != 2 {
		t.Fatalf("faults = %+v ok=%v, want 2 entries", faults, ok)
	}
	f0 := faults[0]
	if f0.Code != 171 || f0.Source != 0x20 || f0.Active || f0.Count != 1 {
		t.Errorf("entry 0 = %+v", f0)
	}
	if f0.Time.Format("2006-01-02 15:04") != "2026-07-28 09:16" {
		t.Errorf("entry 0 time = %v, want 2026-07-28 09:16", f0.Time)
	}
	if !faults[1].Active || faults[1].Count != 3 {
		t.Errorf("entry 1 = %+v", faults[1])
	}
	// All-zero slots are skipped.
	if _, ok := DecodeFaults(samFrame([]byte{0x00, 0x42, 0x02}, make([]byte, 70))); ok {
		t.Error("empty fault table must return ok=false")
	}
}
```

- [ ] **Step 2: Run to verify FAIL** — `go test ./protocol/`

- [ ] **Step 3: Write implementation**

`protocol/sam.go`:

```go
package protocol

import (
	"time"

	"github.com/utkjmitch/infinid/bus"
)

// SAM-served registers (wall control replies to an active 0x92 reader).
// Layouts per prior art — infinitive/infinitesp/infinitude agree on every
// offset used here; live-validated before the SAM scheduler ships enabled.

// modeNames — 3B02 byte 22 low nibble. Values 0-2 agree across all
// sources; 3+ conflict between source generations, so they decode to the
// raw value with Text "unknown" until live-verified.
var modeNames = []string{"heat", "cool", "auto"}

// systemState3B02 — active_zones(0), temps u8[8]@3, RH u8[8]@11, OAT i8@20,
// stage/mode@22, minutes-since-midnight u16@26.
func systemState3B02(p []byte) []Reading {
	if len(p) < 29 {
		return nil
	}
	zones := p[0]
	rs := []Reading{{Field: "active_zones", Value: float64(zones)}}
	for z := 1; z <= 8; z++ {
		if zones&(1<<(z-1)) == 0 {
			continue
		}
		rs = append(rs,
			Reading{Zone: z, Field: "temp", Value: float64(p[3+z-1])},
			Reading{Zone: z, Field: "humidity", Value: float64(p[11+z-1])},
		)
	}
	mode := p[22] & 0x0f
	text := "unknown"
	if int(mode) < len(modeNames) {
		text = modeNames[mode]
	}
	rs = append(rs,
		Reading{Field: "outdoor_temp_sam", Value: float64(int8(p[20]))},
		Reading{Field: "active_stages", Value: float64(p[22] >> 4)},
		Reading{Field: "system_mode", Value: float64(mode), Text: text},
	)
	return rs
}

// zoneSettings3B03 — fan u8[8]@3, holding bitmap@11, heat u8[8]@12,
// cool u8[8]@20, hold-duration-minutes u16 BE ×8 @38.
func zoneSettings3B03(p []byte) []Reading {
	if len(p) < 150 {
		return nil
	}
	zones := p[0]
	var rs []Reading
	for z := 1; z <= 8; z++ {
		if zones&(1<<(z-1)) == 0 {
			continue
		}
		hold := 0.0
		if p[11]&(1<<(z-1)) != 0 {
			hold = 1
		}
		rs = append(rs,
			Reading{Zone: z, Field: "fan_mode", Value: float64(p[3+z-1]), Text: fanText(p[3+z-1])},
			Reading{Zone: z, Field: "hold", Value: hold},
			Reading{Zone: z, Field: "heat_setpoint", Value: float64(p[12+z-1])},
			Reading{Zone: z, Field: "cool_setpoint", Value: float64(p[20+z-1])},
		)
		if mins := u16(p, 38+2*(z-1)); mins > 0 {
			rs = append(rs, Reading{Zone: z, Field: "hold_remaining_min", Value: float64(mins)})
		}
	}
	return rs
}

// accessoryLife3B05 — consumed % at fixed offsets (0 = new, 100 = replace).
func accessoryLife3B05(p []byte) []Reading {
	if len(p) < 7 {
		return nil
	}
	return []Reading{
		{Field: "filter_life_used", Value: float64(p[3])},
		{Field: "uv_life_used", Value: float64(p[4])},
		{Field: "humidifier_pad_life_used", Value: float64(p[5])},
		{Field: "vent_filter_life_used", Value: float64(p[6])},
	}
}

// Fault is one 4202 fault-history entry.
type Fault struct {
	Code   int
	Source byte // bus address class: 0x20 thermostat, 0x40 IDU, 0x52 ODU
	Time   time.Time
	Active bool
	Count  int
}

// faultEpoch — 4202 day counts are days since 2013-01-01 (nonstandard,
// verified against cloud equipment_events by prior art).
var faultEpoch = time.Date(2013, 1, 1, 0, 0, 0, 0, time.Local)

// DecodeFaults parses a 4202 fault-history reply: 10 entries × 7 bytes,
// newest first; 70 or 72 byte payloads observed (entries at the tail).
// ok=false when f is not a 4202 reply or holds no entries.
func DecodeFaults(f bus.Frame) ([]Fault, bool) {
	if f.Op != bus.OpAck06 || len(f.Data) < 3+70 {
		return nil, false
	}
	if f.Data[0] != 0x00 || f.Data[1] != 0x42 || f.Data[2] != 0x02 {
		return nil, false
	}
	p := f.Data[3:]
	p = p[len(p)-70:] // entries occupy the final 70 bytes
	var out []Fault
	for i := 0; i+7 <= len(p); i += 7 {
		e := p[i : i+7]
		days := int(u16(e, 4))
		if e[0] == 0 && e[1] == 0 && days == 0 {
			continue // empty slot
		}
		day := faultEpoch.AddDate(0, 0, days)
		out = append(out, Fault{
			Code:   int(e[0]),
			Source: e[1],
			Time:   time.Date(day.Year(), day.Month(), day.Day(), int(e[2]), int(e[3]), 0, 0, time.Local),
			Active: e[6]&0x80 == 0, // bit 7 INVERTED: 0 = active
			Count:  int(e[6] & 0x7f),
		})
	}
	return out, len(out) > 0
}
```

Extend the dispatch switch in `protocol/protocol.go`:

```go
	case 0x003b02:
		rs = systemState3B02(p)
	case 0x003b03:
		rs = zoneSettings3B03(p)
	case 0x003b05:
		rs = accessoryLife3B05(p)
```

- [ ] **Step 4: Run to verify PASS** — `go test ./protocol/` (also run `go vet ./...`)

- [ ] **Step 5: Commit**

```bash
git add protocol/ && git commit -m "feat(protocol): SAM tables 3B02/3B03/3B05 + 4202 fault history (prior-art layouts, live gate pending)"
```

---

### Task 5: `state` — assembly, staleness, zone presence

**Files:**
- Create: `state/state.go`, `state/state_test.go`

- [ ] **Step 1: Write the failing test**

`state/state_test.go`:

```go
package state

import (
	"testing"
	"time"

	"github.com/utkjmitch/infinid/protocol"
)

var t0 = time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)

func TestApplyAndSnapshot(t *testing.T) {
	s := New()
	s.Apply(protocol.Reading{Owner: 0x5201, Field: "suction_pressure", Value: 121, TS: t0})
	s.Apply(protocol.Reading{Owner: 0x2201, Zone: 2, Field: "temp", Value: 72.4, TS: t0})
	s.Apply(protocol.Reading{Owner: 0x2201, Zone: 2, Field: "fan_mode", Value: 0, Text: "auto", TS: t0})

	snap := s.Snapshot(t0.Add(10 * time.Second))
	if snap.Sys["suction_pressure"].Value != 121 {
		t.Errorf("sys field: %+v", snap.Sys["suction_pressure"])
	}
	if snap.Zones[2]["temp"].Value != 72.4 {
		t.Errorf("zone field: %+v", snap.Zones[2]["temp"])
	}
	if snap.Zones[2]["fan_mode"].Text != "auto" {
		t.Errorf("text field: %+v", snap.Zones[2]["fan_mode"])
	}
}

func TestStaleness(t *testing.T) {
	s := New()
	s.Apply(protocol.Reading{Field: "supply_cfm", Value: 500, TS: t0})
	fresh := s.Snapshot(t0.Add(1 * time.Minute))
	if fresh.Sys["supply_cfm"].Stale {
		t.Error("1 min old must not be stale (horizon 5 min)")
	}
	old := s.Snapshot(t0.Add(6 * time.Minute))
	if !old.Sys["supply_cfm"].Stale {
		t.Error("6 min old must be stale")
	}
	// SAM-sourced hourly fields get the long horizon.
	s.Apply(protocol.Reading{Field: "filter_life_used", Value: 42, TS: t0})
	if s.Snapshot(t0.Add(2 * time.Hour)).Sys["filter_life_used"].Stale {
		t.Error("filter at 2h must not be stale (horizon 3h)")
	}
}

func TestZonePresence(t *testing.T) {
	s := New()
	// Zone 1 always assumed (wall control's own zone). Damper readings alone
	// must NOT create zones (slot 4 read 0x00 on the reference 3-zone system).
	s.Apply(protocol.Reading{Owner: 0x6001, Zone: 4, Field: "damper_position", Value: 0, TS: t0})
	snap := s.Snapshot(t0)
	if _, ok := snap.Zones[4]; ok {
		t.Error("damper traffic alone must not establish a zone")
	}
	if _, ok := snap.Zones[1]; !ok {
		t.Error("zone 1 (wall control's own) must exist by default")
	}
	// Sensor traffic establishes a zone; its damper then attaches.
	s.Apply(protocol.Reading{Owner: 0x2201, Zone: 2, Field: "temp", Value: 71, TS: t0})
	s.Apply(protocol.Reading{Owner: 0x6001, Zone: 2, Field: "damper_position", Value: 7, TS: t0})
	snap = s.Snapshot(t0)
	if snap.Zones[2]["damper_position"].Value != 7 {
		t.Errorf("zone2 damper: %+v", snap.Zones[2])
	}
	// SAM active_zones bitmask is authoritative: 0b0111 adds zone 3.
	s.Apply(protocol.Reading{Owner: 0x2001, Field: "active_zones", Value: 7, TS: t0})
	snap = s.Snapshot(t0)
	if _, ok := snap.Zones[3]; !ok {
		t.Error("SAM bitmask must establish zone 3")
	}
}
```

- [ ] **Step 2: Run to verify FAIL** — `go test ./state/`

- [ ] **Step 3: Write implementation**

`state/state.go`:

```go
// Package state assembles protocol readings into a coherent SystemState
// with per-field staleness and zone presence tracking.
package state

import (
	"sync"
	"time"

	"github.com/utkjmitch/infinid/protocol"
)

// Field is one assembled value with its observation time.
type Field struct {
	Value float64   `json:"value"`
	Text  string    `json:"text,omitempty"`
	TS    time.Time `json:"ts"`
	Stale bool      `json:"stale,omitempty"`
}

// Snapshot is an immutable copy of the assembled state.
type Snapshot struct {
	Sys   map[string]Field         `json:"sys"`
	Zones map[int]map[string]Field `json:"zones"`
}

// horizons: how old a field may be before it reports stale. Fast fields
// ride 10-16 s bus cadences; SAM accessory/fault fields refresh hourly.
var horizons = map[string]time.Duration{
	"filter_life_used":         3 * time.Hour,
	"uv_life_used":             3 * time.Hour,
	"humidifier_pad_life_used": 3 * time.Hour,
	"vent_filter_life_used":    3 * time.Hour,
}

const defaultHorizon = 5 * time.Minute

// State is the concurrent-safe assembler.
type State struct {
	mu    sync.Mutex
	sys   map[string]Field
	zones map[int]map[string]Field
}

// New returns a State with zone 1 pre-established: the wall control always
// owns a zone, and it is index 1 on every observed system. The SAM
// active_zones bitmask corrects the zone set when SAM reads are enabled.
func New() *State {
	return &State{
		sys:   map[string]Field{},
		zones: map[int]map[string]Field{1: {}},
	}
}

// Apply folds one reading into the state. Zone-presence rule: a zone is
// established by zone-sensor traffic (owner 0x21xx-0x28xx) or the SAM
// active_zones bitmask — never by damper slots alone (a wired-but-absent
// slot can read 0x00, indistinguishable from a closed damper).
func (s *State) Apply(r protocol.Reading) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if r.Field == "active_zones" {
		mask := int(r.Value)
		for z := 1; z <= 8; z++ {
			if mask&(1<<(z-1)) != 0 && s.zones[z] == nil {
				s.zones[z] = map[string]Field{}
			}
		}
	}

	f := Field{Value: r.Value, Text: r.Text, TS: r.TS}
	if r.Zone == 0 {
		s.sys[r.Field] = f
		return
	}
	zf := s.zones[r.Zone]
	if zf == nil {
		if r.Field == "damper_position" || r.Field == "damper_cmd" {
			return // dampers alone don't establish zones
		}
		zf = map[string]Field{}
		s.zones[r.Zone] = zf
	}
	zf[r.Field] = f
}

// Snapshot copies the state, marking staleness as of now.
func (s *State) Snapshot(now time.Time) Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := Snapshot{Sys: map[string]Field{}, Zones: map[int]map[string]Field{}}
	for k, f := range s.sys {
		snap.Sys[k] = staled(k, f, now)
	}
	for z, zf := range s.zones {
		out := map[string]Field{}
		for k, f := range zf {
			out[k] = staled(k, f, now)
		}
		snap.Zones[z] = out
	}
	return snap
}

func staled(field string, f Field, now time.Time) Field {
	h, ok := horizons[field]
	if !ok {
		h = defaultHorizon
	}
	f.Stale = now.Sub(f.TS) > h
	return f
}
```

- [ ] **Step 4: Run to verify PASS** — `go test ./state/`

- [ ] **Step 5: Commit**

```bash
git add state/ && git commit -m "feat(state): reading assembly, staleness horizons, zone-presence rule"
```

---

### Task 6: `state` — liveness tracking + event journal + outage classification

**Files:**
- Create: `state/journal.go`, `state/journal_test.go`

- [ ] **Step 1: Write the failing test**

`state/journal_test.go`:

```go
package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/utkjmitch/infinid/protocol"
)

func testJournal(t *testing.T) (*Journal, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.jsonl")
	j, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	return j, path
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func TestDeviceLiveness(t *testing.T) {
	j, path := testJournal(t)
	defer j.Close()
	j.NoteDevice(0x5201, t0)
	j.NoteDevice(0x3e01, t0)
	// ODU silent for 45s while IDU still talks → device_lost for ODU only.
	j.CheckLiveness(t0.Add(45*time.Second), []uint16{0x3e01})
	// It comes back.
	j.NoteDevice(0x5201, t0.Add(60*time.Second))
	lines := readLines(t, path)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, `"device_lost"`) || !strings.Contains(joined, `"5201"`) {
		t.Errorf("missing device_lost: %s", joined)
	}
	if !strings.Contains(joined, `"device_recovered"`) {
		t.Errorf("missing device_recovered: %s", joined)
	}
}

func TestBusSilence(t *testing.T) {
	j, path := testJournal(t)
	defer j.Close()
	j.NoteDevice(0x5201, t0)
	j.CheckLiveness(t0.Add(2*time.Minute), nil) // no frames at all → bus_silent
	j.NoteDevice(0x5201, t0.Add(3*time.Minute))
	joined := strings.Join(readLines(t, path), "\n")
	if !strings.Contains(joined, `"bus_silent"`) || !strings.Contains(joined, `"bus_recovered"`) {
		t.Errorf("missing bus events: %s", joined)
	}
}

func TestOutageClassification(t *testing.T) {
	j, path := testJournal(t)
	// Journal the reference counters, then simulate a restart over the same file.
	j.NotePowerCounters(map[string]float64{"idu_power_cycles": 24, "odu_power_cycles": 6}, t0)
	j.Close()

	j2, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	defer j2.Close()
	// Counters unchanged → the gap was ours (monitoring_gap).
	j2.ClassifyGap(map[string]float64{"idu_power_cycles": 24, "odu_power_cycles": 6}, t0.Add(time.Hour))
	joined := strings.Join(readLines(t, path), "\n")
	if !strings.Contains(joined, `"monitoring_gap"`) {
		t.Errorf("want monitoring_gap: %s", joined)
	}

	// Counter bumped → HVAC lost power during the gap.
	j2.NotePowerCounters(map[string]float64{"idu_power_cycles": 24, "odu_power_cycles": 6}, t0.Add(time.Hour))
	j2.Close()
	j3, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	defer j3.Close()
	j3.ClassifyGap(map[string]float64{"idu_power_cycles": 25, "odu_power_cycles": 7}, t0.Add(2*time.Hour))
	joined = strings.Join(readLines(t, path), "\n")
	if !strings.Contains(joined, `"hvac_power_loss"`) {
		t.Errorf("want hvac_power_loss: %s", joined)
	}
}

func TestFaultTransitions(t *testing.T) {
	j, path := testJournal(t)
	defer j.Close()
	active := []protocol.Fault{{Code: 12, Source: 0x40, Time: t0, Active: true, Count: 1}}
	j.NoteFaults(active, t0)
	j.NoteFaults(active, t0.Add(time.Hour)) // unchanged → no duplicate event
	cleared := []protocol.Fault{{Code: 12, Source: 0x40, Time: t0, Active: false, Count: 1}}
	j.NoteFaults(cleared, t0.Add(2*time.Hour))
	j.NoteFaults(nil, t0.Add(3*time.Hour)) // history wiped → manually_cleared
	lines := readLines(t, path)
	joined := strings.Join(lines, "\n")
	if strings.Count(joined, `"fault_seen"`) != 1 {
		t.Errorf("want exactly one fault_seen: %s", joined)
	}
	if !strings.Contains(joined, `"self_cleared"`) {
		t.Errorf("active→cleared with entry retained must be self_cleared: %s", joined)
	}
	if !strings.Contains(joined, `"manually_cleared"`) {
		t.Errorf("history wipe must be manually_cleared: %s", joined)
	}
}
```

- [ ] **Step 2: Run to verify FAIL** — `go test ./state/`

- [ ] **Step 3: Write implementation**

`state/journal.go`:

```go
package state

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/utkjmitch/infinid/protocol"
)

// Event is one journal record. The journal is the unit's service history —
// append-only JSONL, replayed on open to recover reference values.
type Event struct {
	TS     time.Time          `json:"ts"`
	Type   string             `json:"type"`
	Device string             `json:"device,omitempty"`   // hex bus address
	Fault  *FaultRecord       `json:"fault,omitempty"`
	Gap    string             `json:"gap,omitempty"`      // outage classification
	Values map[string]float64 `json:"values,omitempty"`   // power counters
}

// FaultRecord mirrors protocol.Fault plus the resolution cause.
type FaultRecord struct {
	Code       int       `json:"code"`
	Source     string    `json:"source"`
	Time       time.Time `json:"time"`
	Count      int       `json:"count"`
	Active     bool      `json:"active"`
	Resolution string    `json:"resolution,omitempty"` // self_cleared | manually_cleared | unknown
}

const (
	deviceLostAfter = 30 * time.Second
	busSilentAfter  = 60 * time.Second
)

// Journal tracks liveness/faults and appends events to a JSONL file.
type Journal struct {
	mu        sync.Mutex
	f         *os.File
	w         *bufio.Writer
	lastSeen  map[uint16]time.Time
	lost      map[uint16]bool
	busSilent bool
	counters  map[string]float64          // last journaled power counters
	faults    map[string]protocol.Fault   // key: code/source/first-time
}

// OpenJournal opens (or creates) the journal at path, replaying existing
// events to recover the last power counters and known-fault set.
func OpenJournal(path string) (*Journal, error) {
	j := &Journal{
		lastSeen: map[uint16]time.Time{},
		lost:     map[uint16]bool{},
		counters: map[string]float64{},
		faults:   map[string]protocol.Fault{},
	}
	if existing, err := os.Open(path); err == nil {
		sc := bufio.NewScanner(existing)
		for sc.Scan() {
			var e Event
			if json.Unmarshal(sc.Bytes(), &e) != nil {
				continue
			}
			if e.Type == "power_counters" {
				j.counters = e.Values
			}
			if e.Type == "fault_seen" && e.Fault != nil {
				f := protocol.Fault{Code: e.Fault.Code, Time: e.Fault.Time,
					Count: e.Fault.Count, Active: e.Fault.Active}
				j.faults[faultKey(f)] = f
			}
			if (e.Type == "fault_cleared" || e.Type == "fault_history_wiped") && e.Fault != nil {
				delete(j.faults, faultKey(protocol.Fault{Code: e.Fault.Code, Time: e.Fault.Time}))
			}
		}
		existing.Close()
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	j.f, j.w = f, bufio.NewWriter(f)
	return j, nil
}

func faultKey(f protocol.Fault) string {
	return fmt.Sprintf("%d/%s", f.Code, f.Time.Format(time.RFC3339))
}

func (j *Journal) append(e Event) {
	line, _ := json.Marshal(e)
	j.w.Write(append(line, '\n'))
	j.w.Flush()
}

// NoteDevice records that addr produced a frame at now.
func (j *Journal) NoteDevice(addr uint16, now time.Time) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.busSilent {
		j.busSilent = false
		j.append(Event{TS: now, Type: "bus_recovered"})
	}
	if j.lost[addr] {
		delete(j.lost, addr)
		j.append(Event{TS: now, Type: "device_recovered", Device: fmt.Sprintf("%04x", addr)})
	}
	j.lastSeen[addr] = now
}

// CheckLiveness runs periodically: activeNow lists devices known to have
// produced frames since the last check (their last-seen refreshes to now;
// callers that NoteDevice per frame pass nil). No devices at all past the
// silence horizon → bus_silent; individual devices silent past
// deviceLostAfter while others talk → device_lost.
func (j *Journal) CheckLiveness(now time.Time, activeNow []uint16) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, a := range activeNow {
		j.lastSeen[a] = now
	}
	newest := time.Time{}
	for _, ts := range j.lastSeen {
		if ts.After(newest) {
			newest = ts
		}
	}
	if !j.busSilent && !newest.IsZero() && now.Sub(newest) > busSilentAfter {
		j.busSilent = true
		j.append(Event{TS: now, Type: "bus_silent"})
		return
	}
	if j.busSilent {
		return // whole-bus outage; individual losses are meaningless
	}
	for addr, ts := range j.lastSeen {
		if !j.lost[addr] && now.Sub(ts) > deviceLostAfter {
			j.lost[addr] = true
			j.append(Event{TS: now, Type: "device_lost", Device: fmt.Sprintf("%04x", addr)})
		}
	}
}

// BusSilent reports whether the whole bus is currently silent.
func (j *Journal) BusSilent() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.busSilent
}

// NotePowerCounters journals the power-on cycle counters when they change —
// the reference values ClassifyGap compares against after a restart.
func (j *Journal) NotePowerCounters(vals map[string]float64, now time.Time) {
	j.mu.Lock()
	defer j.mu.Unlock()
	changed := len(j.counters) != len(vals)
	for k, v := range vals {
		if j.counters[k] != v {
			changed = true
		}
	}
	if !changed {
		return
	}
	j.counters = vals
	j.append(Event{TS: now, Type: "power_counters", Values: vals})
}

// ClassifyGap runs once per daemon start, after the first counter readings
// arrive: unchanged counters → the HVAC ran fine unobserved
// (monitoring_gap); a bumped counter → the HVAC lost power during the gap
// (hvac_power_loss). With no prior reference, the gap is unclassifiable.
func (j *Journal) ClassifyGap(vals map[string]float64, now time.Time) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(j.counters) == 0 {
		j.append(Event{TS: now, Type: "outage_classified", Gap: "no_reference"})
		return
	}
	gap := "monitoring_gap"
	for k, v := range vals {
		if prev, ok := j.counters[k]; ok && v > prev {
			gap = "hvac_power_loss"
		}
	}
	j.append(Event{TS: now, Type: "outage_classified", Gap: gap})
}

// NoteFaults diffs a fresh 4202 read against the known-fault set.
// New entry → fault_seen. Active→cleared with the entry retained →
// fault_cleared/self_cleared. Entire history emptied while faults were
// known → fault_history_wiped/manually_cleared (the panel's reset wipes
// resettable faults; the daemon never clears anything).
func (j *Journal) NoteFaults(current []protocol.Fault, now time.Time) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(current) == 0 && len(j.faults) > 0 {
		for _, f := range j.faults {
			j.append(Event{TS: now, Type: "fault_history_wiped", Fault: &FaultRecord{
				Code: f.Code, Time: f.Time, Count: f.Count,
				Resolution: "manually_cleared"}})
		}
		j.faults = map[string]protocol.Fault{}
		return
	}
	for _, f := range current {
		k := faultKey(f)
		prev, known := j.faults[k]
		switch {
		case !known:
			j.faults[k] = f
			j.append(Event{TS: now, Type: "fault_seen", Fault: recordOf(f, "")})
		case prev.Active && !f.Active:
			j.faults[k] = f
			j.append(Event{TS: now, Type: "fault_cleared", Fault: recordOf(f, "self_cleared")})
		case f.Count > prev.Count:
			j.faults[k] = f
			j.append(Event{TS: now, Type: "fault_recurred", Fault: recordOf(f, "")})
		}
	}
}

func recordOf(f protocol.Fault, resolution string) *FaultRecord {
	return &FaultRecord{Code: f.Code, Source: fmt.Sprintf("%02x", f.Source),
		Time: f.Time, Count: f.Count, Active: f.Active, Resolution: resolution}
}

// ActiveFaults returns the currently-active known faults (unordered).
func (j *Journal) ActiveFaults() []protocol.Fault {
	j.mu.Lock()
	defer j.mu.Unlock()
	var out []protocol.Fault
	for _, f := range j.faults {
		if f.Active {
			out = append(out, f)
		}
	}
	return out
}

// Close flushes and closes the journal file.
func (j *Journal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.w.Flush()
	return j.f.Close()
}
```

- [ ] **Step 4: Run to verify PASS** — `go test ./state/ -race`

- [ ] **Step 5: Commit**

```bash
git add state/ && git commit -m "feat(state): event journal — liveness, fault lifecycle, power-counter outage classification"
```

---

### Task 7: `sam` — read-only request scheduler

**Files:**
- Create: `sam/scheduler.go`, `sam/scheduler_test.go`

- [ ] **Step 1: Write the failing test**

`sam/scheduler_test.go`:

```go
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
```

- [ ] **Step 2: Run to verify FAIL** — `go test ./sam/`

- [ ] **Step 3: Write implementation**

`sam/scheduler.go`:

```go
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
)

type target struct {
	Target
	due     time.Time
	backoff time.Duration
}

// Scheduler paces read requests: one outstanding at a time, a global
// inter-send gap, exponential backoff per target on timeout. Callers drive
// it from the bus read loop: Tick after each frame (the natural inter-frame
// gap), NoteFrame for every decoded frame.
type Scheduler struct {
	w        io.Writer
	targets  []*target
	pending  *target
	sentAt   time.Time
	lastSend time.Time
	failures int
}

// New builds a scheduler writing requests to w (the serial port).
func New(w io.Writer, targets []Target) *Scheduler {
	s := &Scheduler{w: w}
	for _, t := range targets {
		s.targets = append(s.targets, &target{Target: t})
	}
	return s
}

// Tick sends the most-overdue target if none is pending and the inter-send
// gap has passed. A pending request past its timeout counts as a failure
// and doubles that target's backoff.
func (s *Scheduler) Tick(now time.Time) {
	if s.pending != nil {
		if now.Sub(s.sentAt) < pendingTimeout {
			return
		}
		s.failures++
		p := s.pending
		if p.backoff == 0 {
			p.backoff = 4 * time.Second
		} else if p.backoff < maxBackoff {
			p.backoff *= 2
		}
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
		return
	}
	s.pending = pick
	s.sentAt = now
	s.lastSend = now
}

// NoteFrame observes a decoded frame; a wall-control reply addressed to the
// SAM for the pending register completes the transaction and resets backoff.
func (s *Scheduler) NoteFrame(f bus.Frame, now time.Time) {
	if s.pending == nil {
		return
	}
	if f.Src != bus.DevWallControl || f.Dst != bus.DevSAM {
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

// Failures returns the cumulative timeout/write-failure count (published as
// daemon health).
func (s *Scheduler) Failures() int { return s.failures }
```

- [ ] **Step 4: Run to verify PASS** — `go test ./sam/ -race`

- [ ] **Step 5: Commit**

```bash
git add sam/ && git commit -m "feat(sam): read-only request scheduler — single outstanding, spaced, backoff"
```

---

### Task 8: `mqtt` — entity definitions, discovery, state publishing

**Files:**
- Create: `mqtt/entities.go`, `mqtt/exporter.go`, `mqtt/exporter_test.go`

- [ ] **Step 1: Write the failing test**

`mqtt/exporter_test.go`:

```go
package mqtt

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/utkjmitch/infinid/state"
)

var t0 = time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)

type fakePub struct {
	msgs map[string][]string // topic → payloads in order
	ret  map[string]bool
}

func newFake() *fakePub { return &fakePub{msgs: map[string][]string{}, ret: map[string]bool{}} }

func (f *fakePub) Publish(topic string, payload []byte, retain bool) error {
	f.msgs[topic] = append(f.msgs[topic], string(payload))
	f.ret[topic] = retain
	return nil
}

func testConfig() Config {
	return Config{BaseTopic: "infinid", DiscoveryPrefix: "homeassistant",
		ZoneNames: []string{"bedrooms", "living_room", "basement"}, Version: "test"}
}

func snapWith(sys map[string]state.Field, zones map[int]map[string]state.Field) state.Snapshot {
	if sys == nil {
		sys = map[string]state.Field{}
	}
	if zones == nil {
		zones = map[int]map[string]state.Field{}
	}
	return state.Snapshot{Sys: sys, Zones: zones}
}

func TestDiscoveryContractIDs(t *testing.T) {
	p := newFake()
	e := New(p, testConfig())
	e.PublishDiscovery([]int{1, 2, 3})
	// The 12 frozen contract ids — object_id and unique_id must match verbatim.
	contract := []string{
		"infinid_compressor_stage", "infinid_compressor_rpm", "infinid_supply_cfm",
		"infinid_blower_rpm", "infinid_static_pressure", "infinid_blower_watts",
		"infinid_suction_pressure", "infinid_outdoor_coil_temp", "infinid_discharge_temp",
		"infinid_damper_bedrooms", "infinid_damper_living_room", "infinid_damper_basement",
	}
	for _, id := range contract {
		topic := "homeassistant/sensor/" + id + "/config"
		if len(p.msgs[topic]) == 0 {
			t.Errorf("no discovery for %s", id)
			continue
		}
		if !p.ret[topic] {
			t.Errorf("%s discovery not retained", id)
		}
		var cfg map[string]any
		if err := json.Unmarshal([]byte(p.msgs[topic][0]), &cfg); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if cfg["unique_id"] != id {
			t.Errorf("%s unique_id = %v", id, cfg["unique_id"])
		}
		if cfg["availability_topic"] != "infinid/availability" {
			t.Errorf("%s availability = %v", id, cfg["availability_topic"])
		}
	}
	// Zone devices: zone 2 temp entity exists and belongs to the zone device.
	ztopic := "homeassistant/sensor/infinid_zone_living_room_temp/config"
	if len(p.msgs[ztopic]) == 0 {
		t.Fatal("no zone temp discovery")
	}
	var zcfg map[string]any
	json.Unmarshal([]byte(p.msgs[ztopic][0]), &zcfg)
	dev := zcfg["device"].(map[string]any)
	ids := dev["identifiers"].([]any)
	if ids[0] != "infinid_zone_2" {
		t.Errorf("zone device identifiers = %v", ids)
	}
}

func TestStatePublishChangeDetection(t *testing.T) {
	p := newFake()
	e := New(p, testConfig())
	snap := snapWith(map[string]state.Field{
		"suction_pressure": {Value: 121, TS: t0},
	}, nil)
	e.PublishState(snap, t0)
	e.PublishState(snap, t0.Add(time.Second)) // unchanged → no republish
	topic := "infinid/suction_pressure/state"
	if len(p.msgs[topic]) != 1 {
		t.Fatalf("published %d times, want 1 (change detection)", len(p.msgs[topic]))
	}
	if p.msgs[topic][0] != "121" {
		t.Errorf("payload = %q", p.msgs[topic][0])
	}
	// Heartbeat: after 60s everything republishes even unchanged.
	e.PublishState(snap, t0.Add(61*time.Second))
	if len(p.msgs[topic]) != 2 {
		t.Fatalf("heartbeat republish missing")
	}
}

func TestDamperPercentTransform(t *testing.T) {
	p := newFake()
	e := New(p, testConfig())
	snap := snapWith(nil, map[int]map[string]state.Field{
		2: {"damper_position": {Value: 7, TS: t0}},
	})
	e.PublishState(snap, t0)
	got := p.msgs["infinid/damper_living_room/state"]
	if len(got) != 1 || got[0] != "47" { // round(7/15*100)
		t.Fatalf("damper payload = %v, want [47]", got)
	}
}

func TestTextAndStaleFields(t *testing.T) {
	p := newFake()
	e := New(p, testConfig())
	snap := snapWith(nil, map[int]map[string]state.Field{
		1: {"fan_mode": {Value: 2, Text: "med", TS: t0}},
	})
	e.PublishState(snap, t0)
	if got := p.msgs["infinid/zone_bedrooms_fan_mode/state"]; len(got) != 1 || got[0] != "med" {
		t.Fatalf("fan_mode payload = %v", got)
	}
	// Stale fields publish "None" so HA shows unknown instead of stale-as-fresh.
	stale := snapWith(nil, map[int]map[string]state.Field{
		1: {"fan_mode": {Value: 2, Text: "med", TS: t0, Stale: true}},
	})
	e.PublishState(stale, t0.Add(2*time.Minute))
	msgs := p.msgs["infinid/zone_bedrooms_fan_mode/state"]
	if msgs[len(msgs)-1] != "None" {
		t.Fatalf("stale payload = %q, want None", msgs[len(msgs)-1])
	}
}
```

- [ ] **Step 2: Run to verify FAIL** — `go test ./mqtt/`

- [ ] **Step 3: Write implementation**

`mqtt/entities.go`:

```go
// Package mqtt publishes Home Assistant MQTT-discovery entities from state
// snapshots. Contract: docs/MQTT-CONTRACT.md — the 12 diagnostic ids are
// frozen verbatim; everything else is additive-only.
package mqtt

import "fmt"

// entityDef maps one state field to one HA entity.
type entityDef struct {
	field       string // key in Snapshot.Sys or Snapshot.Zones[z]
	object      string // object/unique id suffix after "infinid_"; zone defs use %s = zone name
	name        string
	unit        string
	deviceClass string
	stateClass  string
	transform   func(v float64) string // optional; default = trimmed float
	text        bool                   // publish Field.Text instead of Value
}

func pct15(v float64) string { return fmt.Sprintf("%.0f", v/15.0*100.0) }

// inv100 publishes bus-native "consumed %" fields as the remaining % every
// consumer convention expects (decided in plan grill; REST keeps the raw
// used value).
func inv100(v float64) string { return fmt.Sprintf("%.0f", 100.0-v) }

// sysEntities: system-level sensors. The first 9 + the damper pattern are
// the frozen dashboard contract.
var sysEntities = []entityDef{
	{field: "compressor_stage", object: "compressor_stage", name: "Compressor stage"},
	{field: "compressor_rpm", object: "compressor_rpm", name: "Compressor RPM", unit: "rpm"},
	{field: "supply_cfm", object: "supply_cfm", name: "Supply airflow", unit: "CFM"},
	{field: "blower_rpm", object: "blower_rpm", name: "Blower RPM", unit: "rpm"},
	{field: "static_pressure", object: "static_pressure", name: "Static pressure", unit: "inH2O", stateClass: "measurement"},
	{field: "blower_watts", object: "blower_watts", name: "Blower power", unit: "W", deviceClass: "power", stateClass: "measurement"},
	{field: "suction_pressure", object: "suction_pressure", name: "Suction pressure", unit: "psi", deviceClass: "pressure", stateClass: "measurement"},
	{field: "outdoor_coil_temp", object: "outdoor_coil_temp", name: "Outdoor coil", unit: "°F", deviceClass: "temperature", stateClass: "measurement"},
	{field: "discharge_temp", object: "discharge_temp", name: "Discharge temp", unit: "°F", deviceClass: "temperature", stateClass: "measurement"},
	// Additive beyond the contract:
	{field: "outdoor_temp", object: "outdoor_temp", name: "Outdoor temp", unit: "°F", deviceClass: "temperature", stateClass: "measurement"},
	{field: "supply_air_temp", object: "supply_air_temp", name: "Supply air temp", unit: "°F", deviceClass: "temperature", stateClass: "measurement"},
	{field: "suction_temp", object: "suction_temp", name: "Suction temp", unit: "°F", deviceClass: "temperature", stateClass: "measurement"},
	{field: "superheat", object: "superheat", name: "Superheat", unit: "°F", stateClass: "measurement"},
	{field: "line_voltage", object: "line_voltage", name: "Line voltage", unit: "V", deviceClass: "voltage", stateClass: "measurement"},
	{field: "filter_life_used", object: "filter_life", name: "Filter life", unit: "%", transform: inv100},
	{field: "heat_stage1_cycles", object: "heat_stage1_cycles", name: "Heat stage 1 cycles", stateClass: "total_increasing"},
	{field: "heat_stage2_cycles", object: "heat_stage2_cycles", name: "Heat stage 2 cycles", stateClass: "total_increasing"},
	{field: "blower_cycles", object: "blower_cycles", name: "Blower cycles", stateClass: "total_increasing"},
	{field: "cool_cycles", object: "cool_cycles", name: "Cool cycles", stateClass: "total_increasing"},
	{field: "heat_stage1_hours", object: "heat_stage1_hours", name: "Heat stage 1 hours", unit: "h", stateClass: "total_increasing"},
	{field: "heat_stage2_hours", object: "heat_stage2_hours", name: "Heat stage 2 hours", unit: "h", stateClass: "total_increasing"},
	{field: "blower_hours", object: "blower_hours", name: "Blower hours", unit: "h", stateClass: "total_increasing"},
	{field: "cool_hours", object: "cool_hours", name: "Cool hours", unit: "h", stateClass: "total_increasing"},
	{field: "idu_power_cycles", object: "idu_power_cycles", name: "Indoor unit power cycles", stateClass: "total_increasing"},
	{field: "odu_power_cycles", object: "odu_power_cycles", name: "Outdoor unit power cycles", stateClass: "total_increasing"},
	{field: "system_mode", object: "system_mode", name: "System mode", text: true},
}

// zoneEntities: per-zone sensors; object pattern "zone_<name>_<suffix>",
// except the damper which is the frozen "damper_<name>" contract id.
var zoneEntities = []entityDef{
	{field: "temp", object: "zone_%s_temp", name: "Temperature", unit: "°F", deviceClass: "temperature", stateClass: "measurement"},
	{field: "humidity", object: "zone_%s_humidity", name: "Humidity", unit: "%", deviceClass: "humidity", stateClass: "measurement"},
	{field: "cool_setpoint", object: "zone_%s_cool_setpoint", name: "Cool setpoint", unit: "°F", deviceClass: "temperature"},
	{field: "heat_setpoint", object: "zone_%s_heat_setpoint", name: "Heat setpoint", unit: "°F", deviceClass: "temperature"},
	{field: "fan_mode", object: "zone_%s_fan_mode", name: "Fan mode", text: true},
	{field: "hold", object: "zone_%s_hold", name: "Hold"},
	{field: "hold_remaining_min", object: "zone_%s_hold_remaining", name: "Hold remaining", unit: "min"},
	{field: "damper_position", object: "damper_%s", name: "Damper", unit: "%", transform: pct15},
}
```

`mqtt/exporter.go`:

```go
package mqtt

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/utkjmitch/infinid/state"
)

// Publisher is the transport seam — the paho adapter in production, a fake
// in tests.
type Publisher interface {
	Publish(topic string, payload []byte, retain bool) error
}

// Config parameterizes topics and zone naming. ZoneNames[i] names zone
// index i+1; missing names fall back to "zone_<n>".
type Config struct {
	BaseTopic       string
	DiscoveryPrefix string
	ZoneNames       []string
	Version         string
}

const heartbeat = 60 * time.Second

// Exporter publishes discovery + state with change detection.
type Exporter struct {
	p          Publisher
	cfg        Config
	discovered map[string]bool   // object ids with discovery published
	last       map[string]string // state topic → last payload
	lastBeat   time.Time
}

// New builds an Exporter.
func New(p Publisher, cfg Config) *Exporter {
	return &Exporter{p: p, cfg: cfg,
		discovered: map[string]bool{}, last: map[string]string{}}
}

func (e *Exporter) zoneName(z int) string {
	if z-1 < len(e.cfg.ZoneNames) && z >= 1 && e.cfg.ZoneNames[z-1] != "" {
		return e.cfg.ZoneNames[z-1]
	}
	return fmt.Sprintf("zone_%d", z)
}

func (e *Exporter) availabilityTopic() string { return e.cfg.BaseTopic + "/availability" }

// device blocks: one hub device, one device per zone (via_device → hub).
func (e *Exporter) hubDevice() map[string]any {
	return map[string]any{
		"identifiers": []string{"infinid"}, "name": "infinid",
		"manufacturer": "infinid", "model": "ABCD bus daemon",
		"sw_version": e.cfg.Version,
	}
}

func (e *Exporter) zoneDevice(z int) map[string]any {
	return map[string]any{
		"identifiers": []string{fmt.Sprintf("infinid_zone_%d", z)},
		"name":        "Zone " + e.zoneName(z),
		"manufacturer": "infinid", "model": "Infinity zone",
		"via_device": "infinid",
	}
}

// PublishDiscovery publishes retained discovery configs for the hub
// entities and each zone in zones. Idempotent per object id — call freely
// when new zones appear.
func (e *Exporter) PublishDiscovery(zones []int) {
	for _, def := range sysEntities {
		e.publishConfig(def, def.object, e.hubDevice())
	}
	for _, z := range zones {
		for _, def := range zoneEntities {
			object := fmt.Sprintf(def.object, e.zoneName(z))
			e.publishConfig(def, object, e.zoneDevice(z))
		}
	}
}

func (e *Exporter) publishConfig(def entityDef, object string, device map[string]any) {
	id := "infinid_" + object
	if e.discovered[id] {
		return
	}
	cfg := map[string]any{
		"name":               def.name,
		"unique_id":          id,
		"object_id":          id,
		"state_topic":        fmt.Sprintf("%s/%s/state", e.cfg.BaseTopic, object),
		"availability_topic": e.availabilityTopic(),
		"device":             device,
	}
	if def.unit != "" {
		cfg["unit_of_measurement"] = def.unit
	}
	if def.deviceClass != "" {
		cfg["device_class"] = def.deviceClass
	}
	if def.stateClass != "" {
		cfg["state_class"] = def.stateClass
	}
	payload, _ := json.Marshal(cfg)
	topic := fmt.Sprintf("%s/sensor/%s/config", e.cfg.DiscoveryPrefix, id)
	if e.p.Publish(topic, payload, true) == nil {
		e.discovered[id] = true
	}
}

// PublishState publishes changed fields (retained) and re-publishes
// everything on the heartbeat so a restarted broker/HA converges. Stale
// fields publish "None" — unknown beats stale-as-fresh.
func (e *Exporter) PublishState(snap state.Snapshot, now time.Time) {
	beat := now.Sub(e.lastBeat) >= heartbeat
	if beat {
		e.lastBeat = now
	}
	for _, def := range sysEntities {
		if f, ok := snap.Sys[def.field]; ok {
			e.publishField(def, def.object, f, beat)
		}
	}
	for z, zf := range snap.Zones {
		for _, def := range zoneEntities {
			if f, ok := zf[def.field]; ok {
				e.publishField(def, fmt.Sprintf(def.object, e.zoneName(z)), f, beat)
			}
		}
	}
}

func (e *Exporter) publishField(def entityDef, object string, f state.Field, beat bool) {
	var payload string
	switch {
	case f.Stale:
		payload = "None"
	case def.text:
		payload = f.Text
	case def.transform != nil:
		payload = def.transform(f.Value)
	default:
		payload = strconv.FormatFloat(f.Value, 'f', -1, 64)
	}
	topic := fmt.Sprintf("%s/%s/state", e.cfg.BaseTopic, object)
	if !beat && e.last[topic] == payload {
		return
	}
	if e.p.Publish(topic, []byte(payload), true) == nil {
		e.last[topic] = payload
	}
}

// PublishAvailability publishes the retained availability flag. Bus
// silence maps to offline — HA must degrade to unavailable, never show
// stale data as fresh.
func (e *Exporter) PublishAvailability(online bool) {
	v := "offline"
	if online {
		v = "online"
	}
	e.p.Publish(e.availabilityTopic(), []byte(v), true)
}
```

- [ ] **Step 4: Run to verify PASS** — `go test ./mqtt/`

- [ ] **Step 5: Commit**

```bash
git add mqtt/ && git commit -m "feat(mqtt): HA discovery exporter — contract ids, zone devices, change detection + heartbeat"
```

---

### Task 9: `mqtt` — paho client adapter + fault/health entities

**Files:**
- Modify: `mqtt/exporter.go`, `mqtt/entities.go`, `mqtt/exporter_test.go`
- Create: `mqtt/client.go`
- Modify: `go.mod` / `go.sum` (new dependency)

- [ ] **Step 1: Add the dependency**

```bash
cd /d/Dev/Personal/infinid && export PATH="$PATH:/c/Users/jimmy/.go-toolchain/go/bin" && go get github.com/eclipse/paho.mqtt.golang@v1.5.0
```
Check `go.mod` afterward: if `go get` bumped the `go` directive, the Dockerfile (`deploy/haos-addon/Dockerfile`) and CI (`.github/workflows/ci.yml`) Go versions must still be ≥ it — they are pinned to 1.25; fix them in this task if the directive moved past that.

- [ ] **Step 2: Write the failing test** (append to `mqtt/exporter_test.go`; this test uses `strings.Contains`, so add `"strings"` to that file's imports now)

```go
func TestFaultAndHealthEntities(t *testing.T) {
	p := newFake()
	e := New(p, testConfig())
	e.PublishDiscovery(nil)
	for _, id := range []string{"infinid_last_fault", "infinid_fault_count", "infinid_frames_per_min", "infinid_unknown_frames"} {
		if len(p.msgs["homeassistant/sensor/"+id+"/config"]) == 0 {
			t.Errorf("no discovery for %s", id)
		}
	}
	for _, id := range []string{"infinid_fault_active", "infinid_bus_online"} {
		if len(p.msgs["homeassistant/binary_sensor/"+id+"/config"]) == 0 {
			t.Errorf("no discovery for binary_sensor %s", id)
		}
	}
	e.PublishHealth(Health{FramesPerMin: 1500, UnknownFrames: 12, SAMFailures: 0,
		BusOnline: true, FaultCount: 2, LastFault: "171 @ 2026-07-28 09:16 (x1)", FaultActive: false}, t0)
	if got := p.msgs["infinid/frames_per_min/state"]; len(got) != 1 || got[0] != "1500" {
		t.Errorf("frames_per_min = %v", got)
	}
	if got := p.msgs["infinid/bus_online/state"]; len(got) != 1 || got[0] != "ON" {
		t.Errorf("bus_online = %v", got)
	}
	if got := p.msgs["infinid/last_fault/state"]; len(got) != 1 || !strings.Contains(got[0], "171") {
		t.Errorf("last_fault = %v", got)
	}
}
```

- [ ] **Step 3: Run to verify FAIL**, then implement

Append to `mqtt/entities.go`:

```go
// healthEntities publish on the hub device; binary sensors carry a
// distinct discovery component.
var healthEntities = []entityDef{
	{field: "last_fault", object: "last_fault", name: "Last fault", text: true},
	{field: "fault_count", object: "fault_count", name: "Fault count", stateClass: "total_increasing"},
	{field: "frames_per_min", object: "frames_per_min", name: "Bus frames/min"},
	{field: "unknown_frames", object: "unknown_frames", name: "Unknown frames", stateClass: "total_increasing"},
	{field: "sam_failures", object: "sam_failures", name: "SAM read failures", stateClass: "total_increasing"},
}

var binaryEntities = []entityDef{
	{field: "fault_active", object: "fault_active", name: "Fault active", deviceClass: "problem"},
	{field: "bus_online", object: "bus_online", name: "Bus online", deviceClass: "connectivity"},
}
```

In `mqtt/exporter.go`: `publishConfig` gains a `component string` parameter (`"sensor"` / `"binary_sensor"`) used in the topic (update the two existing call sites to pass `"sensor"`); `PublishDiscovery` also loops `healthEntities` (component sensor) and `binaryEntities` (component binary_sensor). Add:

```go
// Health is the daemon's own vitals, published alongside decoded state.
type Health struct {
	FramesPerMin  float64
	UnknownFrames float64
	SAMFailures   float64
	BusOnline     bool
	FaultActive   bool
	FaultCount    float64
	LastFault     string
}

// PublishHealth publishes daemon vitals and fault summary entities.
func (e *Exporter) PublishHealth(h Health, now time.Time) {
	pub := func(object, payload string) {
		topic := fmt.Sprintf("%s/%s/state", e.cfg.BaseTopic, object)
		if e.last[topic] == payload {
			return
		}
		if e.p.Publish(topic, []byte(payload), true) == nil {
			e.last[topic] = payload
		}
	}
	num := func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
	onoff := func(b bool) string {
		if b {
			return "ON"
		}
		return "OFF"
	}
	pub("frames_per_min", num(h.FramesPerMin))
	pub("unknown_frames", num(h.UnknownFrames))
	pub("sam_failures", num(h.SAMFailures))
	pub("fault_count", num(h.FaultCount))
	pub("last_fault", h.LastFault)
	pub("fault_active", onoff(h.FaultActive))
	pub("bus_online", onoff(h.BusOnline))
}
```

`mqtt/client.go`:

```go
package mqtt

import (
	"fmt"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
)

// PahoPublisher adapts an eclipse/paho client to the Publisher seam, with
// LWT ("offline" retained on the availability topic), auto-reconnect, and
// QoS 0 (retained state makes redelivery unnecessary).
type PahoPublisher struct {
	c paho.Client
}

// Connect dials the broker. broker is a URL like tcp://host:1883. The LWT
// makes daemon death indistinguishable from an explicit offline — HA
// degrades to unavailable either way.
func Connect(broker, user, pass, clientID, availabilityTopic string) (*PahoPublisher, error) {
	opts := paho.NewClientOptions().
		AddBroker(broker).
		SetClientID(clientID).
		SetUsername(user).
		SetPassword(pass).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(5 * time.Second).
		SetWill(availabilityTopic, "offline", 0, true)
	c := paho.NewClient(opts)
	tok := c.Connect()
	if !tok.WaitTimeout(30 * time.Second) {
		return nil, fmt.Errorf("mqtt connect timeout")
	}
	if tok.Error() != nil {
		return nil, tok.Error()
	}
	return &PahoPublisher{c: c}, nil
}

// Publish implements Publisher.
func (p *PahoPublisher) Publish(topic string, payload []byte, retain bool) error {
	tok := p.c.Publish(topic, 0, retain, payload)
	tok.WaitTimeout(5 * time.Second)
	return tok.Error()
}
```

- [ ] **Step 4: Run to verify PASS** — `go test ./mqtt/ && go build ./...`

- [ ] **Step 5: Commit**

```bash
git add mqtt/ go.mod go.sum && git commit -m "feat(mqtt): paho adapter with LWT + fault/health entities"
```

---

### Task 10: `rest` — read-only debug server

**Files:**
- Create: `rest/server.go`, `rest/server_test.go`

- [ ] **Step 1: Write the failing test**

`rest/server_test.go`:

```go
package rest

import (
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/utkjmitch/infinid/capture"
	"github.com/utkjmitch/infinid/state"
)

func TestEndpoints(t *testing.T) {
	st := state.New()
	rec := capture.New(8, nil)
	rec.Add(capture.Record{TS: time.Now(), Src: 0x5201, Dst: 0x2001, Op: 0x06,
		Data: []byte{0x00, 0x03, 0x03, 0x01}})
	journal := filepath.Join(t.TempDir(), "events.jsonl")
	os.WriteFile(journal, []byte(`{"ts":"2026-08-13T12:00:00Z","type":"daemon_start"}`+"\n"), 0o644)

	stats := func() map[string]any { return map[string]any{"frames": 42} }
	srv := httptest.NewServer(Handler(st, rec, journal, stats))
	defer srv.Close()

	for path, wantIn := range map[string]string{
		"/status": "frames",
		"/state":  "zones",
		"/frames": "5201",
		"/events": "daemon_start",
	} {
		resp, err := srv.Client().Get(srv.URL + path)
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("%s: %v %v", path, err, resp)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatalf("%s read: %v", path, err)
		}
		if !strings.Contains(string(body), wantIn) {
			t.Errorf("%s missing %q: %s", path, wantIn, body)
		}
	}
}
```

- [ ] **Step 2: Run to verify FAIL** — `go test ./rest/`

- [ ] **Step 3: Write implementation**

`rest/server.go`:

```go
// Package rest serves the read-only debug surface: daemon status, assembled
// state, the raw-frame ring, and the event journal (the technician export).
// No auth — bind localhost or a container network, never the open LAN.
package rest

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/utkjmitch/infinid/capture"
	"github.com/utkjmitch/infinid/state"
)

// Handler builds the debug mux. statusFn supplies daemon counters (frames,
// CRC failures, unknown frames — owned by main's read loop).
func Handler(st *state.State, rec *capture.Recorder, journalPath string,
	statusFn func() map[string]any) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, statusFn())
	})

	mux.HandleFunc("GET /state", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, st.Snapshot(time.Now()))
	})

	mux.HandleFunc("GET /frames", func(w http.ResponseWriter, r *http.Request) {
		type frame struct {
			TS   time.Time `json:"ts"`
			Src  string    `json:"src"`
			Dst  string    `json:"dst"`
			Op   string    `json:"op"`
			Data string    `json:"data"`
		}
		var out []frame
		for _, rec := range rec.Snapshot() {
			out = append(out, frame{TS: rec.TS,
				Src: fmt.Sprintf("%04x", rec.Src), Dst: fmt.Sprintf("%04x", rec.Dst),
				Op: fmt.Sprintf("%02x", rec.Op), Data: hex.EncodeToString(rec.Data)})
		}
		writeJSON(w, out)
	})

	mux.HandleFunc("GET /events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		f, err := os.Open(journalPath)
		if err != nil {
			http.Error(w, "no journal", http.StatusNotFound)
			return
		}
		defer f.Close()
		buf := make([]byte, 64*1024)
		for {
			n, err := f.Read(buf)
			if n > 0 {
				w.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	})

	return mux
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
```

- [ ] **Step 4: Run to verify PASS** — `go test ./rest/`

- [ ] **Step 5: Commit**

```bash
git add rest/ && git commit -m "feat(rest): read-only debug server — status, state, frames, event journal"
```

---

### Task 11: wire `cmd/infinid` — flags, pipeline, publish loop

**Files:**
- Modify: `cmd/infinid/main.go`

No unit test (integration wiring); the gate is `go build ./...` + `go vet ./...` + the full existing suite staying green. Keep the existing cappedWriter, capture flags, and reconnect loop intact.

- [ ] **Step 1: Extend flags and wiring**

Replace `main()` and `run()` in `cmd/infinid/main.go` with:

```go
func main() {
	serialDev := flag.String("serial", "", "RS-485 serial device (required)")
	capturePath := flag.String("capture", "", "append frames as JSONL to this file (optional)")
	captureMaxMB := flag.Int64("capture-max-mb", 1024, "stop capturing once the file reaches this size")
	ringSize := flag.Int("ring", 4096, "frames kept in the in-memory ring")
	verbose := flag.Bool("verbose", false, "log every frame (default: one stats line per minute)")
	mqttBroker := flag.String("mqtt-broker", "", "MQTT broker URL, e.g. tcp://broker:1883 (empty = MQTT off)")
	mqttUser := flag.String("mqtt-user", "", "MQTT username")
	mqttPassEnv := flag.String("mqtt-pass-env", "INFINID_MQTT_PASS", "env var holding the MQTT password (never a flag — flags leak into process lists)")
	discoveryPrefix := flag.String("mqtt-discovery-prefix", "homeassistant", "HA discovery prefix")
	baseTopic := flag.String("mqtt-base-topic", "infinid", "state topic base")
	zoneNamesFlag := flag.String("zone-names", "", "comma-separated zone names by index, e.g. bedrooms,living_room,basement")
	samEnabled := flag.Bool("sam", false, "enable active SAM reads (default: passive-only)")
	journalPath := flag.String("journal", "", "event journal JSONL path (empty = journal off)")
	restAddr := flag.String("rest", "127.0.0.1:8099", "REST debug listen address (empty = REST off)")
	flag.Parse()
	if *serialDev == "" {
		log.Fatal("-serial is required")
	}

	zoneNames := parseZoneNames(*zoneNamesFlag)

	var w *cappedWriter
	if *capturePath != "" {
		f, err := os.OpenFile(*capturePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			log.Fatalf("open capture file: %v", err)
		}
		remain := *captureMaxMB << 20
		if st, err := f.Stat(); err == nil {
			remain -= st.Size()
		}
		w = &cappedWriter{f: f, remain: remain}
		if remain <= 0 {
			w.stopped = true
			log.Printf("capture: file already at size cap, capture disabled")
		}
	}
	var rec *capture.Recorder
	if w != nil {
		rec = capture.New(*ringSize, w)
	} else {
		rec = capture.New(*ringSize, nil)
	}

	st := state.New()

	var journal *state.Journal
	if *journalPath != "" {
		var err error
		journal, err = state.OpenJournal(*journalPath)
		if err != nil {
			log.Fatalf("open journal: %v", err)
		}
	}

	var exporter *mqtt.Exporter
	if *mqttBroker != "" {
		avail := *baseTopic + "/availability"
		pub, err := mqtt.Connect(*mqttBroker, *mqttUser, os.Getenv(*mqttPassEnv), "infinid", avail)
		if err != nil {
			log.Fatalf("mqtt connect: %v", err)
		}
		exporter = mqtt.New(pub, mqtt.Config{
			BaseTopic: *baseTopic, DiscoveryPrefix: *discoveryPrefix,
			ZoneNames: zoneNames, Version: version})
		exporter.PublishAvailability(true)
	}

	d := &daemon{
		rec: rec, st: st, journal: journal, exporter: exporter,
		samEnabled: *samEnabled, verbose: *verbose,
	}

	if *restAddr != "" {
		go func() {
			h := rest.Handler(st, rec, *journalPath, d.statusMap)
			log.Printf("rest: listening on %s", *restAddr)
			if err := http.ListenAndServe(*restAddr, h); err != nil {
				log.Printf("rest: %v", err)
			}
		}()
	}

	go d.publishLoop()

	for {
		if err := d.run(*serialDev); err != nil {
			log.Printf("bus error: %v — reopening in 5s", err)
			time.Sleep(5 * time.Second)
		}
	}
}

const version = "0.2.0"

// parseZoneNames validates entity-id-safe slugs: lowercase a-z, 0-9, _.
func parseZoneNames(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	for _, p := range parts {
		for _, c := range p {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
				log.Fatalf("zone name %q: must be lowercase letters, digits, underscores", p)
			}
		}
		if p == "" {
			log.Fatal("empty zone name in -zone-names")
		}
	}
	return parts
}

// daemon holds the wired pipeline shared between the read loop and the
// publish loop.
type daemon struct {
	rec        *capture.Recorder
	st         *state.State
	journal    *state.Journal
	exporter   *mqtt.Exporter
	samEnabled bool
	verbose    bool

	mu            sync.Mutex
	frames        uint64
	framesWindow  uint64
	unknownFrames uint64
	crcResyncs    uint64
	lastFrame     time.Time
	samFailures   int
	classified    bool
	counters      map[string]float64
}

func (d *daemon) statusMap() map[string]any {
	d.mu.Lock()
	defer d.mu.Unlock()
	return map[string]any{
		"frames":         d.frames,
		"unknown_frames": d.unknownFrames,
		"resync_bytes":   d.crcResyncs,
		"last_frame":     d.lastFrame,
		"sam_failures":   d.samFailures,
		"version":        version,
	}
}

func (d *daemon) run(device string) error {
	port, err := bus.OpenSerial(device)
	if err != nil {
		return err
	}
	defer port.Close()
	log.Printf("listening on %s (sam=%v)", device, d.samEnabled)

	var sched *sam.Scheduler
	if d.samEnabled {
		sched = sam.New(port, []sam.Target{
			{Reg: [3]byte{0x00, 0x3b, 0x02}, Interval: 10 * time.Second},
			{Reg: [3]byte{0x00, 0x3b, 0x03}, Interval: 10 * time.Second},
			{Reg: [3]byte{0x00, 0x3b, 0x05}, Interval: time.Hour},
			{Reg: [3]byte{0x00, 0x42, 0x02}, Interval: time.Hour},
		})
	}

	dec := bus.NewDecoder(port)
	lastStats := time.Now()
	for {
		f, err := dec.Next()
		if err != nil {
			return err
		}
		now := time.Now()
		if d.verbose {
			log.Printf("frame: %s", f)
		}
		d.rec.Add(capture.Record{TS: now, Src: f.Src, Dst: f.Dst, Op: f.Op, Data: f.Data, Raw: f.Raw})

		readings, ok := protocol.Decode(f, now)
		d.mu.Lock()
		d.frames++
		d.framesWindow++
		d.lastFrame = now
		d.crcResyncs = uint64(dec.Resyncs())
		if !ok {
			d.unknownFrames++
		}
		d.mu.Unlock()

		for _, r := range readings {
			d.st.Apply(r)
			d.trackCounter(r, now)
		}
		if faults, fok := protocol.DecodeFaults(f); fok && d.journal != nil {
			d.journal.NoteFaults(faults, now)
		}
		if d.journal != nil {
			d.journal.NoteDevice(f.Src, now)
		}
		if sched != nil {
			sched.NoteFrame(f, now)
			sched.Tick(now) // between frames = the natural inter-frame gap
			d.mu.Lock()
			d.samFailures = sched.Failures()
			d.mu.Unlock()
		}

		if !d.verbose && time.Since(lastStats) >= time.Minute {
			d.mu.Lock()
			log.Printf("stats: %d frames this interval, %d unknown total, %d resync bytes",
				d.framesWindow, d.unknownFrames, dec.Resyncs())
			d.framesWindow = 0
			d.mu.Unlock()
			lastStats = time.Now()
		}
	}
}

// trackCounter feeds power-on counters to the journal: reference values for
// outage classification, and the one-shot gap classification at startup.
func (d *daemon) trackCounter(r protocol.Reading, now time.Time) {
	if d.journal == nil {
		return
	}
	if r.Field != "idu_power_cycles" && r.Field != "odu_power_cycles" {
		return
	}
	d.mu.Lock()
	if d.counters == nil {
		d.counters = map[string]float64{}
	}
	d.counters[r.Field] = r.Value
	both := len(d.counters) == 2
	classified := d.classified
	vals := map[string]float64{}
	for k, v := range d.counters {
		vals[k] = v
	}
	if both && !classified {
		d.classified = true
	}
	d.mu.Unlock()
	if both && !classified {
		d.journal.ClassifyGap(vals, now)
	}
	if both {
		d.journal.NotePowerCounters(vals, now)
	}
}

// publishLoop pushes snapshots to MQTT once a second and maintains
// availability from bus liveness.
func (d *daemon) publishLoop() {
	if d.exporter == nil && d.journal == nil {
		return
	}
	var lastFrames uint64
	var lastCount time.Time
	fpm := 0.0
	for range time.Tick(time.Second) {
		now := time.Now()
		d.mu.Lock()
		frames := d.frames
		last := d.lastFrame
		unknown := d.unknownFrames
		samFail := d.samFailures
		d.mu.Unlock()

		if d.journal != nil {
			d.journal.CheckLiveness(now, nil)
		}
		if d.exporter == nil {
			continue
		}

		if lastCount.IsZero() || now.Sub(lastCount) >= time.Minute {
			if !lastCount.IsZero() {
				fpm = float64(frames-lastFrames) / now.Sub(lastCount).Minutes()
			}
			lastFrames, lastCount = frames, now
		}

		snap := d.st.Snapshot(now)
		zones := make([]int, 0, len(snap.Zones))
		for z := range snap.Zones {
			zones = append(zones, z)
		}
		d.exporter.PublishDiscovery(zones)
		d.exporter.PublishState(snap, now)

		busOnline := !last.IsZero() && now.Sub(last) < 60*time.Second
		d.exporter.PublishAvailability(busOnline)

		h := mqtt.Health{FramesPerMin: fpm, UnknownFrames: float64(unknown),
			SAMFailures: float64(samFail), BusOnline: busOnline}
		if d.journal != nil {
			active := d.journal.ActiveFaults()
			h.FaultActive = len(active) > 0
			h.FaultCount = float64(len(active))
			if len(active) > 0 {
				f := active[0]
				h.LastFault = fmt.Sprintf("%d @ %s (x%d)", f.Code,
					f.Time.Format("2006-01-02 15:04"), f.Count)
			}
		}
		d.exporter.PublishHealth(h, now)
	}
}
```

Imports to add: `"fmt"`, `"net/http"`, `"strings"`, `"sync"`, and the module packages `protocol`, `state`, `sam`, `mqtt`, `rest`.

- [ ] **Step 2: Build + vet + full suite**

```bash
cd /d/Dev/Personal/infinid && export PATH="$PATH:/c/Users/jimmy/.go-toolchain/go/bin" && gofmt -l . && go vet ./... && go test ./... -race
```
Expected: gofmt prints nothing; vet clean; all packages PASS.

- [ ] **Step 3: Commit**

```bash
git add cmd/ && git commit -m "feat(cmd): wire protocol→state→{mqtt,rest} pipeline, SAM scheduler, journal"
```

---

### Task 12: `docs/MQTT-CONTRACT.md`

**Files:**
- Create: `docs/MQTT-CONTRACT.md`

- [ ] **Step 1: Write the contract doc**

Full content:

```markdown
# infinid MQTT Contract

Additive-only: entities may be added in any release; the ids below are
never renamed or removed. Consumers may bind to any id here.

## Availability

`infinid/availability` (retained): `online` / `offline`. Offline means the
daemon is down **or the bus has been silent for 60 s** — consumers must
treat entities as unavailable, never stale-data-as-fresh. Set as the LWT.

## Frozen diagnostic ids (the dashboard contract)

| Entity | Source register | Notes |
|---|---|---|
| `sensor.infinid_compressor_stage` | 00060E[0] | 0=off, 1-5 |
| `sensor.infinid_compressor_rpm` | 000604[2..3] | actual |
| `sensor.infinid_supply_cfm` | 000413[0..1] | measured |
| `sensor.infinid_blower_rpm` | 000413[2..3] | |
| `sensor.infinid_static_pressure` | 000413[4..7] | inH2O |
| `sensor.infinid_blower_watts` | 000413[8..11] | W |
| `sensor.infinid_suction_pressure` | 000303[2..3] | psi |
| `sensor.infinid_outdoor_coil_temp` | 000302@ODU id 0x12 | °F |
| `sensor.infinid_discharge_temp` | 000302@ODU id 0x45 | °F |
| `sensor.infinid_damper_<zone>` | 000319 per slot | % open (raw/15×100) |

`<zone>` comes from the `zone_names` config (index-ordered slugs); unset
indexes name themselves `zone_<n>`.

## Per-zone entities

`sensor.infinid_zone_<zone>_{temp,humidity,cool_setpoint,heat_setpoint,fan_mode,hold,hold_remaining}`
— passive sources cover sensor-equipped zones; the wall control's own zone
fills in only when SAM reads are enabled (`sam: true`).

## System / equipment (additive)

`sensor.infinid_{outdoor_temp,supply_air_temp,suction_temp,superheat,line_voltage,system_mode}`
and `sensor.infinid_filter_life` (**remaining %** — the bus reports consumed %,
inverted at the contract boundary; REST `/state` shows the raw used value)
plus runtime counters
`sensor.infinid_{heat_stage1,heat_stage2,blower,cool}_{cycles,hours}` and
`sensor.infinid_{idu,odu}_power_cycles` (state_class total_increasing —
long-term statistics candidates).

## Faults & health

`sensor.infinid_last_fault` (text: `<code> @ <timestamp> (x<count>)`),
`sensor.infinid_fault_count`, `binary_sensor.infinid_fault_active`,
`binary_sensor.infinid_bus_online`,
`sensor.infinid_{frames_per_min,unknown_frames,sam_failures}`.
The full event journal (fault lifecycle, outage classification, device
liveness) is not an MQTT surface: `GET /events` on the REST port, or the
journal JSONL file itself.

**REST invariant:** the REST surface is read-only forever. If writes ever
exist (v2), they arrive via MQTT command topics behind broker auth — never
REST. This is what makes the unauthenticated debug port acceptable on a
trusted LAN; do not add POST/PUT handlers.

## Devices

One `infinid` hub device (diagnostics, counters, health) + one device per
zone (`infinid_zone_<n>`, via_device → hub).

## Staleness

State topics are retained; stale fields (per-field horizon exceeded)
publish `None` so HA shows unknown. Everything re-publishes on a 60 s
heartbeat.
```

- [ ] **Step 2: Commit**

```bash
git add docs/MQTT-CONTRACT.md && git commit -m "docs: MQTT contract — frozen ids, additive rule, staleness semantics"
```

---

### Task 13: HAOS add-on v0.2.0 — options schema

**Files:**
- Modify: `deploy/haos-addon/config.yaml`, `deploy/haos-addon/run.sh`

- [ ] **Step 1: Update config.yaml**

```yaml
name: infinid
version: "0.2.0"
slug: infinid
description: "Carrier Infinity ABCD bus daemon — local decode + MQTT discovery (read-only)"
arch:
  - aarch64
startup: services
boot: auto
uart: true
map:
  - share:rw
ports:
  8099/tcp: 8099
ports_description:
  8099/tcp: "REST debug/export (read-only, UNAUTHENTICATED — blank the host port here to keep it container-internal; never port-forward it)"
options:
  serial: "/dev/serial/by-id/CHANGE-ME"
  mqtt_broker: ""
  mqtt_user: ""
  mqtt_password: ""
  zone_names: ""
  sam_reads: false
  capture: true
schema:
  serial: str
  mqtt_broker: str
  mqtt_user: str
  mqtt_password: password
  zone_names: str
  sam_reads: bool
  capture: bool
```

- [ ] **Step 2: Update run.sh**

```sh
#!/bin/sh
set -e
CONFIG=/data/options.json
SERIAL=$(jq -r '.serial' "$CONFIG")
MQTT_BROKER=$(jq -r '.mqtt_broker' "$CONFIG")
MQTT_USER=$(jq -r '.mqtt_user' "$CONFIG")
ZONE_NAMES=$(jq -r '.zone_names' "$CONFIG")
SAM=$(jq -r '.sam_reads' "$CONFIG")
CAPTURE=$(jq -r '.capture' "$CONFIG")

mkdir -p /share/infinid
set -- -serial "$SERIAL" \
  -journal /share/infinid/events.jsonl \
  -rest ":8099"
[ "$CAPTURE" = "true" ] && set -- "$@" -capture /share/infinid/capture.jsonl
[ -n "$MQTT_BROKER" ] && set -- "$@" -mqtt-broker "$MQTT_BROKER" -mqtt-user "$MQTT_USER"
[ -n "$ZONE_NAMES" ] && set -- "$@" -zone-names "$ZONE_NAMES"
[ "$SAM" = "true" ] && set -- "$@" -sam

# Password travels via env, never argv (visible in process lists).
INFINID_MQTT_PASS=$(jq -r '.mqtt_password' "$CONFIG")
export INFINID_MQTT_PASS

exec /infinid "$@"
```

Check the Dockerfile installs `jq` in the runtime stage (`apk add --no-cache jq` alongside whatever it already installs); add it if missing. Do NOT bump `INFINID_REF` in this task — that happens at deploy time (Task 16) when the final commit SHA exists, together with this version bump (Supervisor caches by version; the two move in lockstep).

- [ ] **Step 3: Commit**

```bash
git add deploy/ && git commit -m "feat(addon): v0.2.0 options — MQTT, zone names, SAM toggle (default off)"
```

---

### Task 14: community guide — `docs/guide/00..05`

**Files:**
- Create: `docs/guide/00-overview.md`, `01-equipment.md`, `02-tapping-the-bus.md`, `03-first-capture.md`, `04-decode-workflow.md`, `05-contributing.md`

Write all six files. Rules that are NOT negotiable (spec §6): no original wiring instructions anywhere — link out; "what the author used" framing for hardware, no endorsements; the safety/liability block below appears verbatim in `00-overview.md`; plain prose, no AI-isms, no marketing voice. Each file under ~120 lines.

- [ ] **Step 1: Write `00-overview.md`**

Must open with the project's one-paragraph purpose (local, read-only decoding of Carrier Infinity / Bryant Evolution systems), then this block verbatim:

```markdown
## Read this first

This project provides **software and a method — not electrical instruction.**

- **Nothing here is professional HVAC, electrical, or safety advice.**
- Your HVAC equipment is your responsibility. This software is MIT-licensed
  and comes with **no warranty of any kind.**
- **Kill power at the breaker** before opening equipment or touching any
  wiring. The ABCD bus is low-voltage, but it lives inside equipment that
  also carries line voltage.
- If any physical step is unfamiliar, **stop and hire a professional.**
  An HVAC tech can land two wires on a terminal block in minutes.
- Everything here is **read-only**. infinid v0.x cannot write to your
  equipment: no command topics exist, and the only optional transmission
  (SAM reads) requests data and nothing else. Start in passive-only mode
  (the default) and stay there until your decode is verified.
```

Then: what you get at the end (HA entities, an event journal, a decode workbench), the journey map (equipment → tap → capture → verify → decode → contribute) with links to guides 01-05, and the honest framing that register layouts vary by firmware generation — this project decodes only what is verified, and your system may need its own verification pass (link `docs/protocol-tables.md` and ADR-0001).

- [ ] **Step 2: Write `01-equipment.md`**

Sections: **What the author used** (a DSD TECH SH-U11 USB RS-485 adapter and a Raspberry Pi 5 running Home Assistant OS — stated as fact, explicitly "not an endorsement; it is simply what worked on one system"); **what matters when choosing** (RS-485 half-duplex with A/B terminals, FTDI or CH340 chips both fine, ~$10-15 range; any Linux box works — the daemon is one static binary); **search terms** ("USB RS-485 adapter"); **what you do NOT need** (no SAM hardware, no cloud account, no Infinitude proxy, no soldering).

- [ ] **Step 3: Write `02-tapping-the-bus.md`**

Sections: **What a tap is** — the ABCD bus is four low-voltage wires (A/B data, C/D power) daisy-chained between the wall control and equipment; a tap lands two wires (A and B) on the same terminals, adding a listener, changing nothing electrically. **Where people tap** — at the air handler / damper control terminal strip. **We do not provide wiring instructions** — link to the Infinitude wiki's physical-connection page (`https://github.com/nebulous/infinitude/wiki`) and to general community resources; repeat the hire-a-professional line from 00. **What success looks like** — A/B landed, breaker back on, system running normally, dongle showing on the host (`ls /dev/serial/by-id/`).

- [ ] **Step 4: Write `03-first-capture.md`**

Sections: install (build from source `go build ./cmd/infinid`, or the HAOS local add-on in `deploy/haos-addon/` — copy to `/addons/infinid`, set the `serial` option to your by-id path); first run (`infinid -serial /dev/serial/by-id/<yours> -capture capture.jsonl -verbose`); **what healthy looks like** (a steady stream, hundreds to ~1,500 frames/min on a zoned Touch system, resync bytes near zero after startup); **what unhealthy looks like** (zero frames → check A/B swap — reversed polarity reads nothing; constant resyncs → loose connection); the capture file is JSONL, one frame per line, and it is the raw material for everything that follows. Close with the REST note: the debug port (8099) is read-only but **unauthenticated** — fine on a trusted LAN, disable the host mapping in the add-on Network panel if unsure, and never port-forward it to the internet.

- [ ] **Step 5: Write `04-decode-workflow.md`**

The labeled-experiment method, presented as the method that produced this repo's verified map: (1) capture continuously; (2) change exactly ONE thing at the panel and write down the wall-clock time (bus writes lead panel labels by ~15-25 s); (3) run `businspect` lenses over the window — `tables` (what registers exist), `timeline <owner> <reg>` (how a register's payload evolved), `diff <RFC3339 time>` (what changed around your labeled instant); (4) form a byte-layout hypothesis and verify it against a *second* independent ground truth (panel display, a known temperature, arithmetic like superheat = suction temp − saturation); (5) only then call it decoded — and read `docs/adr/0001-verified-only-decoders.md` for why hypotheses never ship as decoders. Include the real worked example: how the timed-hold countdown was pinned (0x4BD2 = 19410 ticks = 647 min = exactly the minutes to 8:00 PM — two independent confirmations).

- [ ] **Step 6: Write `05-contributing.md`**

What to contribute: unknown-register observations (`sensor.infinid_unknown_frames` counting up is your map of undecoded traffic), verified layouts (the evidence bar: byte-exact against ground truth, documented like the verification sections of `docs/protocol-tables.md`), and equipment-generation notes (address anomalies, register drift). **Sanitizing captures**: frames carry no credentials or personal data, but captures reveal usage patterns (when you're home, hold schedules) — trim to the experiment window before sharing. File issues/PRs with the capture excerpt, the labeled timestamp, and the ground truth observed.

- [ ] **Step 7: Commit**

```bash
git add docs/guide/ && git commit -m "docs(guide): decode-your-own-system series — equipment through contribution"
```

---

### Task 15: agent skill — `skills/decode-your-infinity-bus/SKILL.md`

**Files:**
- Create: `skills/decode-your-infinity-bus/SKILL.md`

- [ ] **Step 1: Write the skill**

Full content (open Agent Skills format — plain markdown + frontmatter, consumable by any agent):

```markdown
---
name: decode-your-infinity-bus
description: Walk a user from zero to safely decoding their own Carrier
  Infinity / Bryant Evolution HVAC bus with infinid — equipment selection,
  bus tap (via linked community guides, never original wiring instruction),
  first capture, verification, decode workflow, and contribution. Staged
  with checkpoints; resumable at any stage.
---

# Decode Your Infinity Bus

You are guiding a user through the infinid journey documented in
`docs/guide/` (files 00-05). Read those files; they are the content — this
skill is the walkthrough discipline.

## Hard rules (non-negotiable, enforce throughout)

1. **Read-only, always.** Never suggest register writes, timing changes, or
   "try sending X" experiments. infinid v0.x has no write path; do not help
   the user build one.
2. **Never generate physical wiring instructions.** For anything involving
   opening equipment or touching wires, point to `docs/guide/02` and its
   linked community guides, and recommend a professional whenever the user
   sounds unsure. Do not improvise diagrams, terminal names, or wire colors.
3. **Passive-only first.** Do not suggest enabling SAM reads (`sam: true` /
   `-sam`) until the user's passive decode is verified against their real
   system (stage E complete).
4. **Safety framing travels with you.** Before any stage that touches
   hardware, restate: breaker off, low-voltage bus inside line-voltage
   equipment, no warranty, professional if unfamiliar.
5. **Verify before advancing.** Each stage has an exit criterion. Do not
   move on until it is met — a wrong foundation wastes every later step.

## Stages

Ask the user where they are, then start at that stage.

- **A. Orientation** — user reads `docs/guide/00`. Exit: they can say what
  read-only means here and what the end state is.
- **B. Equipment** — `docs/guide/01`. Exit: user has an RS-485 adapter and
  a Linux host.
- **C. Tap** — `docs/guide/02` and its links only. Exit: adapter visible at
  `/dev/serial/by-id/`, system running normally afterward.
- **D. First capture** — `docs/guide/03`. Exit: sustained frames at a
  plausible rate (hundreds+/min) with near-zero resyncs. Zero frames →
  check A/B polarity before anything else.
- **E. Verify decode** — run the daemon, compare decoded entities against
  the wall control's own display (temps, setpoints, mode). Exit: values
  track through *change* (adjust a setpoint at the panel, watch the entity
  follow), not just at rest. Registers that don't decode on their firmware
  are archived, not broken — see `docs/adr/0001`.
- **F. Decode workflow** — `docs/guide/04` for anything their system shows
  that infinid doesn't decode. One labeled change at a time; a layout is
  real only with two independent ground-truth confirmations.
- **G. Contribute** — `docs/guide/05`: trimmed captures, evidence chains,
  layout PRs.

## Failure triage

- Zero frames: A/B swapped (most common), wrong device path, tap not landed.
- Constant resyncs: loose connection or wrong baud assumption (bus is
  38400 8N1 — fixed in infinid, so resyncs mean physical issues).
- Entities `unavailable`: check `infinid/availability` on the broker, then
  the add-on log. Bus silence 60 s+ flips availability off by design.
- Values look wrong: different firmware generation — do NOT assume the
  repo's layouts; run stage F and contribute the difference.
```

- [ ] **Step 2: Commit**

```bash
git add skills/ && git commit -m "feat(skill): decode-your-infinity-bus — staged agent walkthrough with safety rails"
```

---

### Task 16: README, final sweep, deploy prep

**Files:**
- Modify: `README.md` (add Phase 2 sections), `deploy/haos-addon/Dockerfile` (INFINID_REF — deploy step only)

- [ ] **Step 1: Update README.md**

Add/replace sections (keep existing credits + Phase 1 content): **What you get** (HA MQTT-discovery entities, event journal with outage classification, REST debug, read-only guarantee + passive-only default); **Quick start** (bare Linux one-liner with the new flags, HAOS add-on pointer); **Decode your own system** — link `docs/guide/00-overview.md` and the skill (`skills/decode-your-infinity-bus/`); **Contract** — link `docs/MQTT-CONTRACT.md`; note ADR-0001 verified-only policy with a link.

- [ ] **Step 2: Full verification sweep**

```bash
cd /d/Dev/Personal/infinid && export PATH="$PATH:/c/Users/jimmy/.go-toolchain/go/bin" && gofmt -l . && go vet ./... && go test ./... -race && go build ./...
```
Expected: gofmt silent, vet clean, all tests PASS, build clean.

- [ ] **Step 3: Commit**

```bash
git add README.md && git commit -m "docs: README — Phase 2 surface, guide + skill links, contract pointer"
```

- [ ] **Step 4 (orchestrator, at deploy time — not a subagent step):** bump `INFINID_REF` in `deploy/haos-addon/Dockerfile` to the final Phase 2 SHA in the same commit as any further `version` bump; push; rebuild the add-on on the HA host; configure `mqtt_*` options from host-held credentials (never in-repo), `zone_names: bedrooms,living_room,basement`, `sam_reads: false` initially.

---

## Post-plan validation (orchestrator-run, live system required)

Not subagent tasks — these need the live bus, host credentials, or the private hunterhill repo:

1. **Passive validation**: create a dedicated `infinid` user on the operator's Mosquitto broker (per-service credentials, macfactory pattern; password lives host-side only — for Hunterhill: `C:\Users\jimmy\.hunterhill\mqtt-infinid.txt` — and is entered once into add-on options, never a repo). Deploy add-on v0.2.0 (SAM off), confirm the 12 contract entities + zone 2/3 entities appear in HA and track the wall panel through a setpoint change. Dashboard HvacPage DiagTiles light up with zero app changes.
2. **SAM live gate**: enable `sam_reads: true`; verify 3B02/3B03/3B05/4202 replies decode against ground truth (ha_carrier entities for bedrooms setpoint + filter %; the panel event log 171/172 for faults). Capture the real replies, cut them into `protocol/testdata/` fixtures, tighten the Task 4 synthetic tests into golden tests, adjust layouts if live bytes disagree (targeted-then-bounded-sweep per spec §2 if candidates fail).
3. **Comparator (hunterhill repo, private)**: HA template package computing infinid-vs-ha_carrier deltas per zone; runs for the validation window, retires at cutover.
4. **Journal verification**: restart the add-on → `monitoring_gap` classified; flip the HVAC breaker off/on briefly (Jimmy's call, not automated) → `hvac_power_loss`.

## Out of scope

Writes of any kind; MQTT climate entities; schedules/vacation decode; 0x1E alarm payload decode (experimental target, archive-only for now); guide translations.
```
