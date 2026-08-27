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
3. **Passive-only first.** Do not suggest enabling SAM reads
   (`sam_reads: true` / `-sam`) until the user's passive decode is verified
   against their real system (stage E complete).
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
  are archived, not broken — see `docs/adr/0001-verified-only-decoders.md`.
- **F. Decode workflow** — `docs/guide/04` for anything their system shows
  that infinid doesn't decode. One labeled change at a time; a layout is
  real only with two independent ground-truth confirmations.
- **G. Contribute** — `docs/guide/05`: trimmed captures, evidence chains,
  layout PRs.

## Failure triage

- Zero frames: A/B swapped (most common), wrong device path, tap not landed.
- Constant resyncs: loose connection or wrong baud assumption (bus is
  38400 8N1 — fixed in infinid, so resyncs mean physical issues).
- Frame rate a fraction of normal AND resyncs climbing steadily: a second
  process has the same serial device open and is stealing bytes — find and
  remove it (one device, one reader; see
  `docs/experiments/2026-08-27-shared-port-incident.md`).
- Entities `unavailable`: check `infinid/availability` on the broker, then
  the add-on log. Bus silence 60 s+ flips availability off by design.
- Values look wrong: different firmware generation — do NOT assume the
  repo's layouts; run stage F and contribute the difference.
