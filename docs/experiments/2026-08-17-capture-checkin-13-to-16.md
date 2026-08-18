# Capture check-in — 2026-08-17 (archive of 08-13→08-16, cap hit, new leads)

Routine check-in on the running capture add-on, plus a `businspect tables`/
`alarms` sweep of the second archived capture. Confidence scale as in
[2026-08-12-labeled-session.md](2026-08-12-labeled-session.md).

## Operational

- **The 1 GiB capture cap fired as designed**: `capture.jsonl` froze at
  1024.0 MB on 2026-08-16 04:55Z while the daemon kept decoding (~1,530
  frames/min, resync bytes creeping single digits per hour — healthy bus).
  **~2 days of traffic (08-16 05:00Z → 08-18 09:52Z) went unrecorded.**
- Rotated on the box (`capture-2026-08-13-to-16.jsonl`, same pattern as the
  first archive) and restarted the add-on 2026-08-18 09:52Z; new capture
  confirmed growing. `/share` has 92 GB free; the archive is also pulled
  local as gzip (83 MB — 12:1, worth remembering for transfers).
- **Follow-up (operational):** the cap silently stops recording while the
  daemon stays green. Either rotate-on-cap in the daemon or a size watch in
  HA would have caught this 2 days sooner.

## The archived capture

5,304,896 frames, 2026-08-13 19:34Z → 2026-08-16 04:55Z (~2.4 days of
cooling-season duty cycling). Bus is clean: **zero op-`1e` alarm frames,
zero NACK codes other than the known ~1 s `0x0a`** (`000715` poll refusal,
3e01→2001, same picture as 8/12 and 8/13).

## New leads

- **`000410` (furnace, NEW — undocumented)**: wall control reads it from
  3e01 **once nightly at local midnight** (04:01–04:02Z, 2–3 read pairs per
  night, all three nights). Response is a constant u16 `02eb` = **747**.
  Semantics unknown — a daily-checked counter/threshold (service hours?
  filter-related?). Watch whether it ever changes. LOW.
- **`000420` delivery switched unicast→broadcast mid-capture**: per-sensor
  writes (2001→2201/2301) stop at 2026-08-15 21:00Z (17:00 EDT); from then
  on the same 20-byte payload rides **op `0c` broadcasts to `f1f1`** (~5 s
  cadence, still flowing 08-18). The OAT decode holds through the switch
  ([6..7] u16/16 °F: `04c6` = 76.4 °F on the 08-18 morning frame). What
  triggered the mode change is unknown — nothing else on the bus changed at
  that timestamp. MED (observation), LOW (cause).
- **Zone-sensor asymmetry on `00041f`**: living room (2201) shows 451
  distinct payloads across offsets [0..7]; basement (2301) is byte-constant
  the whole 2.4 days. Whatever 41f carries, it's active on exactly the zone
  that had the 08-13 hold exercise. Feeds the open `00041f`[1]=0x18 /
  countdown question. MED.
- **Cycle/runtime counters ticked**: 3e01 `000310`/`000311` each show 4
  distinct payloads — real increments on the gas furnace counters during a
  cooling-only window (blower/power counters, presumably). The
  increment-latency open question is now answerable from this archive. Not
  yet extracted.
- `5201 000302` (ODU sensor block) logged 32,574 distinct payloads over the
  window — dense duty-cycle coverage for the superheat/subcooling and
  commanded-CFM=0 purge questions. Not yet mined.

## Status vs plan

Phase 1 + businspect: complete. Phase 2 (16-task TDD plan,
[2026-08-13-phase2-decode-publish-guide.md](../superpowers/plans/2026-08-13-phase2-decode-publish-guide.md)):
**not started** — 0/16 tasks. The two archived captures (700 MB + 1 GB) are
the golden-fixture source the plan calls for.
