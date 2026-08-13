# infinid Phase 2 — Decode, Publish, Guide (Design)

**Date:** 2026-08-13 · **Status:** draft (awaiting grill) · **Parent:** the Phase 1
foundation (bus/capture/inspect, shipped v0.1.1) and the original daemon design
(2026-08-11, approved). Phase 2 turns the verified register map into live HA entities
and ships the community "decode your own system" skill.

**Source of truth for all decoders:** the dated *verification* sections of
[`docs/protocol-tables.md`](../../protocol-tables.md). Only byte-verified layouts get
typed decoders. Everything else stays on the archive-unknown path.

**Posture (unchanged from v1):** read-only. No command topics exist; the daemon
physically cannot write to anyone's equipment. Active SAM *reads* are the only
transmissions, and they are optional (`-passive-only` disables them entirely).

---

## 1. `protocol` package — verified-only decoders

A registry keyed by `(device address, register)` mapping frames to typed readings.
Decoders ship for exactly the registers verified live on 2026-08-12/13:

| Register | Decodes to |
|---|---|
| `000202` / `000203` | bus clock (hour/min/weekday; day/month/year) |
| `000302` (TLV) | temps ×16: OAT `0x11`, coil `0x12`, LAT `0x14`, suction `0x30`, discharge `0x45`, superheat `0x4A` |
| `000303` | suction pressure PSIG (`[2-3]/16`) |
| `000304` | line voltage (`[7]`) |
| `000305` | commanded CFM (`[4-5]`), fire stage (`[0]`), cool-demand flag (`[2]`) |
| `000306` | RPM (`[1-2]`) / CFM echo (`[3-4]`) |
| `000308` / `000319` | damper positions, one byte per zone, `0x00–0x0F` (write + ~10–15 s mirror) |
| `000310` / `000311` | KV counter tables (key u8 + u24 BE): cycle counts / run hours per stage, blower, power |
| `000413` | measured CFM (`[0-1]`), RPM (`[2-3]`), static pressure float32 in-wc (`[4-7]`), blower watts float32 (`[8-11]`) |
| `000420` | environment push to sensors: OAT ×16 (`[6-7]`), RH % (`[10]`), local hh:mm (`[18-19]`) |
| `00041F` | zone setpoints: cool °F (`[7]`), heat °F (`[6]`), fan enum (`[5]`), timed-hold marker (`[1]=0x18`) + remaining 2-second ticks (`[3-4]` u16 BE), indefinite-hold bit (`[0]` bit 7) |
| `000604` / `000605` / `00060E` | compressor: target/actual RPM + per-stage RPM tables; commanded stage float32 + mode flag (`[4]`); actual stage (`[0]`) |
| `000625` | power-like analog u16 (`[0-1]`) |

Rules:

- **Unknown or unverified → archived, counted, never an error.** The Phase 1 ring
  buffer and capture stream remain the contribution surface.
- A decoder that sees a frame shorter than its layout logs once and archives — no
  partial guesses.
- Readings are typed values `{source addr, register, field, value, unit, timestamp}`;
  the package has zero knowledge of zones, MQTT, or state.
- **Every decoder ships with golden-frame tests cut from the real capture corpus**
  (the same JSONL the verification sessions produced). No hand-invented fixtures.

## 2. `state` package — assembly + SAM read scheduler

Assembles `SystemState` (zones, equipment, counters, clock) from two feeds:

- **Passive snoop** of the wall control's own polling — covers everything in §1.
- **Active SAM reads** (source `0x92`, read-only register requests; ACK behaviour
  proven live) for state that never appears on the bus passively:
  1. the wall control's *own zone* setpoints (on zoned systems the panel's zone is
     internal — its setpoint is never broadcast),
  2. fault / event history,
  3. filter life.

Scheduler design (conservative by construction):

- Sequential — one outstanding read at a time, minimum spacing between reads (default
  ≥ 2 s), never interleaved into another device's transaction window.
- Cadence (decided in grill): own-zone setpoints every 10 s fixed — next to the wall
  control's own 1 s polling this is negligible airtime, and it keeps the panel's own
  zone as fresh as the passively-snooped ones; fault history and filter life hourly
  (plus one read at startup).
- NACK / timeout → exponential backoff per register; repeated failure marks the field
  unavailable, never retries hot.
- **`-passive-only` flag disables all active reads.** This is the recommended first
  posture in the community guide: prove your tap and your decode before your daemon
  transmits a single byte.
- Per-field staleness tracking; a field past its horizon is reported unavailable
  (never stale-as-fresh).

**Discovery precondition (explicit plan task, not an assumption):** the exact
registers for own-zone setpoints, fault history, and filter life are *not yet
verified* — they are wall-control-local. Phase 2 includes a bounded discovery task:
live SAM probes of candidate registers with `businspect diff` analysis, validated
against known panel ground truth (our fault log shows codes 171/172 on 07/28/26; the
filter page shows a known %). Decoders for these three are written only after that
task verifies layouts, same standard as everything else. Discovery is
targeted-then-bounded-sweep (decided in grill): probe the prior-art candidate
registers first; if they NACK or decode wrong, sweep a defined window of the wall
control's table space with spaced read-only probes (ACK/NACK is itself signal),
analyzed offline. "Not yet decoded" is the outcome of an exhausted bounded search,
not a shallow one. Ground truth for validating own-zone setpoint and filter-life
decodes comes live from the incumbent cloud integration's entities; fault-history
decode validates against the operator's panel event log.

### 2a. Event journal — faults, outages, liveness (decided in grill)

An append-only JSONL journal on disk (next to the capture file, survives restarts)
recording the unit's service history — the artifact you hand a technician:

- **Faults.** The panel's last-10 event list is a *source, not the store*: the
  hourly SAM read is diffed, new entries become journal records with code, text,
  first-seen, last-seen, cleared-at, and best-effort resolution cause
  (`self_cleared` / `manually_cleared` / `unknown` — the daemon itself never clears
  anything; v1 is read-only). If live alarm traffic is verified on the bus, it
  feeds near-real-time active-fault state; until then active-state comes from the
  hourly diff.
- **Per-device liveness.** The wall control round-robin polls every bus node ~1 s;
  `state` tracks last-seen per bus address, distinguishing *whole bus silent*
  (HVAC power off or tap failure) from *one device missing* (equipment problem →
  `device_lost` / `device_recovered` events) from healthy.
- **Outage classification via power-on counters.** On startup the daemon reads the
  verified power-on cycle/hour counters (KV keys `2b`/`2c`) and compares to the
  last journaled values: counter unchanged → the HVAC never lost power (the gap
  was ours — host reboot); counter incremented → the HVAC genuinely lost power
  during the gap. Combined with the host's own restart timing this separates
  house-wide outage / HVAC-only power loss / monitoring-only gap — written
  retroactively at power-up, so even unwitnessed outages get classified. No
  battery backup required for a correct history.

## 3. `mqtt` package — HA discovery publisher

Exporter DNA (proven in production elsewhere): retained discovery + retained state,
LWT availability (bus silence or daemon death ⇒ `offline`), change-detection with a
periodic heartbeat republish, additive-only contract.

**Device grouping (decided in grill):** one `infinid` HA device holds diagnostics,
counters, and daemon health; each zone gets its own HA device holding that zone's
sensors. Users think in zones, not bus addresses — no per-bus-node devices.

Entity surface:

- **Zone identity (decided in grill):** zone *count* is autodetected from bus
  traffic — per-zone damper bytes and zone-sensor transactions reveal which zone
  indexes are live; zones appear as discovered (additive, never removed at runtime).
  Config supplies *names only*: an ordered `zone_names` list mapping index → name,
  defaulting to `zone_1..zone_N`, validated at startup as entity-id-safe slugs
  (lowercase, underscores). Names are cosmetic; indexes are the truth.
- **Diagnostics — these 12 ids verbatim** (a consuming dashboard already binds them):
  `sensor.infinid_compressor_stage`, `_compressor_rpm`, `_supply_cfm`, `_blower_rpm`,
  `_static_pressure`, `_blower_watts`, `_suction_pressure`, `_outdoor_coil_temp`,
  `_discharge_temp`, `_damper_bedrooms`, `_damper_living_room`, `_damper_basement`.
  Damper ids follow `sensor.infinid_damper_<zone>` from the configured zone names —
  the author's deployment sets `zone_names: bedrooms,living_room,basement` to
  reproduce these ids exactly.
- **Per zone:** `sensor.infinid_zone_<name>_{temp,humidity,cool_setpoint,heat_setpoint,fan_mode,hold,hold_remaining,damper}`.
  Sensors only — no MQTT climate entities in Phase 2. A read-only thermostat card
  that ignores taps is worse than no card; climate stays with the incumbent
  integration until v2 writes are validated.
- **Counters:** cycle counts and run hours from the KV tables (long-term statistics
  candidates).
- **Faults & liveness:** `sensor.infinid_last_fault` (state = code; attributes:
  text, timestamp, occurrence count), `binary_sensor.infinid_fault_active`,
  `sensor.infinid_fault_count`, `binary_sensor.infinid_bus_online` (per-device
  last-seen as attributes). The full journal is served by REST and the file
  itself, not MQTT.
- **Daemon health:** availability, frames/sec, CRC-fail rate, unknown-frame count,
  last-frame age, SAM read failure count.

Config via flags/env (add-on passes options through): broker host/port, username,
password, base topic, zone names. **Never a real host or credential in-repo.**
Contract documented in `docs/MQTT-CONTRACT.md`; additive changes only.

## 4. `rest` package — minimal debug

`GET /status` (health + counters), `GET /state` (full `SystemState` JSON),
`GET /frames` (ring-buffer dump), `GET /events` (the event journal — the
technician export). Read-only, no auth (bind localhost by default;
add-on exposes on the container network only). Unchanged scope from the parent
design.

## 5. Validation comparator (operator-side, not in this repo)

The trust harness — side-by-side logging of infinid values vs the incumbent cloud
integration across days of real HVAC activity — is deployment-specific: it references
an operator's private entity ids. It lives in the operator's own HA config (ours does;
a template package computing per-field deltas). This repo documents the *method* in
the guide (§6) so others can build their own. A field is trusted when it tracks
through change, not just at rest; full trust remains the gate for v2 writes.

**The comparator is scaffolding, not architecture (decided in grill):** the cloud
integration runs in parallel only during the validation window. The end state is
fully local — the cloud reference disappears, so nothing in infinid may depend on
it: no code path, no config key, no entity assumption. Validation-by-comparison is
a documented *method* applied from outside, and it retires with the cutover.

## 6. The community skill — "decode your own system"

Two layers, one content source:

**`docs/guide/` — human-readable series:**

1. `00-overview.md` — what this is, what read-only means, the safety/liability
   frame: this project provides software and a method, not electrical instruction;
   kill power at the breaker before touching equipment; the ABCD bus is low-voltage
   but lives inside HVAC equipment — if any step is unfamiliar, hire a professional;
   MIT license, no warranty, your equipment is your responsibility.
2. `01-equipment.md` — "what the author used" framing: the exact RS-485 dongle and
   host that worked for us, stated as fact, not endorsement; equipment *categories*
   and search terms for alternatives; link-outs to community resources (Infinitude
   wiki and related guides) for anything physical.
3. `02-tapping-the-bus.md` — what a tap *is* (two low-voltage data wires, A/B), what
   a good result looks like (clean frames at 38400 8N1), and **links to the existing
   community wiring guides rather than our own step-by-step**. Explicit: we do not
   provide wiring instructions.
4. `03-first-capture.md` — run the daemon in capture mode, confirm frame rate and
   CRC health, what healthy bus traffic looks like.
5. `04-decode-workflow.md` — the labeled-experiment method that produced our map:
   change exactly one thing at the panel, timestamp it, `businspect diff`/`timeline`
   the capture, verify bytes against ground truth before believing a layout.
6. `05-contributing.md` — archive-unknown counters, how to sanitize and share
   captures, how to submit a verified table layout.

**`skills/decode-your-infinity-bus/SKILL.md`** — the same journey in the open Agent
Skills format (plain markdown + name/description frontmatter) so any agent — Claude,
Copilot, Gemini, whatever comes next — can load it and walk a user through the guide
interactively. One skill covers the whole journey (decided in grill): staged with
explicit checkpoints — the agent verifies each stage's exit criterion (e.g. "clean
frames at the expected rate") before advancing, and can resume mid-journey ("I
already have a tap"). The skill references the guide files rather than duplicating
them.
Hard rules stated in the skill itself, for the agent to enforce:

- Read-only always; recommend `-passive-only` until the user's decode is verified.
- Never generate physical wiring instructions; point to the guide's link-outs and
  recommend a professional when in doubt.
- Never suggest register writes, timing changes, or "try sending" experiments.
- Verify each stage (tap → capture → decode) before advancing the user to the next.

## 7. HAOS add-on v0.2.0

Options schema grows: MQTT host/port/username/password, base topic, zone names, SAM
enable (default off ⇒ passive-only), REST port. Options map to daemon flags/env in
`run.sh`. Same discipline as Phase 1: `INFINID_REF` SHA pin bumped in lockstep with
the add-on `version` (Supervisor caches by version), multi-stage golang→alpine build,
`uart: true`, by-id serial path.

## 8. Testing & CI

- Golden-frame decoder tests from real captures for every §1 register.
- State assembly tests: feed a recorded frame sequence, assert the resulting
  `SystemState` including staleness transitions.
- SAM scheduler tests with a fake bus: cadence, single-outstanding, backoff, and the
  `-passive-only` guarantee (zero writes to the transport — asserted, not assumed).
- MQTT publisher tests against an in-memory broker interface: discovery payloads,
  retained flags, LWT config, contract ids byte-exact.
- CI unchanged (gofmt gate, vet, `test -race`); guide/skill files are prose, linted
  only by review.

## Out of scope (Phase 2)

Writes of any kind (v2, gated on the trust harness); MQTT climate entities;
schedule/vacation/profile decoding; multi-DCM (0x61) systems beyond
written-to-spec-community-validation-needed; non-HA MQTT consumers beyond the
documented contract; translations of the guide.

## Risks

- **SAM-read registers unproven** for the three local-only fields — bounded discovery
  task with a no-block fallback (§2).
- **Active reads on a live bus** — mitigated by sequential+spaced scheduler, backoff,
  default-off in the add-on, and `-passive-only`; a misbehaving scheduler cannot
  write state, only read.
- **Contract drift vs consumers** — the 12 diagnostic ids are frozen verbatim;
  everything else is additive per `MQTT-CONTRACT.md`.
- **Guide liability** — no original wiring instructions, "author's setup" framing,
  link-outs, prominent disclaimers, MIT no-warranty. The skill instructs agents to
  hold the same line.
