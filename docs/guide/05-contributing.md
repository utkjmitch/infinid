# Contributing

Three kinds of contribution move this project forward, and none of them
require writing Go.

## Unknown-register observations

infinid publishes a single running total of undecoded traffic
(`sensor.infinid_unknown_frames`). On its own that counter only tells you
that undecoded traffic exists on your bus and roughly how much — it's one
scalar, not a breakdown. To see which registers, how often, and from which
devices, run `businspect tables` over your own capture (see
[decode workflow](04-decode-workflow.md)); that's the actual map of
undecoded traffic. Even without a full decode, reporting which unknown
registers show up on your equipment (especially if your system is a
different generation or configuration than what's already covered in
[protocol-tables.md](../protocol-tables.md)) tells the project where to
look next.

## Verified layouts

If you've worked through the [decode workflow](04-decode-workflow.md) and
landed a byte-exact confirmation against independent ground truth, that's
a contribution on its own — the evidence bar is the same one this
project's own verification sections hold themselves to: cite the
capture window, the labeled action and its wall-clock time, the register
and byte offsets, and the independent confirmation that closed it. Look at
any of the dated verification sections in `protocol-tables.md` for the
shape a writeup should take.

## Equipment-generation notes

Register layouts and device addresses aren't fixed across every
installation. If your system uses a different device address for a
component than what's documented (this project's own air handler, for
example, answers at an address no prior source expected for that device
class), or a table's row layout doesn't match what's written down, that
mismatch is worth reporting even without a full re-decode — it tells
future readers where firmware or hardware generations diverge.

## Sanitizing captures

Bus frames don't carry credentials, account identifiers, or anything like
that — there's nothing to redact for secrets. But a capture is a record of
real usage: when setpoints changed, when a hold was set and for how long,
effectively when someone was home. Before sharing a capture excerpt, trim
it to just the window around the experiment you're documenting rather than
attaching a full day (or week) of raw traffic.

## Filing issues or PRs

Include three things: the capture excerpt covering the window in question,
the wall-clock time of whatever you labeled, and the independent ground
truth you observed (panel display value, known temperature, or the
arithmetic that confirms it). That's the same evidence shape used
throughout this project's own decode work, and it's what lets a
maintainer — or the next contributor — verify the claim rather than take
it on faith.
