# Decode workflow

This is the method that produced every verified register in
[protocol-tables.md](../protocol-tables.md). It doesn't require reading
Go code or understanding the frame format in detail — it requires a
capture, a clock, and patience about calling something "decoded" before
it's actually confirmed.

## The method

1. **Capture continuously.** Start infinid capturing to a file (see
   [first capture](03-first-capture.md)) and leave it running through
   whatever you're about to do at the panel.

2. **Change exactly one thing, and write down the wall-clock time.** Pick
   a single, specific action — set a zone's cooling setpoint, switch fan
   speed, put the system in a mode it wasn't in — and note the time you
   did it. Bus writes tend to lead the panel's own display by roughly
   15-25 seconds, so don't worry about sub-second precision; do write down
   which clock (and which timezone) you're using.

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

That's a hypothesis: some encoding of "time until 8:00 PM." The
independent confirmation is arithmetic, not a second reading of the same
display. From 09:13 to 20:00 is 647 minutes. `0x4BD2` in decimal is 19410.
19410 divided by 647 is exactly 30 — so the field is remaining hold time
in 2-second ticks (30 ticks per minute), and 647 minutes to the labeled
end time times 30 gives 19410, which is exactly `0x4BD2`. Two independent
routes — the labeled action's wall-clock time, and the arithmetic
relating ticks to minutes — land on the same number. That's what makes it
a verified decode rather than a guess that happened to look plausible.
The full evidence chain, including the field's later confirmation against
natural hold expiry, is in
[experiments/2026-08-13-wall-control-session.md](../experiments/2026-08-13-wall-control-session.md).
