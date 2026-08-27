# Shared-port incident — 2026-08-27 (retired SAM emulator, live capture)

A leftover `acd/infinitive` install — an earlier, retired attempt at SAM
emulation on the author's system — survived a host reboot on 2026-08-24 via
its own boot-on-startup configuration, came back up, and began transmitting
on the bus as SAM address `0x9201`, while sharing the same serial device
node as the passive infinid capture daemon. Neither process expected the
other, and Linux does not enforce exclusive access to a serial device by
default, so incoming bytes were split unpredictably between both readers.

## Effects observed

- Capture frame rate dropped from the system's normal ~1,500 frames/min to
  ~296 frames/min — the bus's own traffic didn't change; every other
  frame's bytes were simply going to the other process instead.
- Resync bytes climbed continuously instead of settling: roughly
  17,000/min sustained over the three days before it was caught, ~82
  million bytes total.
- The wall control answered the impersonator's `3B02` reads with
  all-zero payloads — a live device serving zeros to a second, unexpected
  reader claiming the SAM's address.
- The wall control logged fault code 146, "system monitor alert," twice
  during the window (2026-08-25 and 2026-08-27), consistent with the panel
  treating the extra, uncoordinated transmitter as a fault in its SAM
  communication rather than something to do with infinid, which never
  transmits in passive mode.

## Resolution

Stopping the second process restored the capture rate to ~1,550
frames/min within about a minute, and resync bytes went flat immediately.
No change to infinid's own configuration was needed; the fix was removing
the other reader.

## Lessons

- One device, one reader. Nothing else may hold the serial device open
  while infinid (or any bus reader) is running against it.
- A retired transmitter must be uninstalled, not merely stopped. This one
  came back specifically because its boot-on-startup configuration
  survived a host reboot — a process that is stopped but not disabled or
  removed will return exactly like this after the next restart.

This incident is the source for the third failure mode documented in
[03-first-capture.md](../guide/03-first-capture.md).
