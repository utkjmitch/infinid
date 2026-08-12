# businspect Capture-Analysis Workbench Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A CLI that turns `capture.jsonl` into decode leads: register inventory, change timelines, event diffs, and a faults view — the tool that makes the labeled mode-test session productive.

**Architecture:** One new library package `inspect` (JSONL parsing, register keying, grouping/diff logic — fully unit-tested) plus a thin `cmd/businspect` CLI with four subcommands (`tables`, `timeline`, `diff`, `alarms`). Stdlib only. Reads a file via `-in` or stdin, so `ssh root@pi cat /share/infinid/capture.jsonl | businspect tables` works.

**Tech Stack:** Go 1.25, stdlib only. Reference: `docs/protocol-tables.md` (register semantics, all unverified), capture JSONL schema from `capture/recorder.go` (`ts` RFC3339Nano, `src`/`dst` 4-hex, `op` 2-hex, `data`/`raw` hex).

**Register keying:** For ops 0x0b (READ), 0x0c (WRITE), 0x06 (ACK06) with `len(Data) >= 3`, the register is `hex(Data[0:3])` (e.g. `000306`). The register OWNER is `Src` for ACK06 replies and `Dst` for READ/WRITE. Frames with shorter data (bare ACKs) group under key `-`.

---

### Task 1: `inspect` package — parse, group, diff

**Files:**
- Create: `inspect/inspect.go`
- Test: `inspect/inspect_test.go`

- [x] **Step 1: Tests first.** Fixture = a JSONL string built in the test from ~10 hand-written records (use the real schema; include: two ACK06 payload versions of register `000306` from src `3e01` at distinct timestamps, a READ poll pair for cadence, one op `1e` frame, one bare `ACK06` with 1-byte data, one malformed line). Test:
  - `ParseAll(r io.Reader) ([]Rec, int, error)` returns records in order, skipped-line count = 1, fields parsed (ts/src/dst/op/data bytes).
  - `Key(r Rec) (reg string, owner uint16, ok bool)` — ACK06 → (`000306`, src, true); READ → (reg, dst, true); 1-byte ACK06 → ok=false.
  - `Group(recs) map[GroupKey]*Stats` where `GroupKey{Owner uint16; Reg string}` and `Stats{Count int; First, Last time.Time; PayloadLen int; Distinct [][]byte; ChangedOffsets []int}` — Distinct holds payloads (post-register bytes, i.e. `Data[3:]`) in first-seen order; ChangedOffsets = byte positions that differ across any pair of Distinct.
  - `Changes(recs, owner, reg) []Change` with `Change{TS time.Time; Payload []byte; ChangedFrom []int}` — one entry per time the payload differs from the previous one for that (owner, reg).
  - `DiffAt(recs, at time.Time, window time.Duration) []RegDiff` with `RegDiff{Owner uint16; Reg string; Before, After []byte; Changed []int}` — per (owner,reg): last payload in `[at-window, at]` vs first in `(at, at+window]`, only where both exist and differ.
- [x] **Step 2: Run, verify failure.** `go test ./inspect/` → undefined symbols.
- [x] **Step 3: Implement.** No logging, no globals; exported API exactly as tested; doc comments on all exported identifiers; `gofmt` clean.
- [x] **Step 4: `go test -run . ./inspect/ -v` → PASS; `go vet ./...` clean.**
- [x] **Step 5: Commit** `feat(inspect): capture parsing, register grouping, change timelines, event diff`.

### Task 2: `cmd/businspect` CLI

**Files:**
- Create: `cmd/businspect/main.go`

- [ ] **Step 1: Implement.** `businspect <tables|timeline|diff|alarms> [flags]`, shared flag `-in` (path, default `-` = stdin).
  - `tables`: one line per (owner, reg) sorted by owner then reg: `owner reg count len distinct changed-offsets first..last`; then a second section listing READ-poll cadence per (owner, reg): average interval from consecutive READ timestamps (skip if <2 polls).
  - `timeline -reg <hex> [-owner <4-hex>]`: chronological `Changes` output: `ts  payload-hex  changed:[offsets]`. If `-owner` omitted and multiple owners answer that reg, print all, prefixed by owner.
  - `diff -at <RFC3339> [-window 2m]`: `DiffAt` output: `owner reg  changed:[offsets]` then indented `- before-hex` / `+ after-hex`. This is the labeled-experiment workhorse — output must make single-byte changes instantly visible.
  - `alarms`: (a) every frame with op `1e` or `15` printed raw with ts; (b) every distinct ACK06 payload of reg `004202` (fault history LASTTEN — layout per docs/protocol-tables.md §faults, UNVERIFIED) decoded best-effort: 10 × 7-byte entries `code src-addr hh:mm days-since-2013-01-01→date status(active,count)`, skipping all-zero entries, labeled `(unverified decode)`; (c) if no alarm/fault data at all, say so.
  - Errors to stderr, exit 1; malformed-line skip count to stderr as a one-line note.
- [ ] **Step 2: Verify.** `go build ./... && go vet ./... && gofmt -l .` clean; smoke: pipe a 3-line fixture through each subcommand.
- [ ] **Step 3: Commit** `feat(businspect): tables/timeline/diff/alarms CLI over capture JSONL`.

---

## Explicitly out of scope

Live-follow mode, plotting, protocol decoding beyond the 4202 best-effort view (Phase 2 owns decoding), any bus access.
