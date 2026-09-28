# infinid v2 — Write Path Design

**Date:** 2026-08-28 · **Status:** approved (brainstorm w/ utkjmitch) · **Depends on:** Phase 2 (read/decode/publish) merged + deployed; first live SAM reads captured ([experiments/2026-08-28-first-sam-reads.md](../../experiments/2026-08-28-first-sam-reads.md))

Adds the ability to command a Carrier Infinity / Bryant Evolution system from
Home Assistant — setpoints, hold, mode, fan — by transmitting on the ABCD bus
as the SAM (0x92). This is the §D7-phase-b work: v1 proved local decode; v2
closes the loop and unlocks retiring the ha_carrier cloud integration.

The whole design is organized around one non-negotiable: **v1's safety
property must not erode.** Today the `sam` package can construct only
`OpRead` frames, by construction, so the binary is physically incapable of
writing. v2 adds a write path without weakening that — the read package is
untouched, and the write capability lives behind hard rails in a separate,
default-off package.

## Evidence this rests on (2026-08-28 live SAM reads)

- 3B03 (zone settings) reads back **all-zero** on this unit, from a clean
  well-behaved SAM — reproducing the infinitive-incident zeros, so it is
  real firmware behavior, not a byte-starvation artifact.
- **Working hypothesis (unconfirmed):** 3B03 is the register a SAM *writes*
  to control the system; the zero read-back is "no SAM has claimed control
  yet." This matches infinitive/InfinitESP, which set setpoints by writing
  these registers.
- **Consequence:** reading 3B03 back cannot verify a write. Write
  verification comes from the passive `00041x` broadcasts the wall control
  emits for its own display, plus the panel — the same independent-ground-
  truth rule the decode workflow already uses.
- The wall control accepted every SAM read with **zero NACKs**. Whether it
  requires a SAM registration/announce frame before honoring a *write* is
  the first open question the write gate must answer, in dry-run.
- The 4202 fault decode is **misaligned against real bytes** (decoded a
  fault as year 2103). Fixing it is a prerequisite: an untrustworthy fault
  surface undermines the "watch the fault table during writes" safety step.

## Scope

**In:** per-zone cool/heat setpoints, hold (set/clear, timed + permanent),
system mode, fan mode — everything the Apple Home Climate Bridge repoint
needs. One coherent design; **implementation is staged** (setpoint round-trip
must pass before mode/fan work begins — see Staging).

**Out (v2):** schedule/vacation editing; writing any register other than
3B03 (system-mode/fan may live in a different register — resolved during
implementation, but the allowlist expands one verified register at a time);
humidifier/ventilator control; multi-SAM coordination; non-Carrier systems.

## Architecture — one new package, hard boundaries

| Package | Change | Safety role |
|---|---|---|
| `sam` (existing) | **untouched** | Keeps its by-construction OpRead-only guarantee. The read scheduler is unchanged. |
| `samctl` (new) | the only code that can construct `OpWrite` | All write rails live here. Cannot be bypassed — nothing else in the tree encodes a write frame. |
| `mqtt` | add `climate` discovery + inbound command subscription | First inbound path into the daemon. Commands in, validated, handed to `samctl`. |
| `state` | add commanded-vs-observed reconciliation | Tracks "we asked for X; the bus now shows X" for the climate entity's reported state. |
| `cmd/infinid` | wire command topics → validate → `samctl` → bus | New `-write` flag (default off), layered on `-sam`. |

Command flow: `MQTT command topic → mqtt (parse) → state (validate against
current snapshot) → samctl (encode, clamp, compose-from-Write-Baseline) → bus`. Writes never flow
through REST — REST stays read-only forever (the v1 invariant that makes the
unauthenticated debug port acceptable).

## `samctl` rails (the heart of the design)

Every one of these is enforced in code, not documentation:

1. **Register allowlist.** A closed set, 3B03-only at first. `samctl` cannot
   encode a write to any register not on the list — the allowlist is the
   input to the encoder, not a check the caller may skip.
2. **Write Baseline, mandatory.** A write composes the full 3B03 payload
   from the current passive-observed state of every zone (the `00041x`
   registers v1 already decodes), changing only the target zone's target
   field. 3B03 read-back is explicitly **disqualified** as a baseline
   source: on this unit it reads all-zero (see Evidence), so "RMW against
   the last read" would construct the payload from zeros — the exact
   failure this rail exists to prevent. If any zone's observed state is
   stale or unknown, the write is refused — no complete observed picture,
   no write. Stage-2 dry-run must additionally confirm the composed
   payload's *unchanged-zone* bytes match SAM prior art
   (infinitive/InfinitESP) before first transmission. (Glossary: **Write
   Baseline** in CONTEXT.md, resolved 2026-09-11.)
3. **Value clamps.** Setpoints bounded to a sane envelope (**55–90 °F**,
   heat < cool with a minimum deadband); mode/fan ∈ the known decoded enums.
   Out-of-range commands are rejected with a logged reason, never clamped
   silently into a surprising value.
4. **Rate limiting.** Minimum interval between writes per zone; a burst of HA
   commands coalesces to the latest intended state (the Task 7 airtime
   discipline, extended to writes).
5. **Dry-run mode.** Encodes and logs the exact frame it *would* transmit,
   and returns without touching the port. This is how the SAM-registration
   question and the 3B03 byte layout get verified before a single real
   write. Dry-run is the default sub-state of `-write` until explicitly
   armed.
6. **Default-off, two-key.** Writes require BOTH `-sam` (transmit at all) and
   `-write` (construct write frames). Neither alone can emit a write.

## Write verification (no read-back)

Because 3B03 read-back may just echo, a completed write is confirmed by
watching the wall control's own passive broadcasts:

- Setpoint/hold writes → confirmed against the `00041x` zone registers the
  wall control emits for its display (the same registers v1 already decodes
  passively).
- The `state` package reconciles commanded vs observed: the climate entity
  reports `commanded` optimistically, then settles to `observed` once the
  passive stream confirms (or flags a divergence if it never does).
- A write that never shows up in the passive stream within a timeout is a
  **failed** write, surfaced (health entity + log), not a silent success —
  the macfactorybox staleness lesson applied to the write direction.

## HA surface

One MQTT `climate` entity per zone via discovery: `target_temperature` /
`target_temp_low` + `target_temp_high` (mode-dependent), `hvac_mode`,
`fan_mode`, `hvac_action`, current temp/humidity from existing sensors. The
v1 diagnostic sensors stay. Command topics per the HA MQTT climate spec;
`samctl` is the only consumer of the parsed commands.

Discovery for the climate entities is additive to the frozen MQTT contract —
new ids, nothing renamed. The contract doc gains a "Commandable entities"
section marked v2.

## Staging (implementation order; each gate blocks the next)

1. **Fix 4202 fault decode** against the 2026-08-28 capture — re-derive the
   slot layout, cut a real golden fixture, make the fault surface
   trustworthy. (Prerequisite: writes are watched via the fault table.)
2. **`samctl` + dry-run.** Build the package and its rails; wire `-write`
   (dry-run only). Dry-run a 3B03 setpoint change; confirm the encoded
   frame byte-for-byte against the decoded read, and determine whether a SAM
   registration/announce frame is needed first. No transmission of writes.
3. **Live setpoint round-trip, one zone.** Arm writes; change one zone's
   setpoint; confirm via panel + passive `00041x`; revert. Capture running,
   fault table watched (fault-146-incident discipline: one frame at a time).
   Repeat per zone.
4. **Hold, then mode, then fan.** Each as its own verified round-trip; expand
   the allowlist only as each register/field is confirmed live.
5. **Cutover.** Repoint the Apple Home Climate Bridge from ha_carrier to
   infinid's climate entities; retire ha_carrier; the comparator automation
   retires with it. (Cutover mechanics live in the private hunterhill repo.)

## Testing

- **`samctl` safety sweep** (mirrors the Task 7 OpRead sweep): a seeded run
  over many commands asserting every emitted frame is (a) op-write, (b) to an
  allowlisted register, (c) composed from a complete, fresh Write Baseline
  (never from a zero, stale, or partial picture), (d) within clamps.
  Mutation-tested — inject a rogue out-of-allowlist write, or one composed
  with a missing/stale zone, and prove the sweep catches it.
- **Round-trip encode/decode**: a value written then decoded returns the
  input (property test), against real 3B03 bytes once captured.
- **Clamp/reject tests**: out-of-range setpoints, heat≥cool, missing
  baseline, unknown mode — all rejected with reasons.
- **Command-flow integration**: MQTT command → validated → dry-run frame,
  asserted end-to-end with a fake publisher and a fake port.
- Full existing suite stays green; CI keeps `-race`.

## Risks

- **SAM registration unknown.** The wall control may ignore or fault a write
  from an unannounced SAM. Mitigated by dry-run + staged live gate; if
  registration is needed, it becomes a stage-2 sub-task (announce frame
  decoded from infinitive/InfinitESP prior art, verified passively).
- **3B03-write hypothesis wrong.** If setpoints live in a different register,
  stage 2's dry-run reveals it before any transmission; the allowlist model
  absorbs the correction (swap the allowlisted register).
- **Firmware variance.** Other people's units may differ; v2 ships the rails
  and the method, marks live-write community-validated-needed, and never
  assumes a layout the operator hasn't confirmed on their own system.
- **Write storms from HA automations.** Rate limiting + coalescing bound bus
  airtime; a misbehaving automation cannot flood the bus.

## Out of scope (restated)

Schedule/vacation editing; non-3B03 registers until each is separately
verified; multi-SAM coordination; HomeKit (rides the HA Climate Bridge);
writes on any system the operator has not validated the decode on first.
