# Longitudinal findings — 2026-08-23 (captures 08-13→08-16 and 08-18→08-20)

Unlabeled multi-day sweep of the second and third archives with `businspect
timeline` plus targeted greps, answering open questions from
[2026-08-13-wall-control-session.md](2026-08-13-wall-control-session.md).
Corpus: `capture-2026-08-13-to-16.jsonl` (5,304,896 frames, 08-13 19:34Z →
08-16 04:55Z) + `capture-2026-08-18-to-20.jsonl` (5,308,324 frames, 08-18
09:51Z → 08-20 19:11Z). Cooling season throughout; no hands on the panel
during either window (holds below were normal household use).

## Operational

- The 1 GiB cap fired **again** on 08-20 15:11 local (second time; ~3 days
  of tail lost each time). Fixed properly this session: rotated to
  `capture-2026-08-18-to-20.jsonl`, and the add-on now runs with
  `-capture-max-mb 8192` (~20 days at observed rate; run.sh in repo +
  on-box, add-on rebuilt 08-23). Recording verified flowing.
- Recorded corpus to date: ~13.3M frames / 2.7 GB across three archives
  (08-11→13, 08-13→16, 08-18→20).

## Answers to open questions

### `000310`/`000311` increment latency → daily batch rollup

IDU (3e01) counter commits, every one observed:

| Commit (Z) | Day |
|---|---|
| 08-13 21:02:22 | +0 |
| 08-14 21:05:44 | +1 |
| 08-15 21:09:30 | +2 |
| 08-18 21:20:11 | +5 |
| 08-19 21:23:29 | +6 |

One commit per day, drifting +3–4 min/day → a free-running ~24h04m timer,
not a wall-clock schedule. Poweron hours (0x2C) advance exactly +24/day
(0x3438→3450→3468→3480 … →34C8→34E0), which independently proves both the
u24 decode and the once-daily cadence. So a counter increment can lag the
physical cycle by up to ~24 h — decoders should present these as daily
totals, and the validation harness must not expect same-hour movement.

ODU (5201) `000310` is different: its cool-cycle key increments within
seconds of compressor start (confirmed 08-13), and the a2/a3 timelines show
per-cycle single-key bumps (offset 7) — **the ODU counts live, the IDU
batches daily.**

### IDU keys 0x27/0x29 → cooling-blower cycles/hours (MED-HIGH)

Cooling-only weeks; all heat keys frozen. Deltas per rollup:

- 0x27 (cycles): 12151 → +33 → +31 → +43 → (gap) → +37 → +45 per day —
  matches compressor duty cycling frequency.
- 0x29 (hours): 4062 → +20 → +20 → +18 → (gap) → +19 → +19 per day —
  matches ~80 % cooling duty in August heat.
- 0x28/0x2A: byte-constant on 3e01 all week.

### `00041F` hold-clear tail → resolved

The 08-13 session left "countdown kept running ≥60 s after panel
hold-clear" open. Natural expiry (twice, 08-13 and 08-15, both 8:00 PM EDT
holds): countdown walks 30/min to 0x001E, then the next frame (≤5 s past
the target minute) zeroes it, clears [1] 0x18→0x00, and reverts [6..7]
setpoints to schedule in the **same frame** (4749=71/73 → 424a=66/74).
Scheduled setpoint transitions ride the same bytes (10:00Z/06:00 EDT →
4449=68/73). New hold set 08-14 17:19Z: [1]=0x18 returns with countdown
0x1518 = 5400 ticks = exactly 3 h; [0] pulses 01↔00 and [2] briefly 0x0C
during the set transaction (transaction/dirty flag, LOW). Steady idle both
zones: [0]=0x80.

So the 41f churn asymmetry (2201 busy, 2301 flat) is fully explained: only
zones with active timed holds tick.

### `000410` — new nightly register

3e01 answers `000410` = u16 `0x02EB` (747) 2–3× at local midnight
(04:01–04:02Z), all 5 nights across both archives; never read at any other
time, never a different value. Unknown daily-checked threshold/counter —
cheap watch: alert when it moves.

### `000420` delivery mode

Not one-way. a2: unicast until 08-15 21:00Z, broadcast (op `0C` → F1F1)
after. a3: broadcast-only until unicast resumes at exactly 08-18 12:00:12Z;
22,762 broadcast + 36,297 unicast frames coexist over the window. Transitions
observed only at clock boundaries (21:00Z, 12:00Z). Payload identical in
both modes; OAT [6..7] decode holds (76.4 °F on an 08-18 morning frame).
Consumer purpose still unknown.

## Still open (data exists, not yet mined)

- Commanded-CFM=0 purge autonomy: needs a cycle-stop slice of 3e01
  `000306` vs 2001 `000305`; 5201 `000303`[3] turned out to be a modulating
  nibble (0x00–0x30), not a crisp stage flag, so stop-finding needs the
  ODU sensor block instead.
- `00041E`[13] 0xC0 flag; `000420` consumer purpose; `000410` semantics.

## Bus health

Across both archives: zero op-`1E` alarms, zero non-`0A` NACKs, resync
single-digit bytes/hour. Seven recorded days without a single protocol
anomaly — the transport layer is boring, which is exactly what Phase 2
wants under it.
