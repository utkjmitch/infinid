# Decode your own system — overview

infinid is software and a method for decoding the ABCD communicating bus
used by Carrier Infinity and Bryant Evolution HVAC systems, running locally
on a Raspberry Pi or any other Linux box, with no cloud account and no
proprietary hardware. It reads the bus passively, turns the traffic it
understands into Home Assistant entities over MQTT, and keeps a record of
everything it doesn't yet understand so that record can become the next
verified register.

## Read this first

This project provides **software and a method — not electrical instruction.**

- **Nothing here is professional HVAC, electrical, or safety advice.**
- Your HVAC equipment is your responsibility. This software is MIT-licensed
  and comes with **no warranty of any kind.**
- **Kill power at the breaker** before opening equipment or touching any
  wiring. The ABCD bus is low-voltage, but it lives inside equipment that
  also carries line voltage.
- If any physical step is unfamiliar, **stop and hire a professional.**
  An HVAC tech can land two wires on a terminal block in minutes.
- Everything here is **read-only**. infinid v0.x cannot write to your
  equipment: no command topics exist, and the only optional transmission
  (SAM reads) requests data and nothing else. Start in passive-only mode
  (the default) and stay there until your decode is verified.

## What you get

Once a tap is in place and the daemon is running, you get three things: a
set of Home Assistant entities discovered automatically over MQTT (zone
temperatures, setpoints, damper positions, blower and compressor telemetry,
runtime counters, fault status — the full list is in
[MQTT-CONTRACT.md](../MQTT-CONTRACT.md)), an event journal recording fault
history and equipment liveness over time, and a decode workbench —
`businspect`, a small set of command-line lenses over your own captured
traffic — for pushing the verified register map further on your equipment.

## The path

The guides in this directory walk the same path this project's own decode
work followed, in order:

1. [Equipment](01-equipment.md) — what you need to buy.
2. [Tapping the bus](02-tapping-the-bus.md) — landing a listener on the wire.
3. [First capture](03-first-capture.md) — installing infinid and confirming
   you're seeing real traffic.
4. [Decode workflow](04-decode-workflow.md) — the labeled-experiment method
   for turning captured bytes into a verified register.
5. [Contributing](05-contributing.md) — sending what you find back upstream.

## Register layouts vary — verify your own

The register map in [protocol-tables.md](../protocol-tables.md) is built
from prior open-source decoders plus this project's own verification work,
and it says explicitly, per section, what has been confirmed against a real
bus and what hasn't. Firmware generations differ: a register that means one
thing on a Touch-era system may carry something else, or nothing, on
another. infinid only ships a typed decoder for a register once its byte
layout has been verified against ground truth — see
[ADR-0001](../adr/0001-verified-only-decoders.md) for why. Your system may
already be covered by an existing verification, or it may need its own pass
using the method in guide 04. Either way, nothing here assumes your bus
looks exactly like the one this project was built against.
