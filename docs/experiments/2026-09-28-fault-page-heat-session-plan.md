# Session plan — fault-page ground truth + heat-season labels

Status: PLAN (not yet run). Written 2026-09-28. Operator at the wall control,
agent at the capture. ~45 minutes. Everything here is a READ; nothing is
written to the bus.

## Why

Register `004202` (fault history) is half-decoded: 10 entries x 7 bytes + a
2-byte tail, byte[0] = fault code (corroborated twice). The timestamp encoding
cannot be derived from the single reply we have — one sample, and only one
entry with a known date. What cracks it is **one fresh 4202 reply paired with a
photo of every entry on the panel's fault page** at the same moment: ten
labeled records instead of one.

Heating season has started (stage-1 cycles are climbing again), so the same
sitting can also label heat staging, which the summer corpus could not.

## Key mechanic

Every SAM target is due immediately when the daemon starts. With `sam_reads`
on, **each add-on restart fires exactly one 4202 read** (then hourly). So the
operator controls when the read happens: restart = read. No new faults need to
occur.

## Before (agent)

1. Back up the event journal (`events.jsonl`) — the current 4202 decoder is
   known-misaligned and will emit a spurious fault event (it read "year 2103"
   on 2026-08-28). Restore the backup after the session instead of deleting
   the journal.
2. Confirm capture headroom (fresh file, rotated 2026-09-28) and
   `sam_read_failures` = 0.
3. Flip the add-on option `sam_reads: true` — do not restart yet.

## Part A — fault page (operator + agent, ~15 min)

1. Operator opens the fault / service history page on the wall control.
2. **Photograph every entry**: code, description, date, time — scroll through
   all of them. Note the order the panel lists them (newest first?).
3. Agent restarts the add-on (= one 4202 read). Note the wall-clock time.
4. Agent confirms a `2001->9201 op 06 004202` reply landed in the capture.
5. Repeat the restart once more a few minutes later (a second, identical
   reply is the control: any byte that differs between the two replies is not
   part of the stored log).

Abort if: any NACK or `op 1e` alarm appears, `sam_read_failures` climbs more
than a few, or the panel shows a new fault. Flip `sam_reads` off and restart.

## Part B — heat labels (operator, ~25 min; only if outdoor temp allows)

Pick a REMOTE zone (living_room or basement — zone 1/bedrooms is invisible on
the bus). Operator writes down the time of each action to the minute.

1. Raise that zone's heat setpoint ~4 degF above room temperature. Label:
   "heat call".
2. Watch for stage 1 fire (`000305[0]`, `000605`, blower ramp). Label when the
   panel shows heating.
3. Leave it calling ~15-20 min to see whether the control stages up to
   stage 2 on its own (stage-2 fires are rare: 14 in the unit's lifetime).
4. Restore the setpoint. Label: "call ends".

## After (agent)

1. `sam_reads: false`, restart, confirm passive-only.
2. Restore the backed-up `events.jsonl`.
3. Cut the two 4202 replies into a fixture next to
   `reg-004202-sam-reply-2026-08-28.jsonl` (private archive, not this repo,
   until de-identified) and decode against the photos.
4. `businspect diff` / `timeline` over the Part B window for the heat labels.
5. Write the session up here as a dated experiment note.

## Not in this session

The SAM Announce test stays parked: it needs `samctl` + dry-run, which do not
exist yet, and it must come after the 4202 fix (the fault table is our only
instrument for noticing the panel objecting — the fault-146 lesson).
