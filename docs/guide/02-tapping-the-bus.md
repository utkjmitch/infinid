# Tapping the bus

## What a tap is

The ABCD bus is four low-voltage wires — A and B carry data, C and D carry
power — daisy-chained between the wall control and every other device on
the system (air handler, outdoor unit, zone/damper controller, remote
sensors). A "tap" means landing two more wires, A and B, on the same
terminals some existing device already uses. This adds a passive listener
to the bus. It does not add a new bus segment, does not remove or move any
existing wire, and does not change how any existing device behaves.

## Where people tap

The most common tap point is the terminal strip at the air handler or
furnace, where the zone/damper control board already lands A/B/C/D. That
terminal strip is normally reachable without disturbing any other
connection — you're adding a second pair of wires to terminals that already
have one pair on them.

## We do not provide wiring instructions

Landing wires on a live terminal block inside HVAC equipment is a physical
task with real electrical safety considerations, and it varies by
equipment model, panel layout, and local code. This project does not
document how to do it. For the physical connection itself, use the
Infinitude project's wiki, which covers it in more detail than belongs
here: https://github.com/nebulous/infinitude/wiki

If any part of that process is unfamiliar to you — identifying terminals,
working inside a panel that also carries line voltage, isolating power
correctly — stop and hire a professional. An HVAC tech can land two wires
on a terminal block in a few minutes, and getting it wrong risks damaging
equipment that costs a lot more than a service call.

## What success looks like

Once the tap is landed and the breaker is back on, check two things —
neither is sufficient on its own, but both should hold:

- The HVAC system itself is running exactly as it did before you touched
  it — no new faults, no change in behavior. The tap is passive; if
  anything changed, something is wrong with the physical connection, not
  with infinid.
- The USB adapter shows up on the host as a serial device. On Linux
  (including Home Assistant OS), check:

  ```
  ls /dev/serial/by-id/
  ```

  You should see an entry for your adapter. That path is what you'll hand
  to infinid in the next guide.

The adapter enumerating only proves the USB side is working — it says
nothing about whether A and B are actually landed correctly on the bus.
If nothing shows up, that's a USB problem to chase first. If it does show
up, the real test of the tap itself is whether infinid sees real frames
once you point it at that path, which is the next guide.
