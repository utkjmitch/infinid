# infinid MQTT Contract

Additive-only: entities may be added in any release; the ids below are
never renamed or removed. Consumers may bind to any id here.

## Availability

`infinid/availability` (retained): `online` / `offline`. Offline means the
daemon is down **or the bus has been silent for 60 s** — consumers must
treat entities as unavailable, never stale-data-as-fresh. Set as the LWT;
the publish loop also re-asserts it every cycle, so it self-heals after a
broker restart or retained-message wipe.

## Frozen diagnostic ids (the dashboard contract)

| Entity | Source register | Notes |
|---|---|---|
| `sensor.infinid_compressor_stage` | 00060E[0] | 0=off, 1-5 |
| `sensor.infinid_compressor_rpm` | 000604[2..3] | actual |
| `sensor.infinid_supply_cfm` | 000413[0..1] | measured |
| `sensor.infinid_blower_rpm` | 000413[2..3] | |
| `sensor.infinid_static_pressure` | 000413[4..7] | inH2O, 2 decimals |
| `sensor.infinid_blower_watts` | 000413[8..11] | W, whole watts |
| `sensor.infinid_suction_pressure` | 000303[2..3] | psi |
| `sensor.infinid_outdoor_coil_temp` | 000302@ODU id 0x12 | °F |
| `sensor.infinid_discharge_temp` | 000302@ODU id 0x45 | °F |
| `sensor.infinid_damper_<zone>` | 000319 per slot | % open (raw/15×100) |

`<zone>` comes from the zone-names config (index-ordered slugs; the
`-zone-names` flag today, the add-on's `zone_names` option once the HAOS
packaging lands); unset indexes name themselves `zone_<n>`.

Zone-naming caveats:

- **Renaming a zone mints new entity ids** (the slug is embedded in both
  topic and `unique_id`). The old ids' retained discovery configs and
  state stay on the broker, so HA keeps a duplicate, frozen entity set
  until those retained topics are cleared manually. Pick names once.
- The fallback slug feeds the same patterns as configured names, so an
  unnamed zone 5 yields `sensor.infinid_zone_zone_5_temp` (stuttered) and
  `sensor.infinid_damper_zone_5`. Ids are frozen the moment HA sees them;
  the stutter is documented rather than "fixed" for that reason.
- Do not configure a zone name that literally matches `zone_<n>` — it
  collides with the fallback id of the unnamed zone at that index.

## Per-zone entities

`sensor.infinid_zone_<zone>_{temp,humidity,cool_setpoint,heat_setpoint,fan_mode,hold,hold_permanent,hold_remaining}`
— all populated from passive decode of the wall control's own bus
traffic. Enabling SAM reads (`-sam`) adds exactly one MQTT-published zone
field: `hold_permanent` (the SAM 3B03 bitmap; `hold` is the timed hold
from 00041F — distinct signals, both publish). Every other SAM-sourced
zone value lands in `*_sam`-suffixed fields that are deliberately **not**
published to MQTT; they exist on the REST surface for decode validation
only, so passive and SAM decodes can be compared without duplicate HA
entities. Open validation-phase question: if the wall control's own zone
turns out to lack full passive coverage, publishing selected `_sam`
fields for that zone alone is a candidate additive change.

## System / equipment (additive)

`sensor.infinid_{outdoor_temp,supply_air_temp,suction_temp,superheat,line_voltage,system_mode}`
and `sensor.infinid_filter_life` (**remaining %** — the bus reports consumed %,
inverted at the contract boundary; REST `/state` shows the raw used value)
plus runtime counters
`sensor.infinid_{heat_stage1,heat_stage2,blower,cool}_{cycles,hours}` and
`sensor.infinid_{idu,odu}_power_cycles` (state_class total_increasing —
long-term statistics candidates). IDU (furnace) counters commit on a
~daily internal rollup, so same-day movement is not expected; the ODU
counts live.

## Faults & health

`sensor.infinid_last_fault` (text: `<code> @ <timestamp> (x<count>)`, or
`None` when no fault is active),
`sensor.infinid_fault_count`, `binary_sensor.infinid_fault_active`,
`binary_sensor.infinid_bus_online`,
`sensor.infinid_{frames_per_min,unknown_frames,sam_failures}`.
`sam_failures`, `unknown_frames`, and the REST `resync_bytes` counter are
daemon-lifetime running totals (they do not reset when the serial port
reconnects). The three fault entities are only meaningful when the event
journal is enabled (`-journal`); without it they read as no-faults, not
as unknown.

The full event journal (fault lifecycle, outage classification, device
liveness) is not an MQTT surface: `GET /events` on the REST port, or the
journal JSONL file itself.

**REST invariant:** the REST surface is read-only forever. If writes ever
exist (v2), they arrive via MQTT command topics behind broker auth — never
REST. This is what makes the unauthenticated debug port acceptable on a
trusted LAN; do not add POST/PUT handlers.

## Devices

One `infinid` hub device (diagnostics, counters, health) + one device per
zone (`infinid_zone_<n>`, via_device → hub).

## Staleness & retraction

State topics are retained; stale fields (per-field horizon exceeded)
publish `None` so HA shows unknown. A field that disappears from the
assembled state entirely (e.g. `hold_remaining` after a hold clears) is
retracted the same way — its topic receives a retained `None` rather than
freezing at the last value. Everything re-publishes on a 60 s heartbeat
(state and health each keep their own heartbeat clock), and the daemon
re-asserts all discovery + state after an MQTT reconnect, so a broker
that loses its retained store converges within a minute.
