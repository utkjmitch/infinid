# infinid

Local decoding — and eventually control — of Carrier Infinity / Bryant Evolution
communicating HVAC systems, from any Linux box with a ~$12 USB RS-485 dongle
wired to the ABCD bus.

`infinid` is the Pi-hosted sibling of
[InfinitESP](https://github.com/nebulous/infinitesp) (ESP32/ESPHome). If you'd
rather run on a microcontroller, use that. If you have a Raspberry Pi or any
Linux machine near your equipment, this is for you.

## Status

**v1 (in development): read-only.** Decodes zone temperatures/humidity/setpoints,
damper positions, filter life, blower RPM/CFM, and outdoor-unit diagnostics from
bus traffic, and publishes everything to Home Assistant via MQTT discovery
(sensors and position-only covers). v1 has no write path at all — it cannot
command your equipment.

v2 will add setpoint/mode writes via SAM emulation, gated on validated decode.

## Heritage & credit

- Frame codec (framing/checksum/serial handling) ported from
  [acd/infinitive](https://github.com/acd/infinitive) (MIT).
- Protocol table knowledge derived from
  [nebulous/infinitesp](https://github.com/nebulous/infinitesp) and the
  [infinitude](https://github.com/nebulous/infinitude) lineage.

This project interacts with a proprietary bus protocol via reverse-engineered
information. It works, but no guarantee or warranty is provided — use at your
own risk to your HVAC system and yourself.
