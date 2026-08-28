# infinid

Local decoding — and eventually control — of Carrier Infinity / Bryant Evolution
communicating HVAC systems, from any Linux box with a ~$12 USB RS-485 dongle
wired to the ABCD bus.

`infinid` is the Pi-hosted sibling of
[InfinitESP](https://github.com/nebulous/infinitesp) (ESP32/ESPHome). If you'd
rather run on a microcontroller, use that. If you have a Raspberry Pi or any
Linux machine near your equipment, this is for you.

## Status

**v1 (in development): read-only.** Passively decodes zone
temperatures/humidity/setpoints, damper positions, blower RPM/CFM, and
outdoor-unit diagnostics from bus traffic, and publishes everything to Home
Assistant via MQTT discovery. Fault history, filter life, and system mode
are not on the passive bus at all — they require enabling SAM reads
(`-sam`, off by default). v1 has no write path at all — it cannot command
your equipment. The only optional transmission (`-sam`, off by default)
issues read requests and nothing else, and is physically unable to
construct anything but reads.

v2 will add setpoint/mode writes via SAM emulation, gated on validated decode.

## What you get

- **Home Assistant entities via MQTT discovery**: per-zone temperature,
  humidity, setpoints, fan mode, hold state, and damper position; system
  diagnostics (compressor stage/RPM, airflow, static pressure, blower watts,
  suction pressure, coil/discharge temps); runtime and cycle counters; filter
  life; daemon health (bus liveness, frames/min, fault summary). The entity
  ids are a frozen, additive-only contract: [docs/MQTT-CONTRACT.md](docs/MQTT-CONTRACT.md).
- **An event journal**: fault lifecycle (appear, clear, panel reset), device
  liveness, and power-outage classification (was that gap a daemon restart or
  did the HVAC lose power?), as append-only JSONL.
- **A REST debug surface** (read-only, unauthenticated, LAN-only by intent):
  `/status`, `/state`, `/frames` (raw ring buffer), `/events` — the decode
  workbench.
- **A capture pipeline**: every frame to JSONL for offline analysis with the
  bundled `businspect` tool.

Staleness is never hidden: silent bus → entities go unavailable; stale or
vanished fields publish as unknown, not as frozen last values.

## Quick start

Bare Linux:

```sh
go build ./cmd/infinid
./infinid -serial /dev/serial/by-id/<your-adapter> \
  -capture capture.jsonl -journal events.jsonl \
  -mqtt-broker tcp://broker:1883 -mqtt-user infinid \
  -zone-names bedrooms,living_room,basement
```

The MQTT password travels via the `INFINID_MQTT_PASS` environment variable,
never a flag. Home Assistant OS: copy `deploy/haos-addon/` to
`/addons/infinid`, install from the local add-on store, and set the `serial`
option to your adapter's by-id path.

## Decode your own system

Register layouts vary by firmware generation; this project ships only
byte-verified decoders (see
[ADR-0001](docs/adr/0001-verified-only-decoders.md)) and archives everything
else as your contribution surface. The community guide walks the whole path —
equipment, tapping the bus (linked guides, no original wiring instruction),
first capture, verification, decode workflow, contributing:

- Start here: [docs/guide/00-overview.md](docs/guide/00-overview.md)
- Agent-assisted: point your coding agent at
  [skills/decode-your-infinity-bus/](skills/decode-your-infinity-bus/SKILL.md)

The verified register map lives in
[docs/protocol-tables.md](docs/protocol-tables.md).

## Heritage & credit

- Frame codec (framing/checksum/serial handling) ported from
  [acd/infinitive](https://github.com/acd/infinitive) (MIT).
- Protocol table knowledge derived from
  [nebulous/infinitesp](https://github.com/nebulous/infinitesp) and the
  [infinitude](https://github.com/nebulous/infinitude) lineage.

This project interacts with a proprietary bus protocol via reverse-engineered
information. It works, but no guarantee or warranty is provided — use at your
own risk to your HVAC system and yourself.
