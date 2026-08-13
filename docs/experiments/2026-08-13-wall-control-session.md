# Wall-control session — 2026-08-13 (run/fault counters, heat mode, hold encoding)

Correlation of a hand-labeled wall-control menu walk + heat-mode exercise
against a morning bus capture (~296k frames, 10:09Z–13:21Z) using
`businspect` plus targeted grep. All times UTC unless noted.

**Timezone correction first**: the bus's own `000202` broadcasts prove local =
**UTC-4 (EDT)**, not UTC-5 — `09 15 04` at 13:21:01Z = 09:21 Thursday
(0x15 = 21 min), and `08 3a 04` at 12:58:00Z = 08:58. Every local-time decode
below uses UTC-4; this is load-bearing for the hold-until arithmetic.

System: cool mode, OAT rising 78.9→80.1 °F across the morning, compressor
duty-cycling at stage 1. Devices: 2001 wall control, 3e01 furnace (**GAS** —
confirmed at the panel), 5201 AC/heat-pump ODU, 6001 damper module,
2201 living-room zone sensor, 2301 basement zone sensor, f1f1 broadcast.

Confidence scale as in [2026-08-12-labeled-session.md](2026-08-12-labeled-session.md).

## Labels

| Label | Time (UTC) | Action |
|---|---|---|
| W1 | 12:59:59 | filter page open (panel "Accessories Status") |
| W2 | 13:01:30 | fault history page open |
| W3 | 13:02:08 | run/fault history menu (FURNACE + AC) |
| W4 | 13:03:03 | furnace GAS HEAT CYCLES page: power 24, blower 20470, heat_stage1 6598, heat_stage2 13 |
| W5 | 13:03:54 | AC COOLING CYCLES page: power 6, cool 12537 |
| W6 | 13:04:20 | GAS HEAT LIFETIME HOURS page: power 13368, blower 4990, heat_stage1 1035, heat_stage2 9 |
| W7 | 13:04:47 | COOLING RUN TIMES page: power 13808, cool 4171 |
| W8 | — | resettable-faults menu greyed out (equipment stores no faults) |
| W9 | 13:06:11 | "last 10 system events" page: code 172 "SMART SENSOR ZONE 3 COMM FAULT" 07/28/26 09:16AM ×1, code 171 "SMART SENSOR ZONE 2 COMM FAULT" 07/28/26 09:16AM ×1 |
| H1/H2 | 13:05:16–24 | HA-cloud heat command — **never landed** (Carrier cloud down; non-event, verified below) |
| H4 | 13:13:35 | PANEL: mode→HEAT, all zones heat target 78; living_room prompted "hold until 8:00 PM" and got it; others no hold |
| H7 | 13:20:28 | PANEL: restored — COOL, holds cleared, targets 73 |

Bus events consistently **led** the hand labels by ~16–25 s (H4 label
13:13:35 vs first writes 13:13:12–13:13:28; H7 label 13:20:28 vs writes
13:20:05–13:20:11) — labels were recorded after each action completed.

## Headline results

1. **The panel's run/time counter pages are the prior-art `000310`/`000311`
   KV tables — byte-exact.** All 12 panel values (W4–W7) match the
   continuously-polled register contents exactly; the key map from
   infinitude/infinitesp is CONFIRMED on this bus.
2. **Fault history and "last 10 system events" never touch the bus.** Zero
   reads of `4202` (or anything new); zero payloads anywhere in 296k frames
   containing the entries' known bytes; zero read-requests addressed to 2001
   in the whole capture. Wall-control local storage; SAM read of `4202`
   remains the Phase 2 path.
3. **Heat mode decoded**: `000305` byte0 = furnace heat stage (01 low →
   02 high), byte2 = cool-demand flag; heat airflow 0x0510 = 1296 CFM;
   `000605` stage float 0.0 throughout gas heat with a NEW mode flag at
   byte4 (01 cool / 00 heat); damper heat profile opens the **basement**
   (byte2 ≠ 0 for the first time ever — zone-map item closed).
4. **"Hold until 8:00 PM" encoding cracked**: `00041F` [3..4] u16 BE =
   remaining hold time in **2-second ticks** (= minutes × 30); initial
   0x4BD2 = 19410 = 647 min = exactly 09:13→20:00 EDT. [1] = 0x18 flags the
   timed hold ([0] bit7 remains the indefinite-hold flag).
5. **Keepalive cadence codified**: the thermostat rebroadcasts current state
   on fixed timers (10.2 s and 20.3 s groups) with unchanged payloads —
   periodic state pushes, not event commands (yesterday's H-CORRECTION now
   ground truth).
6. **LAT identity confirmed**: 3e01 `000302` TLV id 0x14 rose 70.25→78.2 °F
   across the gas-heat run (74.5 °F at 13:17:50, 76.1 °F at 13:18:57) —
   supply-air / leaving-air thermistor, heavily lagged (8/12 open item
   closed).

---

## Q1 — run/fault counter registers (W4–W7)

No new registers were read for the menu pages: the wall control polls
`000310`/`000311` continuously (3e01 every ~15.8 s, 5201 every ~10.2 s) and
serves the UI from its cache. Layout is the prior-art KV sequence
`key(u8) value(u24 BE)`.

### 3e01 `000310` — furnace cycle counters (28 B)

Constant all session:
`23 0019c6 | 24 00000d | 27 002f77 | 28 000000 | 2b 000018 | 2d 004ff6 | 48 000000`

| Key | Value | Panel (W4, 13:03:03Z) | Match |
|---|---|---|---|
| 0x23 heat_stage1 cycles | 0x19C6 = 6598 | heat_stage1 = 6598 | ✓ exact |
| 0x24 heat_stage2 cycles | 0x0D = 13 | heat_stage2 = 13 | ✓ exact |
| 0x27 unknown | 0x2F77 = 12151 | not shown | — |
| 0x28 unknown | 0 | not shown | — |
| 0x2B poweron cycles | 0x18 = 24 | power = 24 | ✓ exact |
| 0x2D blower cycles | 0x4FF6 = 20470 | blower = 20470 | ✓ exact |
| 0x48 med_heat cycles | 0 | not shown (2-stage furnace) | — |

### 3e01 `000311` — furnace lifetime hours (28 B)

`25 00040b | 26 000009 | 29 000fde | 2a 000000 | 2e 00137e | 2c 003438 | 49 000000`

| Key | Value | Panel (W6, 13:04:20Z) | Match |
|---|---|---|---|
| 0x25 heat_stage1 hours | 0x040B = 1035 | 1035 | ✓ exact |
| 0x26 heat_stage2 hours | 9 | 9 | ✓ exact |
| 0x29 unknown | 0x0FDE = 4062 | not shown | — |
| 0x2E blower hours | 0x137E = 4990 | 4990 | ✓ exact |
| 0x2C poweron hours | 0x3438 = 13368 | 13368 | ✓ exact |
| 0x49 med_heat hours | 0 | not shown | — |

### 5201 `000310` / `000311` — AC counters (16 B each)

`23 000000 | 28 0030f9 | 3c 000000 | 2b 000006` and
`25 000000 | 2a 00104b | 3d 000000 | 2c 0035f0`

| Key | Value | Panel | Match |
|---|---|---|---|
| 0x23 heat cycles / 0x25 heat hours | 0 | — | (gas furnace — HP never heats) |
| 0x28 cool cycles | 0x30F9 = 12537 | W5 cool = 12537 | ✓ exact |
| 0x3C defrost cycles / 0x3D hours | 0 | — | — |
| 0x2B poweron cycles | 6 | W5 power = 6 | ✓ exact |
| 0x2A cool hours | 0x104B = 4171 | W7 cool = 4171 | ✓ exact |
| 0x2C poweron hours | 0x35F0 = 13808 | W7 power = 13808 | ✓ exact |

**Increment behavior** (live, this capture): ODU cool_cycles ticked
0x30F2→0x30FA across the morning (8 compressor duty cycles); the 13:20:17
increment came **6 s after** the 0.0→1.0 stage-float restart at 13:20:11 —
cycles count at compressor **start**. The panel's 12537 (13:03:54) matches
the post-13:01:57 bus value exactly. Cool hours +2 over ~2.75 h of
mostly-on compressor; poweron hours tick with wall clock. The furnace's
13:13–13:20 gas cycle had **not** incremented 0x23/0x24 by capture end
(13:21:26) — furnace counter updates lag ≥1 min past cycle end.

**Unknown keys, new hypothesis**: IDU 0x27 = 12151 cycles / 0x29 = 4062 h
closely shadow the ODU's cool 12537 / 4171 — plausibly cooling
cycles/hours as tallied by the furnace board (blower-for-cooling). LOW.

Whole section: **HIGH** (12/12 values byte-exact against the panel).

## Q2 — fault history: CONFIRMED absent from the bus

Expected bytes from the documented 7-byte entry layout and the W9 screen
contents: codes 171/172 = 0xAB/0xAC; 2026-07-28 = **4956 days** since
2013-01-01 = 0x135C; hour 09, minute 16 = 0x10 → entry core
`.. 09 10 13 5c ..` (or `5c 13` LE).

- `0910135c`: **0 hits** in the whole capture. `09105c13` (LE): 0 hits.
  Every `135c`/`4202` substring in the capture is coincidental float bytes
  inside 3e01 `000413` / 5201 `00061F` payloads.
- Zero read-requests for any `42xx` register, at W2/W9 or ever.
- **Zero read-requests addressed to 2001 in the entire capture** — all
  135,330 frames to 2001 are op-06 replies to its own polls plus 12,056
  NACKs. Nothing on this bus ever reads the wall control.

Verdict: the fault-history and system-events pages are served from
wall-control **local storage**; with the equipment storing no faults (W8
greyed out) there is nothing equipment-side to read either. Reading `4202`
via a SAM-impersonation session remains the Phase 2 path to this data.
**HIGH** (exhaustive negative).

## Q3 — filter page (W1): no bus traffic

Diffed all op-0B read keys in 12:58:00–13:07:30 (covers W1–W9) against the
idle baseline: 57 distinct (src,dst,register) keys in the window, every one
also present ≥20× in baseline, **zero window-unique reads**, no
3B05-family anywhere. Filter life is computed and stored in the wall
control (protocol-tables gap #4 stands). **HIGH**.

## Q4 — heat mode (H4 → H7)

### `000305` (write 2001→3e01, 12 B) — mode + airflow

| Time | Payload | Reading |
|---|---|---|
| 13:12:57 | `00 00 02 00 021e 00…78` | cooling: byte2=02 cool demand, CFM 542 |
| 13:13:12 | (000605 float → 0.0) | compressor commanded off |
| 13:13:17 | `01 00 00 00 021e …` | **heat stage 1** (ignition), cool flag cleared |
| 13:13:28 | `02 00 00 00 021e …` | **heat stage 2** (high fire) |
| 13:14:08 | `02 00 00 00 0000 …` | airflow 0 (pre-heat blower off window) |
| 13:14:19 | `02 00 00 00 0510 …` | heat airflow **1296 CFM**, held to end of cycle |
| 13:20:05 | `00 00 02 00 0000 …` | restore: cool flag back, heat stage 0 |

**Layout: [0] = commanded heat stage (gas furnace: 01 low / 02 high),
[2] = cool-demand flag (0x02 cooling / 0x00), [4..5] u16 BE commanded CFM,
[11] = 0x78 constant.** Corroborated independently by 3e01 `000316`[0]
echoing 01→02 (prior art's "state enum low/med/high heat" — here the
2-stage ladder) and `000306`[3..4] echoing the CFM. **HIGH**.

### `000605` (write 2001→5201, 7 B) — stage float + NEW mode flag

`3f800000 01 00 01` (cooling, stage 1.0) → `00000000 00 00 01` at 13:13:12
(gas heat: compressor off **and** byte4 01→00) → `3f800000 01 00 01` again
at 13:20:11. **[0..3] float32 stage confirmed; [4] = system mode flag
(01 cool / 00 heat); [6] = 01 constant.** Stage float 0.0 for the entire
gas-heat run — compressor fully off in gas heat. [4] MED (two transitions,
both label-locked); rest HIGH.

### Dampers — heat profile, zone map closed

`000308`: cooling `0f 05 00 …` → **`0e 08 0f …` at 13:13:48** — basement
(byte2) commanded **full open for heat**, the first non-zero basement value
across both sessions; trimmed `0f 08 0f` (13:18:44) and `0f 08 0e`
(13:19:45); restored `0f 05 00` at 13:20:36. `000319` mirrored throughout.
**byte2 = basement CONFIRMED by manipulation** (8/12 open item closed).
Heat profile favors bedrooms+basement with living_room partial (0x08).
**HIGH**.

### Blower — heat ramp and an IDU-autonomy surprise

`000306`: RPM 0 (13:14:00, [9] 08→00 while stopped) → 400 → 725 → 870 at
CFM echo 0x0510; steady ~870 RPM through the cycle. After the 13:20:05
restore, commanded CFM stayed **0** yet the IDU ran **1134 CFM**
(`000306`/`000316` [.]=0x046e) at 785→1070 RPM to capture end — heat-
exchanger purge chosen by the furnace itself. First observed case of
`000306`/`000316` CFM diverging from the `000305` command; commanded-CFM=0
evidently cedes airflow authority to the IDU. LOW-MED (single event,
capture ends mid-purge).

### LAT — 3e01 `000302` TLV id 0x14 (H5, item closed)

70.25 °F before ignition → monotonic rise: 74.5 (13:17:50) → 76.06
(13:18:57) → 78.2 °F (13:20:48), still climbing after burner-off on
residual heat-exchanger heat. Identity = **supply-air (leaving-air)
thermistor**, slow/lagged placement — which also explains yesterday's
failure to see it drop during the short stage-5 cool run. **HIGH** on
identity, with the caveat that its magnitude lags true duct temp badly.

### `00060B` + `000406` pair (`01 04 XX 00`)

XX sat at 0x60 (96) from the 12:58 compressor-off idle through the whole
gas-heat run, then climbed 0x63→0x73 (99→115) across the post-restore
compressor restart (still climbing at capture end). Consistent with a
refrigerant-loop target that parks at ~96 when the compressor is off and
re-ramps on start. Observation only; identity still LOW-MED.

### Setpoints during heat — `00041F`/`00041E`

- **[6] heat setpoint °F CONFIRMED by manipulation** (was MED on 8/12):
  0x44/0x42 → **0x4E = 78** on both sensor zones at H4; restored to
  0x47 = 71 — the *scheduled* heat target, not the pre-session 68.
- [7] cool setpoint auto-bumped 0x49 (73) → **0x50 (80)** while heat target
  was 78 — the panel enforces its deadband by pushing the cool target up.
  Restored to 0x49 at H7.
- `00041E` mirrors [3..4] hold countdown, [6..7] setpoints; NEW: [13]
  0x00→0xC0 at heat/hold start, still 0xC0 at capture end (flags,
  identity unknown). LOW.

### H1/H2 — cloud heat command: verified non-event

No register anywhere changed at 13:05:16–24 ±2 min beyond routine
telemetry; `00041F` was byte-stable from 10:09:34 until 13:13:19. The
Carrier-cloud command never reached the bus.

## H6 — "hold until 8:00 PM" encoding (2201 `00041F`)

Baseline: `00 00 00 00 00 00 44 49 00 00 04 …`. At 13:13:19:

`00 18 00 4b d2 00 4e 50 00 00 04 …`

| Offset | Value | Meaning | Confidence |
|---|---|---|---|
| 0 | 0x00 | bit7 indefinite-hold flag **not** set (2301 kept 0x80 all session — permanent hold, no timer fields) | HIGH |
| 1 | 0x18 | timed-hold ("hold until") active marker | MED |
| 3..4 | 0x4BD2 | **u16 BE remaining hold time in 2-second ticks (= minutes × 30)** | HIGH |
| 6 | 0x4E | heat setpoint 78 | HIGH |
| 7 | 0x50 | cool setpoint 80 (deadband push) | HIGH |

Arithmetic (using the verified UTC-4): hold set 09:13 EDT until 20:00 EDT
→ 647 minutes remaining; **647 × 30 = 19410 = 0x4BD2 exactly.** The value
then decremented 0x1E (30) per ~61 s rebroadcast — d2→b4→96→78→5a→3c→1e→00
→ 0x4AE2 (borrow across byte 3: 0x4B00 − 0x1E) — i.e. 1 tick = 2 s,
30 ticks/min. The "0x4B0 minutes-since-midnight" guess is dead; the
`004b…` resemblance was coincidence (countdown, not clock time).

Post-restore tail: the 13:20:07 write reverted setpoints to 71/73 but [1]
stayed 0x18 and the countdown kept running (0x4AE2 at 13:21:08, 62 s
before capture end). Either lazy clearing or the timer field free-runs
until rewritten — open; needs a capture extending past a hold clear.

## Q5 — keepalive cadence (H-CORRECTION codified)

Idle window 10:30–12:30 (no user actions), write cadence from 2001:

| Cadence | Registers | Evidence |
|---|---|---|
| **10.2 s** (median; 9.3–11.3) | `000605`, `00060D`(=0x02 const), `000610`, `000612`, `00061A`, `00061E` → 5201; `000305`, `000307` → 3e01; `000308`, `003404` → 6001 | 707–710 writes each in 2 h; `000605` had exactly 2 payloads (stage 1.0/0.0 — the compressor duty cycle), `00060D` exactly 1 |
| **20.3 s** | `00060B` → 5201 + `000406` → 3e01, back-to-back identical-prefix pair | 354–355 writes, 7 distinct payloads (slow loop-target walk) |
| ~5 s | `00041F`, `000420` → 2201/2301 | `00041F` payload unchanged for 3 h straight |
| 60 s | `000202`/`000203` → f1f1 | time broadcasts |

**Verdict: these WRITEs are periodic rebroadcasts of current commanded
state on fixed timers, not event-driven commands.** State changes ride the
next scheduled slot (which is why every labeled action lands within one
cadence period). HIGH.

## New observations (out of scope, logged)

- **`000420` (write 2001→2201/2301, 20 B, ~5 s cadence) — environment push
  [NEW]**: `f0 02|0a c0 00 0f 01 04 XX 02 02 RH 3c 50 00 3c 50 00 01 HH MM`
  — [6..7] u16/16 °F = **OAT** (78.9→80.1 °F across the morning), [10] =
  RH % (0x37–0x3A), [18..19] = local hh:mm, [11..12]/[14..15] `3c 50` =
  60/80 (plausibly setpoint limits). How the sensors use it unknown.
  LOW-MED.
- NACKs: 12,056, all 3e01→2001 code **0x0A** (the ~1 s `000715` poll
  refusal) — same picture as 8/12. Zero op-0x1E alarm frames again.

## Open questions from this session

- `00041F`[1]=0x18 bit meaning; countdown continuing after hold clear —
  capture past a full hold expiry/clear.
- Furnace `000310` increment latency (catch the tick after a gas cycle).
- IDU keys 0x27/0x29 (cooling-blower cycles/hours hypothesis) — compare
  against ODU deltas over weeks.
- `00041E`[13] 0xC0 flag; `000420` consumer-side purpose.
- Commanded-CFM=0 semantics (IDU autonomy) — observe a full purge to
  blower-stop.
