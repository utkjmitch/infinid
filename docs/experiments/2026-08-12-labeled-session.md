# Labeled experiment session — 2026-08-12 (first live verification)

Correlation of hand-labeled thermostat actions against a full-session bus
capture (~496k frames, 15:43Z–21:01Z) using `businspect` (tables / timeline /
diff / alarms) plus targeted grep of op-0x0C WRITE frames. All times UTC.
System: cool mode throughout, outdoor ~86 °F (bus says 89–91 °F at the ODU
sensor), 3 zones (basement / bedrooms / living_room), all zone targets 73 °F at
baseline. Devices: 2001 wall control, 3e01 air handler, 5201 inverter heat
pump, 6001 damper module, 2201/2301 remote smart sensors, f1f1 broadcast.

Confidence scale: HIGH = multiple independent corroborations (label timing +
physics + cross-register), MED = single strong correlation, LOW = plausible
reading only.

## Labels

| Label | Time (UTC) | Action |
|---|---|---|
| E1 | 20:39:21 | living_room cool setpoint 73→70 |
| E1-end | 20:42:55 | living_room →73 |
| E2 | 20:43:56 | bedrooms cool setpoint 73→76 |
| E2-end | 20:47:00 | bedrooms →73 |
| E3-high | 20:48:01 | fan high |
| E3-med | 20:49:31 | fan med |
| E3-low | 20:51:02 | fan low |
| E3-auto | 20:52:32 | fan auto |
| E4 | 20:56:33 | preset vacation |
| E4-end | 20:58:15 | preset resume |

## Headline results

1. **Setpoint writes land in register `00041F`, WRITEs from the wall control
   to the zone's smart sensor** (2001→2201 for living_room). Cool setpoint is
   payload byte 7, whole °F. **The wall-control's own zone (bedrooms) setpoint
   never appears on the bus at all** — E2 produced zero setpoint writes, only
   damper/airflow consequences.
2. **Fan mode is an enum at `00041F` byte 5: 0=auto 1=low 2=med 3=high** —
   the E3 ladder produced exactly 3→2→1→0, each within 13 s of the label.
3. **Dampers**: thermostat WRITEs `000308` to 6001 (byte per zone, 0x00
   closed … 0x0F open); 6001 mirrors into `000319` ~10–15 s later.
   **Zone map: byte0 = bedrooms, byte1 = living_room, byte2 = basement.**
   Partial positions (0x04–0x0B) are used continuously for balancing.
4. **Compressor staging confirmed end-to-end**: 2001 writes commanded stage as
   float32 into `000605` (2.0 → 5.0 at E1+16s → 1.0 at E1-end+6s); `00060E`
   byte 0 reports actual stage (2→5→1); `000604` [0..1]/[2..3] =
   target/actual compressor RPM. `000625` u16[0..1] is a power-like analog
   (2088 → 4802 → ~1040) — the old "byte1 = stage %" hypothesis is dead.
5. **`0202` broadcast is hour/minute/weekday** (0x10 0x27 0x03 = 16:39
   Wednesday, correct for 2026-08-12 local) — CarBus reading confirmed, wiki
   "HH MM SS" contradicted. `0203` = day/month/year confirmed (0c 08 1a).

---

## E1 / E1-end — living_room setpoint 73→70→73 (full cause→effect chain)

| Δt from E1 | Register | Evidence |
|---|---|---|
| +13 s (20:39:34) | WRITE 2001→2201 `00041F` | byte7 `49`→`46` (73→70 °F). Restored `46`→`49` at 20:43:08 (E1-end+13s) |
| +16 s (20:39:37) | WRITE 2001→5201 `000605` | `40000000`→`40a00000` (float 2.0→5.0, commanded stage). Back to `3f800000` (1.0) at 20:43:01 |
| +21 s (20:39:42) | WRITE 2001→6001 `000308` | `0f06000000000000`→`0b0f000000000000` — living_room (byte1) full open, bedrooms (byte0) pinched to 0x0B. Reverted to `0f05…` at 20:43:16 |
| +26 s (20:39:47) | 5201 `00060E` byte0 | actual stage `02`→`05`; `01` at 20:43:10 |
| +37 s (20:39:58) | 6001 `000319` | `0f06…`→`0f0f…` then `0b0f…` at 20:40:03 (feedback mirrors command, ~15 s lag). `0f0f`→`0f05` after E1-end |
| +82 s (20:40:43) | WRITE 2001→3e01 `000305` | commanded CFM bytes[4..5] `032d`(813)→`05c4`(1476). Down to `022e`(558) at 20:43:16 |
| ramp | 5201 `000604` | target RPM `0b22`(2850) → `0e74`(3700) → `102c`(4140); actual ramps to match each step; then target `06a4`(1700) → `04b0`(1200) after E1-end |
| ramp | 5201 `000625` | u16[0..1] 0x0828(2088) → 0x0ae7 → 0x0fec → 0x12c2(4802) at stage 5; decays to ~0x0410(1040) at stage 1 |
| ramp | 3e01 `000306` | blower RPM [1..2] 645→1025 as CFM [3..4] `032d`→`05c4`; CFM bytes exactly echo the `000305` command |
| ramp | 3e01 `000413` | measured CFM [0..1] 1437–1480 (matches command 1476), RPM [2..3] 1025, static float [4..7] 0.66 in wc, blower power float [8..11] 469 W (vs 0.32–0.43 in wc / 77–117 W at ~560 CFM) |
| thermal | 5201 `000302` | coil 95.4→101.6 °F, discharge 152.9→167 °F at stage 5 (all /16 °F) |

Every step of the chain is label-locked and physically consistent
(higher static at higher flow, higher RPM per CFM when a damper closed,
coil/discharge temps rise at stage 5). **HIGH** across the chain.

## E2 / E2-end — bedrooms setpoint 73→76→73

**No setpoint WRITE anywhere on the bus** (bedrooms is the wall control's own
zone; the value stays internal to 2001). Observable consequences only:

- 20:44:18 (+22 s) WRITE `000308`: `0f05…`→`000f…` — **bedrooms damper
  (byte0) commanded fully closed**, living_room opened full to take the
  minimum airflow. `000319` followed: `0f0f` at 20:44:34, `000f` at 20:44:49.
- 20:47:21 (E2-end+21 s) WRITE `000308`: back to `0f05…`; `000319` mirrored
  by 20:47:52.
- Demand kept decaying (stage already 1 after E1-end): `000604` target
  stepped 1700→1200 RPM at 20:44:34; commanded CFM stayed 558–576.

Zone-byte identification is decisive from timing: byte0 closed at E2+22 s and
reopened at E2-end+21 s; byte1 opened full at E1+21 s and dropped at
E1-end+21 s. Basement (byte2) stayed 0x00 the whole session (no cooling
demand); bytes 4–7 are 0xFF in `000319` (absent). **HIGH**.

## E3 — fan high → med → low → auto

- WRITE 2001→2201 `00041F` byte5: `00`→`03` at 20:48:14 (high, +13 s),
  `03`→`02` at 20:49:36 (med), `02`→`01` at 20:51:08 (low), `01`→`00` at
  20:52:40 (auto). **Enum 0=auto 1=low 2=med 3=high — HIGH** (matches the
  prior-art 3B03 fan enum).
- 2201's own `00041E` byte5 echoes the value on its next serve.
- **Physical effect was nil**: commanded CFM (`000305`) stayed in the
  558–576 band through all four steps, blower RPM ~600–685. During active
  cooling the blower follows the cooling airflow demand; the fan-mode
  selection did not override it downward or upward at stage 1. (So fan mode
  is a floor, not a direct blower command, on this firmware — MED inference.)

## E4 — vacation / resume

**Vacation produced no observable bus traffic.** No WRITE to any device
changed at 20:56:33±60 s beyond the routine per-minute airflow trim; the
smart-sensor `00041F` setpoints were *not* rewritten to vacation targets
during the 102 s the preset was active; dampers, stage, CFM all continued
steady-state stage-1 cooling. Vacation state evidently lives inside the wall
control (its own 3B04/4012-class tables are never read by anyone on this bus
— there is no active SAM in this capture).

The single artifact: at 20:58:27 (resume+12 s) WRITE 2001→2201 `00041F`
byte0 `80`→`00`. Reading: **bit7 of byte0 = hold-active flag**, cleared by
"resume schedule" (the LR zone had been in hold since before the session;
E1/E1-end manual changes were holds). MED — one transition observed, but the
trigger is exact.

---

## Per-register conclusions

### 2001 → smart sensor WRITE `00041F` (20 bytes) — zone config push [NEW]

`80 00 00 00 00 FF SS CC 00 00 04 00 00 …`

| Offset | Meaning | Evidence | Confidence |
|---|---|---|---|
| 0 | flags; bit7 = hold active | 0x80→0x00 at resume+12 s | MED |
| 5 | fan mode 0=auto 1=low 2=med 3=high | E3 ladder 3/2/1/0, each +13 s | HIGH |
| 6 | heat setpoint, whole °F | 0x44=68 (LR), 0x42=66 (basement); never manipulated today | MED |
| 7 | cool setpoint, whole °F | 0x49→0x46→0x49 exactly at E1/E1-end | HIGH |
| 10 | constant 0x04 | — | — |

Prior art had `00041F` as "Hunterhill-observed, undecoded" — now decoded.
Only zones with a remote smart sensor get these writes.

### Smart-sensor served `00041E` (20 bytes) — zone status [NEW]

Sample (2201): `80 00 00 00 00 01 44 49 01 04 94 49 36 00 …`
Sample (2301): `80 00 00 00 00 00 42 46 01 04 64 46 37 00 …`

| Offset | Meaning | Evidence | Confidence |
|---|---|---|---|
| 5 | fan mode echo | followed E3 values | HIGH |
| 6 | mirrors 41F byte6 (heat sp) | 68/66 match per zone | MED |
| 7 | zone temp, whole °F | 2301 shows 70 while its cool sp is 73 — **not** a setpoint | MED |
| 9–10 | u16 BE zone temp ×16 | 0x0494/16=73.25 °F (LR, known ~73), 0x0464/16=70.25 (basement); ticks 0494→0493 during E1 cooling | HIGH |
| 11 | zone temp, whole °F (second copy) | 0x49=73 / 0x46=70 | MED |
| 12 | relative humidity % | 0x36=54 / 0x37=55, drifts by 1 | MED |

### 2001 → 6001 WRITE `000308` / 6001 served `000319` — dampers

8 bytes, one per zone slot, 0x00 closed … 0x0F open; `000319` bytes 4–7 =
0xFF (slots absent). **Zone map (this install): byte0 bedrooms, byte1
living_room, byte2 basement.** `000319` mirrors `000308` with ~10–15 s lag.
Partial positions 0x04–0x0B are the norm, not just the prior-art
0x00/0x0A/0x0F trio. Prior-art register roles CONFIRMED; position
granularity and per-zone mapping REFINED. HIGH.

### 2001 → 3e01 WRITE `000305` — blower airflow command [NEW layout]

12 bytes `00 00 02 00 CC CC 00 00 00 00 00 78`; bytes[4..5] u16 BE =
commanded CFM (813 baseline → 1476 at stage 5 → 558 at stage 1); byte11 =
0x78 (120) constant. Echoed exactly by `000306`[3..4] and matched by
`000316`[4..5] (1472 vs 1476 commanded). Prior art said only "writes to IDU,
layout varies". HIGH.

### 3e01 `000306` — blower status

[1..2] u16 blower RPM (645–1025 observed, tracks CFM and static) — prior art
CONFIRMED. [3..4] u16 CFM, echo of the `000305` command — wiki claim
CONFIRMED. [5..6]=200, [7..8]=2200 constants (plausibly min/max CFM of this
air handler). HIGH.

### 3e01 `000413` — blower telemetry [NEW]

12 bytes: [0..1] u16 measured CFM (1437–1480 during the 1476 command; noisy
484–614 at low flow), [2..3] u16 blower RPM (matches `000306`), [4..7]
float32 BE static pressure in wc (0.32–0.43 low flow, 0.66 at 1476 CFM),
[8..11] float32 BE blower electrical power W (77–117 W low, 469 W high).
CFM/RPM HIGH; static/power MED (magnitudes and monotonicity are right, no
external meter cross-check).

### 3e01 `000316` — airflow config

[4..5] u16 CFM CONFIRMED (1472 at stage 5). [7..8] u16 load-correlated
(5451 at high flow, ~1300–1700 low) — identity unknown (torque? mW?);
prior-art static-pressure-vs-elec-heat-CFM conflict still unresolved. LOW.

### 3e01 `000302` — IDU thermistor TLV

`00 11 00 00 | 01 14 04 59 | 00 1b 00 00` — same TLV format as the ZC
(tag 01=present, 04/00=absent, u16/16 °F). id 0x14 (LAT, leaving-air
thermistor) = 69.5–70.7 °F all afternoon, drifting down slowly during
cooling. Plausible mixed leaving-air temp at stage-1 airflow, but it never
dropped toward 55–60 even during the 3.5-min stage-5 run — either heavy lag
or a cabinet (not duct) sensor. Scaling /16 HIGH; identity-as-LAT MED.

### 3e01 `000406` + 5201 `00060B` — paired thermostat writes [NEW pairing]

Identical payload prefix `01 04 XX 00` written back-to-back to IDU and ODU;
byte2 walked 0x64→0x63→0x61→0x63→0x64 (100→97→100) across the session,
tracking load/OAT. Consistent with prior art's `060B[2]` "target °F 25–115,
refrigerant-loop control target" (values ≈ condensing-temp target at
OAT ~90 °F). The dual IDU/ODU write is new. LOW-MED.

### 5201 `000605` — commanded stage (float32, write-only 2001→ODU)

2.0 → 5.0 (E1+16 s) → 1.0 (E1-end+6 s). Prior art CONFIRMED exactly,
including the ~10 s lag into `00060E`. HIGH.

### 5201 `00060E` byte0 — actual stage

02→05→01, label-locked. Prior art CONFIRMED. HIGH. (Bytes 1–124 remain
churny telemetry; ramp counters at [6..27] move monotonically with runtime.)

### 5201 `000604` — compressor RPM + stage tables

[0..1] target RPM, [2..3] actual RPM — CONFIRMED (target steps
2850/3700/4140 during the stage-5 ramp, actual chases each step over ~30 s;
1700 then 1200 on the way down). **NEW**: [4..13] and [14..23] are two
static five-entry u16 stage-RPM tables — heat 1200/2600/3200/4140/5400,
cool 1200/2180/2850/3700/4140. This unit's ladder differs from the prior-art
rated speeds (1500/1700/2460/2800/3650). HIGH.

### 5201 `000625` — power-like analog [hypothesis REVISED]

u16 BE at [0..1], reported every ~30 s: ~2050–2090 at stage 2, continuous
ramp to 4802 at stage 5, ~1000–1100 at stage 1, small jitter at constant
stage. **Not** byte0=stage/byte1=percent (byte0 reached 0x12 at stage 5).
Magnitudes fit compressor input power in watts (≈4.8 kW at stage 5 for this
tonnage). Field = single u16 HIGH; watts interpretation MED.

### 5201 `000302` — ODU temperatures [format REFINED]

Not flat (threshold, value) pairs — it is the **same 4-byte TLV as the ZC**:
`tag(01) id value(u16 BE /16 °F)` × 6:

| id | Value observed | Identity | Confidence |
|---|---|---|---|
| 0x11 | 89.7–91.4 °F | outdoor air temp (label ~86 °F, sun-warmed pad plausible) | HIGH |
| 0x12 | 95.4→101.6 °F | outdoor coil (rises at stage 5) | HIGH |
| 0x30 | 55.7–57.9 °F | suction line temp — cross-checks: sat temp of R-410A at 120 PSIG ≈ 41 °F + superheat 16 °F = 57 °F ✓ | HIGH |
| 0x4A | 14.0–16.0 | superheat ΔT | HIGH |
| 0x4B | 79–81 °F | unknown (rose during cooling; not zone ambient) | LOW |
| 0x45 | 152.9→167 °F | compressor discharge (rises at stage 5) | HIGH |

### 5201 `000303` — suction pressure

[2..3] u16 /16 = 119–127 PSIG, breathing with stage. CONFIRMED (and it
closes the superheat arithmetic above). HIGH.

### 5201 `000304` byte7 — line voltage

244–245 V. CONFIRMED. HIGH.

### 5201 `000608` — drive frequency / EEV

[5..6] u16 raw where RPM = 3 × raw: raw 1470 ↔ ~4140–4410 RPM during the
stage-5 rampdown ✓ CONFIRMED. [2] = 0x64 = 100, consistent with EEV % at
full load (never left 100 today). MED-HIGH / MED.

### 5201 `000602` — outdoor fan RPM [NEW]

[12..13] u16 target (900 at stage ≥2 → 500 at stage 1, snaps), [4..5] u16
actual (ramps 900→822→498 chasing it). byte0 toggles 0xD1↔0x91 (bit6,
~20–100 s period, unknown flag). MED.

### 5201 `00061F` — float diagnostics

Prior-art float family partially present: 0.039 constant at [21..24] ✓,
superheat/subcool-like floats (8.3/14.4/18.4/16.4). **NEW candidate**:
float32 at [30..33] = 489 (stage 5) → 357 (stage 1 late), tracks head
pressure magnitude in PSIG. MED-LOW.

### f1f1 `000202` / `000203` — time broadcasts

`10 27 03` = 16:39, weekday 3 = Wednesday (correct local wall time; ticks
once per minute at byte1). **Confirms CarBus hour/min/weekday; contradicts
wiki "HH MM SS".** `0c 08 1a` = 12 Aug 2026 ✓ day/month/year(+2000). HIGH.

### 6001 `000302` — ZC sensor TLV

All six entries `04 id 00 00` (ids 1–4, 0x14, 0x1C) = "not installed" — tag
semantics and TLV format CONFIRMED (no ZC-local sensors at Hunterhill). HIGH.

### Exceptions / alarms

19,896 NACKs in this capture, **all** 3e01→2001 **code 0x0A**, every one a
response to the ~1 s poll of `000715` (which 3e01 also answers with empty
06-replies — the wall control polls it regardless). Contradicts infinitude's
"all 54k exceptions were 0x04". Zero op-0x1E alarm frames observed. HIGH.

## Open questions from this session

- `00041E`[6]/[7]/[11] exact roles (need a session where zone temp crosses a
  degree boundary and where heat setpoint is manipulated).
- Basement damper byte2 never left 0x00 — force basement cooling demand to
  confirm the third zone byte.
- `000625` units (clamp a power meter on the ODU).
- `000316`[7..8] identity; `000302`(3e01) id 0x14 placement (duct vs cabinet).
- Vacation: re-run with a longer window and watch whether `00041F` setpoints
  are eventually rewritten (period boundary?), and whether cooling actually
  sheds when zone temp < vacation max.
- 3e01 `000428` bytes[48..49] toggle `0000`↔`4345` ("CE"?) and [33..34]
  `83e5` — 4 distinct values all day, unexplained.
