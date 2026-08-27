# First capture

## Install

Two paths, pick one:

**Build from source** (any Linux host):

```
go build ./cmd/infinid
```

**HAOS local add-on** (Home Assistant OS): copy `deploy/haos-addon/` into
`/addons/infinid` on your HAOS host, then install it from Settings →
Add-ons → Add-on Store → local add-ons. Set the `serial` option to your
adapter's by-id path — the shipped default is a placeholder
(`/dev/serial/by-id/CHANGE-ME`) and the add-on will not find your adapter
until you replace it. The `capture_max_mb` option controls how large the
capture file is allowed to grow before recording stops; the default is
1024 (1 GiB), and it's worth raising if you plan a long unattended capture
for the decode workflow in the next guide.

## First run

Building from source, point infinid at your adapter and start a capture:

```
./infinid -serial /dev/serial/by-id/<yours> -capture capture.jsonl -verbose
```

`-verbose` logs every frame while you're getting oriented; drop it for
longer runs; without it infinid logs one stats line per minute instead.

## What healthy looks like

A steady stream of frames, with a rate somewhere in the hundreds to
roughly 1,500 frames per minute on a zoned Touch-generation system — exact
numbers vary with zone count and equipment activity. Resync bytes (frames
the reader had to skip past to find the next valid frame boundary) should
settle near zero shortly after startup; a handful during the first few
seconds while the reader finds frame boundaries is normal.

## What unhealthy looks like

**Zero frames.** Check that A and B aren't swapped — reversed polarity on
an RS-485 pair typically reads nothing rather than garbage, since the
differential signal never resolves. Swap the two wires at your adapter and
try again.

**Constant resyncs, never settling near zero.** Usually a loose or marginal
connection at the tap — reseat the wires and check for a solid landing on
the terminal block.

**Frame rate at a fraction of normal, with resync bytes climbing
continuously instead of settling.** This is the signature of two processes
sharing the same serial port — something else has also opened the
adapter's device node, and incoming bytes are being split between readers,
so neither one sees a complete, continuous frame stream. This happens most
often when another add-on, or a stray standalone `infinitive`/Infinitude
install, is also configured against the same device. Only one process may
read a given serial device at a time; find and stop the other reader. This
is also a reason to keep transmission off by default: a second process
that's actively writing to the bus — not just reading — can make the wall
control log system-monitor communication faults, on top of corrupting your
capture. One device, one reader, and the passive default stays the safe
choice until you have a specific reason to enable SAM reads.

Captured frames land in the file you named as one JSON object per line
(JSONL) — this is the raw material for everything in the next guide.

## REST note

infinid exposes a read-only debug port at 8099 (`GET /state`, `GET
/events`, and similar). It is read-only by design and will stay that way,
but it is **unauthenticated** — fine to leave reachable on a trusted LAN,
not fine to expose further. If you're at all unsure about your network,
disable the host port mapping in the add-on's Network panel so the port
stays container-internal. Never port-forward it to the internet.
