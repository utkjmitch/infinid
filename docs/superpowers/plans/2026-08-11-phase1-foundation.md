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

### Task 1