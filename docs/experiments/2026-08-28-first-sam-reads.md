# First live SAM reads — 2026-08-28

The read live gate: `sam_reads: true` enabled on the deployed v0.2.0 add-on,
capture running, fault table watched. These are infinid's first-ever
transmissions on the bus — all read requests (OpRead to 0x9201 targets), by
construction. Zero writes. Recorded so the v2 write design rests on observed
behavior, not the plan's assumptions.

## What the wall control does with a well-behaved SAM

The scheduler polled 3B02, 3B03, 3B05, and 4202 as the SAM address (0x9201).
Replies (`src 2001 → dst 9201`, op 06):

- **4202 (fault history): real, non-zero data.** A full ~70-byte payload of
  populated fault slots came back. The wall control serves its fault table to
  a SAM read.
- **3B02 (system state): all-zero payload.** Register echoed, data zeroed.
- **3B03 (zone settings): all-zero payload.** Same — register echoed, ~48
  zero bytes.
- **Zero NACKs.** The wall control accepted every read; nothing was refused
  (no op-15). `sam_failures` sat at 8 over ~2000 frames — timeouts on some
  polls, not rejections. Airtime spacing held (the Task 7 scheduler).

The all-zero 3B02/3B03 is the headline. It is **not** the byte-starvation
artifact from the infinitive incident (that theory is now dead): a clean,
correctly-spaced, single-reader SAM gets the same zeros. This is real
firmware behavior on this unit.

## Working hypothesis (label: unconfirmed, but load-bearing for v2)

3B02 and 3B03 are the registers a SAM **writes** to control the system;
reading them returns "what a SAM last wrote," which is zero because no SAM
has ever claimed control here. 4202 is wall-control-owned data (the fault
log), so it reads back real content regardless. This matches prior art —
infinitive and InfinitESP set setpoints/mode by writing these same
registers.

Two consequences for the write design, if the hypothesis holds:

1. Writing a setpoint is writing 3B03 with the zone's bytes — the intended
   SAM function, not a hack. The all-zero baseline is the expected "no SAM
   in control yet" state.
2. **Read-back of 3B03 cannot verify a write** (it may just echo the SAM's
   own last write, or stay zero). Write verification must come from the
   passive `00041x` broadcasts the wall control emits for its own display,
   and from the panel — the same independent-ground-truth discipline the
   decode workflow already uses.

Open: whether the wall control requires a SAM registration/announce frame
before it will honor a 3B03 write (a read needing no such handshake doesn't
prove a write won't). This is the first thing the write live gate must
probe, in dry-run.

## Decoder bug exposed by real data

The journal decoded one fault from the live 4202 reply as
`{code:9, time:2103-06-05, count:79}`. Year 2103 is impossible — the
DecodeFaults slot layout is **misaligned against real bytes**. This is
exactly the Task 16 live-gate watch-item (tail-anchor assumption / 2-byte
slot skew, synthetic-fixture-only until now). The fault decode must be
re-derived against this capture before any fault entity is trusted; until
then the SAM-gated fault entities are decode-suspect, not just SAM-gated.
Filed as a follow-up, not fixed here.

## Validation wins

- Journal restart classification fired correctly: the add-on restart
  produced `outage_classified: monitoring_gap` (not a false
  `hvac_power_loss`) — the Task 6 logic works on real restart timing.
- `power_counters` reference captured (idu 24 / odu 6); gap classification
  has its baseline.

## Net

SAM reads are safe and accepted here (no NACKs, low failure rate). The write
path's central mechanism (3B03) is reachable and behaves as the
write-register hypothesis predicts. Two things gate real writes: confirming
the SAM-registration question in dry-run, and fixing the 4202 decode so the
fault surface is trustworthy. Both belong in the v2 spec.
