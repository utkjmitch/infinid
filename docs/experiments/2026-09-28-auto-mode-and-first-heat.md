# Auto mode and the first heat calls — 2026-09-11 → 09-28

Two passive-only findings from two capture archives (~72M frames, 08-23 →
09-28; SAM reads were off for all of 09-11 → 09-28). Both are evidence notes under ADR-0001: they
narrow what a register means, and neither ships a decoder yet.

## 1. System mode is not on the passive bus (negative result)

On 2026-09-11 the operator deliberately switched all three zones from Cool to
Auto (heat_cool) at the wall control and asked whether infinid could see it.

It could not. A sweep of every register's modal payload on a Cool-only day
(09-05) against an Auto day (09-12) — 71 register streams — found no register
that carries system mode:

- `00041F` (zone config push to the remote sensors) is byte-identical across
  the change. It carries **both** the heat and the cool setpoint in every
  mode: heat 68 °F was present a week before Auto was ever enabled. The bus
  carries the parameters; the wall control keeps the interpretation.
- `000605` is byte-identical across the change.
- All 22 streams that did differ are accounted for: runtime counters, the
  date broadcast (`2001→f1f1 000203`[0] is the day of month), temperatures,
  humidity, suction pressure, damper position, refrigerant target, and
  outdoor-unit diagnostics.

**Consequence for v2 writes:** a heat-setpoint write is inert in Cool and can
fire the furnace in Auto, and the bus cannot tell which applies. The write
path needs an authoritative mode source (decoded 3B02, which still reads zero,
or an operator-declared mode), and must never infer mode from demand.

## 2. First Auto-mode heat calls: the furnace state machine

Cooling last ran late on 09-26. At **01:49 local on 09-27** (outdoor ~55 °F)
the system changed over by itself and ran six heat cycles through 08:56 —
matching the six-count jump in the furnace's stage-1 cycle counter exactly.

### The call, end to end (first cycle)

| Time | Frame | Change |
|---|---|---|
| 01:49:00 | `2001→5201 000605`[4] | 01 → 00 — equipment operating mode, cool → heat |
| 01:49:05 | `2001→3e01 000305`[0] | 00 → 01 — stage-1 fire command |
| 01:49:15 | `2001→6001 000308` | `0f00…` → `000f…` — damper command: zone 1 closed, zone 2 fully open |
| 01:49:36–56 | `6001→2001 000319` | damper mirror follows |
| 01:50:25 | `3e01→2001 000306` / `000316`[4..5] | airflow target `0x046e` = 1134 CFM |
| 01:59:04 | `000305` | fire command cleared |

So `000605`[4] tracks what the equipment is **doing**, not the system mode: it
reads "cool" throughout a cooling-season Auto day and flips only when a heat
call actually starts.

### `3e01 00041e` byte[17]: ignition sequence state

Across 914 changes on 09-27 (zero in the previous 15 days), byte[17] steps
through the same sequence every cycle:

| State | Dwell | Reading |
|---|---|---|
| 1 | — | idle |
| 5 | ~3 s | seen only on the first call after a long idle |
| 7 | ~15 s | pre-purge |
| 8 | ~15 s | |
| 9 | ~3 s | seen when state 5 is skipped |
| 10 | ~3–6 s | ignition |
| 11 | ~3 s | |
| 12 | ~37–40 s | flame proven, blower-on delay |
| 14 | ~8 m 42 s | steady heat |
| 15 | ~15 s | post-purge |
| 1 | ~2 min after 15 | idle (blower-off delay elapsed) |

- **Bytes [23] and [25]** are in-state timers: +3 every ~3 s, reset on each
  state change.
- **Byte [18]** behaves like an inducer level: 0 idle, 65 during purge, 77 or
  93 at ignition, 95 while running.
- Cycles ran on an exact **40-minute cadence** with ~10 minutes of burn —
  cycle-rate control, not thermostat bounce.

### Why this matters for the event journal

On 09-09, during a house power event, the same bytes went `01 → 00 → 01`
with the timer restarting. That is the furnace control board rebooting
through **state 0** — observed directly, while the bus itself never went
silent. The journal's outage classifier is gap-triggered and so logged
nothing; a watch for byte[17] entering 0 (plus a power-cycle counter bump) is
a direct, gap-independent power-event signal.

## Status

Evidence only. Promoting `00041e`[17]/[18]/[23]/[25] to a typed decoder needs
a second labeled observation — a heat-season panel session is planned
(`2026-09-28-fault-page-heat-session-plan.md`), where stage-2 fires and the
state-5 path can be labeled at the wall control.
