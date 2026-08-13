# infinid

Local decoder (and later controller) for the Carrier Infinity / Bryant Evolution
ABCD bus. This glossary fixes the language used across specs, code, and the
community guide.

## Language

**Zone**:
One independently conditioned area the wall control manages, identified on the bus
by index (1..N).
_Avoid_: room, area

**Zone Index**:
The bus-level position of a zone (byte order in damper tables, sensor addressing);
the source of truth for zone identity.
_Avoid_: zone id, zone number (ambiguous with name)

**Zone Name**:
A cosmetic, operator-configured slug attached to a zone index for entity ids;
never inferred from the bus.

**Passive Snoop**:
Reading state solely from traffic other devices already exchange; the daemon
transmits nothing.

**SAM Read**:
An active read-only register request the daemon sends as source address 0x92,
mimicking Carrier's discontinued System Access Module.
_Avoid_: poll (too generic), write (never)

**Verified Register**:
A register whose byte layout was confirmed against ground truth in a dated
verification section of `docs/protocol-tables.md`; the only kind that gets a typed
decoder.
_Avoid_: known register, documented register (prior art ≠ verified)

**Archive-Unknown**:
The handling path for frames without a verified decoder: counted and preserved for
later analysis, never an error.

## Relationships

- A **Zone** is identified by exactly one **Zone Index** and displays at most one
  **Zone Name**.
- **Passive Snoop** and **SAM Read** are the only two feeds; only **SAM Read**
  transmits.
- A register moves from **Archive-Unknown** handling to a decoder only by becoming
  a **Verified Register**.

## Example dialogue

> **Dev:** "Zone 2 renamed itself?"
> **Domain expert:** "Zones can't rename themselves — a **Zone Name** only comes
> from config. If entities moved, the **Zone Index** mapping changed, which means
> the bus told us something new."

## Flagged ambiguities

- "zone number" was used for both index and display name — resolved: **Zone
  Index** (bus truth) vs **Zone Name** (cosmetic config) are distinct.
