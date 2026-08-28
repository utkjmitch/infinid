# Decode workflow

This is the method that produced every verified register in
[protocol-tables.md](../protocol-tables.md). It doesn't require reading
Go code or understanding the frame format in detail — it requires a
capture, a clock, and the discipline not to call something "decoded"
until it's actually confirmed.

## The method

1. **Capture continuously.** Start infinid capturing to a file (see
   [first capture](03-first-capture.md)) and leave it running through
   whatever you're about to do at the panel.

2. **Change exactly one thing, then look forward from that wall-clock
   time.** Pick a single, specific action — set a zone's cooling setpoint,
   switch fan speed, put the system in a mode it wasn't in — and note the
   time you did it. Bus writes lag the labeled action, not the other way
   around: expect the first related write roughly 13-21 seconds after your
   timestamp, with downstream effects (dependent registers, feedback)
   continuing to show up out to around 40 seconds (slower feedback, e.g.
   airflow, can trail by ~80 s — see the labeled-session offsets in
   [experiments/2026-08-12-labeled-session.md](../experiments/2026-08-12-labeled-session.md)).
   Sub-second precision
   doesn't matter; getting the direction right does — look forward from
   your timestamp, and do write down which clock (and which timezone)
   you're using.

3. **Run `businspect` lenses over the window.** `businspect` reads a
   capture file and gives you a few ways to look at it:

   - `businspect tables -in capture.jsonl` — inventory of every register
     seen on the bus and how often it's polled. Good for finding out what
     exists before you go looking for anything specific.
   - `businspect timeline -reg <hex> [-owner <4-hex>] -in capture.jsonl`
     — every observed payload change for one register over time, optionally
     narrowed to one owning device address. This is where you watch a
     register's bytes move around your labeled instant.
   - `businspect diff -at <RFC3339-time> [-window 2m] -in capture.jsonl` —
     a before/after diff across every register, centered on one instant.
     Useful when you don't yet know which register moved and want the tool
     to surface candidates.

4. **Form a byte-layout hypothesis, then verify it against a *second*,
   independent ground truth.** Seeing a byte change at roughly the right
   time is a hypothesis, not a decode. Confirm it against something else:
   the panel's own display of the same value, a known temperature read
   some other way, or an arithmetic relationship that has to hold — for
   example, superheat equals suction temperature minus saturation
   temperature, so if you can pin both independently, the arithmetic
   either checks out or it doesn't.

5. **Only then call it decoded.** A register earns a place in
   `protocol-tables.md` as a verified layout — and only then does infinid
   ship a typed decoder for it — once step 4 has actually happened. Read
   [ADR-0001](../adr/0001-verified-only-decoders.md) for why this project
   holds that line: prior-art decoders exist for far more registers than
   infinid decodes, and register semantics have been shown to drift across
   firmware generations. A plausible-but-wrong decode silently poisons
   Home Assistant history, which is worse than reporting nothing for that
   field.

## A worked example

This project's decode of the "hold until" timed-hold field is a clean
instance of the whole method. The panel was put into a timed hold with an
explicit end time ("hold until 8:00 PM"), and the wall-clock time of that
action was logged (09:13 local). A capture of the same window showed a
register write carrying the two-byte value `0x4BD2`.

That's a hypothesis: some encoding of "time until 8:00 PM." From 09:13 to
20:00 is 647 minutes; `0x4BD2` in decimal is 19410; 19410 divided by 647
is exactly 30. That arithmetic is suggestive, but on its own it's
circular — it assumes the tick-to-minute relationship it's trying to
prove, since 647 minutes and 30 ticks/minute were both chosen to make the
numbers line up.

The actual independent confirmation came from watching the field on its
own, without reference to the 647-minute total: the same session's
periodic rebroadcasts showed the value stepping down by `0x1E` (30) every
cycle, roughly 61 seconds apart — `d2 → b4 → 96 → 78 → 5a → 3c → 1e → 00`,
with the byte above it decrementing by one wherever the low byte borrowed
past zero. Losing 30 ticks in about 61 seconds pins one tick at very close
to 2 seconds, using nothing but the observed decrement rate — no wall
clock, no labeled end time. That result and the 647-minute arithmetic
agree, and it's the agreement between those two independently-derived
numbers, not either one alone, that makes this a verified decode rather
than a guess that happened to look plausible.

The full evidence chain for this decode — the labeled hold, the payload,
and the decrement observation above — is in
[experiments/2026-08-13-wall-control-session.md](../experiments/2026-08-13-wall-control-session.md).
That session also left one thing open: whether the countdown actually
reached zero and cleared cleanly, or just kept running past the panel's
own hold-clear. [experiments/2026-08-23-longitudinal-findings.md](../experiments/2026-08-23-longitudinal-findings.md)
resolved it by capturing two full natural expiries, both clearing the
countdown and reverting setpoints to schedule in the same frame.
