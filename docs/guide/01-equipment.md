# Equipment

## What the author used

The author's system uses a DSD TECH SH-U11 USB RS-485 adapter, plugged into
a Raspberry Pi 5 running Home Assistant OS. That's it — no other hardware
was involved in building or verifying this project's decode.

This is stated as fact about one working setup, not an endorsement. It is
simply what worked on one system; plenty of other adapters and hosts will
work just as well, and the author has no relationship with DSD TECH.

## What matters when choosing an adapter

The ABCD bus is RS-485, half-duplex, using two data terminals (A and B —
sometimes labeled D+/D− or similar on an adapter). Any USB-to-RS-485
adapter with A/B screw terminals will work. The chip inside doesn't matter
much: FTDI and CH340-based adapters have both been used successfully by
people decoding this bus. Expect to pay somewhere in the $10-15 range —
this is a commodity part, not a specialty one.

## What matters when choosing a host

Any Linux box near the equipment works. The daemon that reads the bus
(`infinid`) is a single static binary with no runtime dependencies beyond a
USB serial port; it runs fine on a Raspberry Pi, an old laptop, a NUC, or a
VM with USB passthrough. If you're already running Home Assistant OS on a
Pi, the [add-on install path](03-first-capture.md) is the least additional
hardware.

## Search terms

If you're shopping, "USB RS-485 adapter" is the search term that will
surface the right category of part. You do not need anything that
advertises Modbus support specifically — Modbus is a different protocol
that happens to share the same electrical layer, and any adapter sold for
it works fine here too.

## What you do NOT need

- No SAM (System Access Module) hardware — SAM emulation, when infinid
  eventually supports it, is done in software.
- No cloud account of any kind.
- No Infinitude proxy or other bridging software running alongside infinid.
- No soldering. The tap is two wires landed on an existing terminal block
  (see [tapping the bus](02-tapping-the-bus.md)).
