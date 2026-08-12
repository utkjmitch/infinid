# Carrier Infinity ABCD-Bus Protocol Table Reference

Register/table layouts mined from prior-art open-source decoders of the Carrier
Infinity RS-485 ("ABCD") bus, collected as the decode reference for infinid
Phase 2 (zone climate, dampers, filter life, equipment diagnostics, faults).

## Provenance and licenses

| Source | What was mined | License |
|---|---|---|
| [acd/infinitive](https://github.com/acd/infinitive) (Go) — `infinity/tables.go`, `infinity/api.go`, `infinity/frame.go`, `infinity/bus.go`, `infinity/conversions.go` | SAM/thermostat tables 3B02–3B06, op codes, write-flag protocol, air-handler/heat-pump snoop decodes | MIT © 2016-2023 Andrew Danforth |
| [nebulous/infinitesp](https://github.com/nebulous/infinitesp) (ESP32/ESPHome C++) — `components/infinitesp/infinitesp.h`, `infinitesp.cpp`, `text_sensor/infinitesp_text_sensor.cpp`, `binary_sensor/` | Register key catalog, 3B0x offset tables, zone-controller (damper) registers, IDU/ODU diagnostics, fault history 0x4202, comfort profiles | MIT © 2022 nebulous (plus a NOTICE disclaiming fitness for HVAC control) |
| [nebulous/infinitude](https://github.com/nebulous/infinitude) (Perl) — `lib/CarBus/Frame.pm`, `SAM.pm`, `IndoorUnit.pm`, `OutdoorUnit.pm`, `ZoneController.pm`, `SAM/ASCII.pm` | Declarative parsers for the same registers with byte-exact layouts, tabledef format, fault table, ODU float arrays | MIT © 2015 John Lifsey |
| [infinitude wiki](https://github.com/nebulous/infinitude/wiki) — [Infinity — interpreting data](https://github.com/nebulous/infinitude/wiki/Infinity---interpreting-data), [Infinity Framing Protocol](https://github.com/nebulous/infinitude/wiki/Infinity-Framing-Protocol), [Infinity — known devices](https://github.com/nebulous/infinitude/wiki/Infinity---known-devices) | Register map, per-device table listings, device addresses, exception codes, polling frequencies | wiki content of the MIT infinitude project (no separate license stated) |

infinitesp and the current infinitude CarBus modules are by the same author
(nebulous) and cross-reference each other; where they agree they are effectively
one source lineage, noted below as "infinitesp/infinitude".

> ## ⚠️ EVERYTHING BELOW IS UNVERIFIED AGAINST THE HUNTERHILL BUS
>
> Every offset, enum, and conversion in this document is what the *sources*
> claim for *their* hardware (mostly single-zone or 4-zone Touch systems,
> Bryant/Carrier furnaces, 24VNA9-class variable heat pumps). None of it has
> been matched to Hunterhill captures yet. Vanilla infinitive's decode produced
> **zeros** on this Touch + 3-zone system — at least partly because its snoop
> filters expect the air handler in 0x4000–0x42ff and the heat pump in
> 0x5000–0x51ff, while Hunterhill's air handler answers at **0x3e01** and the
> heat pump at **0x5201**, both outside those windows (see §Blower and
> §Outdoor-unit sections). Offsets may also genuinely differ per firmware.
> Treat every row here as a hypothesis to confirm against capture data before
> Phase 2 ships a decoder.

---

## Conventions

- **Register ID**: 3 bytes at the start of READ payloads and READ-reply/WRITE
  payloads: `00 TT RR` — first byte (nominally) always 0x00, `TT` = table,
  `RR` = row. Written here as 4 hex digits (`3B02` = table 0x3B row 0x02).
  Reply payload bytes after the 3-byte register ID are "payload offset 0" below
  unless a table explicitly includes the header.
- **Multi-byte integers are big-endian** unless noted (frame CRC16 is
  little-endian on the wire).
- **Temperatures ×16**: equipment-side registers (ODU/IDU/ZC) encode
  temperatures as `int16 BE / 16` in °F regardless of display unit
  (infinitesp: "verified by hooking a thermistor... not (°F-64)×16 as once
  guessed"). Thermostat/SAM table 0x3B registers instead use whole-degree
  bytes **in the current display unit** (°F or °C per the `metric_units` flag).
- **Zone arrays** are 8 slots, zone 1 first. Zone bitmaps put zone 1 in bit 0
  (0x01) … zone 8 in bit 7 (0x80).

### Frame recap (all three sources agree)

`dst(1) dst_bus(1) src(1) src_bus(1) len(1) pid(1) ext(1) op(1) data[len] crc16(2, LE)`

infinitive models dst/src as big-endian uint16 (`0x2001`), infinitesp/infinitude
as address byte + bus byte (`0x20`, bus `0x01`) — same wire bytes. Bus byte is
always 0x01 in home installs; pid/ext always 0x00. CRC16-ARC (poly 0x8005,
reflected, init 0) over header+data.

### Op codes

| Op | Name | Sources |
|---|---|---|
| 0x02 | ACK02 | all |
| 0x06 | ACK06 / reply — len 1 + data 0x00 acks a WRITE; len > 3 is a READ reply: 3-byte register ID then contents | all |
| 0x0B | READ (len 3 = register ID) | all |
| 0x0C | WRITE (register ID + new contents) | all |
| 0x10 | CHGTBN change table name | infinitive frame.go, wiki |
| 0x15 | NACK / exception. Single data byte codes: **0x04** = register not served by this device, **0x0A** = no such table / not writable / invalid data / function unsupported (wiki Framing page; infinitude 10.5-h capture: all 54,076 exceptions were code 0x04) | all |
| 0x1E | ALARM packet — **named only; no source decodes its payload** (see §Faults) | infinitive frame.go, wiki |
| 0x22 | OBJRD read object data | infinitive, wiki |
| 0x62 / 0x63 / 0x64 | RDVAR / FORCE / AUTO variable ops | infinitive, wiki |
| 0x75 | LIST read list | infinitive, wiki |

### Device addresses

(wiki [known devices], infinitesp `infinitesp.h`; class = high nibble)

| Addr | Device |
|---|---|
| 0x1F | Thermostat during bus discovery ("SystemInit") |
| 0x20 | Master thermostat / Infinity Touch (bus master) |
| 0x21–0x28 | Zone 1–8 UI / smart sensor |
| 0x34 | Infinity Smart Sensor (remote room sensor) |
| 0x40 / 0x41 / 0x42 | Furnace / furnace sub-unit / fan coil (class 4 = indoor unit) |
| 0x50 / 0x51 / 0x52 | Outdoor units; 0x52 = variable-speed heat pump (class 5) |
| 0x60 / 0x61 | Zone (damper) controllers — one SYSTXCC4ZC01 per 4 zones; 0x60 serves system zones 1–4, 0x61 zones 5–8 |
| 0x80 | NIM |
| 0x92 / 0x93 | SAM / "FakeSAM" address the emulators transmit as |
| 0xF1(F1) | Broadcast |

infinitesp: match device class with `(addr >> 4)`, never the full address — the
instance nibble varies by install ("ODU is 0x50 on some systems, 0x52 on
others"). **Hunterhill mapping per our own bus:** wall control 0x2001, air
handler **0x3e01** (class 3 — matches no source's "indoor unit" class; wiki
class 3 is "Sensor"), heat pump 0x5201, damper control 0x6001, remote sensors
0x2201/0x2301, SAM 0x9201. The 0x3e01 air-handler address is a Hunterhill
observation, not from any source, and needs capture confirmation.

### Table-definition register (row 1 of every table, `TT01`)

Every device self-describes each table at register `TT01`. Two byte-compatible
readings of the same layout exist:

infinitude `CarBus/Frame.pm` (`'01'` parser — "verified across thermostat, IDU
and ODU by probing live devices"):

| Offset | Size | Field |
|---|---|---|
| 0 | 1 | 0x00 |
| 1 | 1 | small flags/counter byte (0x20/0x21/0x30/0x31 observed; semantics undetermined) |
| 2–9 | 8 | ASCII table name, NUL/space padded (`DEVCONFG`, `SYSTIME`, `RLCSMAIN`, `VARSPEED`, `VAR COMP`, `LINESET`, …) |
| 10–11 | 2 | total table allocation (u16 BE); gaps count as 222 (0xDE) |
| 12 | 1 | rows = slot count **including** this TT01 register |
| 13 | 1 | self_size = byte length of this tabledef register |
| 14 | 1 | 0x01 descriptor-list flag |
| 15… | 2×(rows−1) | (size, access) pairs for rows TT02, TT03… — (0xDE,0xDE)=absent slot, (0x00,0x00)=empty |

access bits (proven at register level on live bus, per Frame.pm): bit0 =
readable, bit1 = writable; 0x02 write-only registers ACK writes but return an
exception on READ.

The wiki ([interpreting data] "Table / Row Addressing") reads the same bytes as
`type(u16) name[8] total_size(u16) row_count(u8)` then N `(size,flags)` pairs
starting with row 1 itself — numerically identical bytes, different naming.
infinid should confirm which framing matches Hunterhill tabledef replies.

---

## Device info — register `0104` (every device)

Source: infinitude `Frame.pm` `'0104'`, wiki register map. 120 bytes, read-only,
row 4 of table 0x01 DEVCONFG.

| Offset | Size | Field |
|---|---|---|
| 0 | 24 | device name (padded ASCII) |
| 24 | 24 | location |
| 48 | 16 | software version (e.g. `CESR131379-03`) |
| 64 | 20 | model (e.g. `SYSTXCCSAM01`) |
| 84 | 12 | reference |
| 96 | 24 | serial |

infinitesp derives manufacture date from the serial: first 2 digits = week,
next 2 = year.

## Time and date — registers `0202` / `0203` (broadcast), plus sync gaps

Source: infinitude `Frame.pm`, wiki Framing page.

- `0202` time: `hour(1) minute(1) weekday(1)` — weekday enum 0=Sunday…6=Saturday.
  (Wiki register-map line says payload "HH MM SS"; the CarBus parser says the
  third byte is weekday — **sources disagree**; CarBus is the newer decode.)
- `0203` date: `day(1) month(1) year(1)` — year is 20xx offset (`2000 + byte`).
- The thermostat broadcasts both to 0xF1F1 roughly every 60–90 s.
- `0420` (thermostat table 0x04 SSSBCAST row 32): 20 bytes, all zeros observed;
  polled routinely by a real SAM, never linked to any command flow (infinitude
  SAM.pm).
- **Hunterhill observation (ours, no source coverage): the wall control WRITEs
  register `00041f` to the remote room sensors (0x2201/0x2301).** No source
  documents table 0x04 row 0x1F. Closest context: thermostat table 0x04 is
  SSSBCAST "broadcast data" (wiki thermostat table list); the zone-sensor
  device (0x22) has tables 0x01/0x02/0x03/0x04 where 0x04 is named
  `SMT SNSR M` (wiki). Payload layout = experimental-decode work for Phase 2;
  a time/date-sync role is plausible but unconfirmed.

---

## Zone climate — SAM/thermostat table 0x3B (mirrored: thermostat SAMINFO ↔ SAM AI PARMS)

All three sources decode these. infinitive strips reply data `[6:]` (3-byte
register ID + 3-byte register header) before mapping its Go structs, so
infinitive struct field N ≡ payload offset N+3 below — with that shift applied,
**infinitive, infinitesp, and infinitude agree on every 3B02/3B03 offset**.

A shared header on every table-0x3B register (infinitesp/infinitude, live-
verified 2026-06-26):

- byte 0: `active_zones` — zone bitmask on read (which zones exist); on write,
  zone selector. infinitude's live SAM captures show writes carry a **zero-based
  zone index** (zone 1 → 0x00, zone 4 → 0x03), while the wiki describes a zone
  bitmap — sources disagree; confirm on capture.
- byte 1: `metric_units` — 0=English(°F), 1=Metric(°C). All temperatures,
  setpoints and the OAT in table-0x3B registers are in this unit.
- byte 2: `change_flags` on write (see 3B03), 0x00 on read.

### `3B02` — system state (29 bytes, SAM-served + thermostat-cached)

Register owner: SAM 0x9201 / thermostat 0x2001 (each serves its copy).
Sources: infinitive `TStatCurrentParams` (addr {00,3B,02}), infinitesp
`REG3B02_*`, infinitude `SAM.pm` `'3B02'`, wiki 3B02 table.

| Offset | Size | Type | Field | Notes |
|---|---|---|---|---|
| 0 | 1 | u8 | active_zones bitmask | header |
| 1 | 1 | enum | metric_units 0=°F 1=°C | header |
| 2 | 1 | — | reserved 0x00 | |
| 3–10 | 8 | u8[8] | current temperature per zone | whole degrees, display unit; read-only |
| 11–18 | 8 | u8[8] | current humidity per zone (%RH) | one physical sensor → all 8 equal |
| 19 | 1 | — | unknown | |
| 20 | 1 | i8 | outdoor air temperature | display unit (infinitive types it int8) |
| 21 | 1 | bitmap | zones unoccupied | Touch: maps to AWAY in "hold permanent" |
| 22 | 1 | u8 | stage/mode: high nibble = active stage count, low nibble = mode | mode enum below |
| 23 | 1 | u8 | display schedule period (non-Touch) | wiki; infinitesp/infinitude call 23–24 unknown |
| 24 | 1 | — | unknown | |
| 25 | 1 | enum | weekday 0=Sunday…6=Saturday | |
| 26–27 | 2 | u16 BE | minutes since midnight | bus clock |
| 28 | 1 | u8 | displayed zone (1–8) | |

Mode enum (low nibble of byte 22) — the sources give **two variants**:

- infinitive `conversions.go` (and wiki): 0=heat, 1=cool, 2=auto, 3=electric
  (fan-coil eheat), 4=heatpump-only, 5=off
- infinitesp `SYSMODE_*` / infinitude `stagmode` enum: 0=heat, 1=cool, 2=auto,
  3=eheat, **4=off** (no heatpump-only value)

Writing mode: write 3B02 with change-flag 0x10 in the byte-2 header position
(infinitude `set_system_mode`, infinitesp `CHANGE_MODE`).

### `3B03` — zone settings (150 bytes, read/write)

Sources: infinitive `TStatZoneParams`, infinitesp `REG3B03_*`, infinitude
`SAM.pm` `'3B03'`, wiki 3B03 table.

| Offset | Size | Type | Field | Write flag |
|---|---|---|---|---|
| 0 | 1 | u8 | active_zones (read: bitmask; write: zone selector, see header note) | |
| 1 | 1 | enum | metric_units | |
| 2 | 1 | u8 | change_flags (write) | |
| 3–10 | 8 | u8[8] | fan mode per zone: 0=auto 1=low 2=med 3=high | 0x01 |
| 11 | 1 | bitmap | zones holding ("hold permanent" on Touch) | 0x02 |
| 12–19 | 8 | u8[8] | heat setpoint per zone (whole degrees, display unit) | 0x04 |
| 20–27 | 8 | u8[8] | cool setpoint per zone | 0x08 |
| 28–35 | 8 | u8[8] | target humidity per zone (%RH; single real target mirrored ×8) | 0x10 (wiki) |
| 36 | 1 | u8 | fan auto config (infinitive `FanAutoCfg`; infinitude `speed_controlled_fan`) | 0x20 (wiki) |
| 37 | 1 | u8 | unknown (wiki: timed-override zone bitmap, flag 0x40, "may not be writable") | |
| 38–53 | 16 | u16 BE ×8 | hold duration ("hold until") minutes per zone | 0x80 |
| 54–149 | 96 | char[12]×8 | zone names, 11 chars + NUL | 0x0100 (wiki: flags are 2 bytes at offsets 1–2 with 0x0100=names — conflicts with byte 1 = metric_units per infinitesp/infinitude live verification; prefer the 1-byte-flag reading) |

Write-flag semantics (all sources): payload = `00 3B 03` + 150-byte buffer whose
byte-2 carries the OR of changed-field flags; unflagged fields are ignored (the
real SAM leaves stale garbage in them). 0x10 in a 3B03 write means *mode* per
infinitesp `CHANGE_MODE` (mode itself lives in 3B02).

Hold encoding (infinitesp, live-verified 2026-06-30 on Touch): permanent hold =
flag 0x02 with the zone's holding bit set and duration ≤ 1; cancel = flag 0x02
with bit clear; the thermostat **ignores** flag-0x80 timed-hold writes.
infinitesp normalizes permanent to 0xFFFF minutes internally.

### `3B04` — vacation (11 bytes; change-frame, not a flat register)

Sources: infinitive `TStatVacationParams` (older, flat-struct reading),
infinitesp `REG3B04_*` + infinitude `SAM.pm` `'3B04'` (2026-07-09 decode from a
real SAM bridged to a live bus).

infinitesp/infinitude (newer, live-verified): the thermostat **never reads 3B04
from the SAM**; the SAM pushes WRITE frames where `data[2]` is a bitmask of
fields carried and unflagged bytes are 0xFF:

| data[2] bit | Field | Location |
|---|---|---|
| 0x02 | hours remaining (u16 BE) | data[4..5] (verified) |
| 0x04 | min temp (display unit) | data[6] (verified) |
| 0x08 | max temp | data[7] (verified) |
| 0x10 | min humidity (0 = NONE) | data[8] (pattern-implied) |
| 0x20 | max humidity (100 = NONE) | data[9] (pattern-implied) |
| 0x40 | fan mode 0–3 | data[10] (verified) |

Read-format layout (for decoding an emulator/monitor view): `header(1, 0xFF on
read) metric_units(1) change_flags(1) pad(1) hours(u16 BE) min_temp max_temp
min_hum max_hum fan_mode`. Vacation is "active" when hours > 0; no separate
active flag. infinitive's older flat struct (`Active(1) Hours(u16)
MinTemperature MaxTemperature MinHumidity MaxHumidity FanMode`) and the wiki's
`days_times7` reading disagree with this and were superseded; infinitive also
oddly derived `days = Hours/7`.

### `3B05` — accessory life / FILTER LIFE (11 bytes)

Sources: infinitesp `REG3B05_*`, infinitude `SAM.pm` `'3B05'` (provenance note:
"our own RE, not a Carrier source" — byte→accessory mapping unconfirmed except
the metric flag). infinitive has **no** 3B05 support.

| Offset | Size | Field |
|---|---|---|
| 0 | 1 | active |
| 1 | 1 | metric_units (live-verified) |
| 2 | 1 | pad |
| 3 | 1 | **filter life consumed %** (0 = new/reset, 100 = replace) |
| 4 | 1 | UV lamp life consumed % |
| 5 | 1 | humidifier pad life consumed % |
| 6 | 1 | ventilator filter life consumed % |
| 7 | 1 | filter reminder 0=off 1=on |
| 8 | 1 | UV reminder |
| 9 | 1 | humidifier reminder |
| 10 | 1 | ventilator reminder |

The SAM ASCII command `FILTRLVL?`/`FILTRLVL!0` reads/resets filter life
(infinitude `SAM/ASCII.pm`, wiki SAM-ASCII page); the reset presumably writes 0
to byte 3. Note this is the SAM's copy — where the *equipment* reports filter
usage on the bus is not documented by any source (see Gaps).

### `3B06` — dealer info / config (52 bytes)

Sources: infinitesp `REG3B06_*`, infinitude `SAM.pm` `'3B06'` (same caveat:
own RE; only the metric flag live-verified). infinitive `TStatSettings`
(49 bytes, older non-Touch layout) differs — both shown:

infinitesp/infinitude (Touch, 52 bytes): `backlight(0) metric_units(1)
unknown(2) deadband(3, 0–6) cycles_per_hour(4, 2–6) schedule_periods(5, 2|4)
programs_enabled(6) unknown 0xFF(7) unknown 0xFF(8) programs_enabled_2(9)
metric_units mirror(10) unknown(11) dealer_name[20]@12 dealer_phone[20]@32`.
Byte 7 was once guessed `temp_units` ('F'/'C') — observed 0xFF on Touch; wrong.
On Touch, writes to deadband/CPH/periods/programs NAK.

infinitive `TStatSettings` (addr {00,3B,06}; remember its structs start at
payload offset 3... **no** — 3B06 in infinitive also passes through the same
`data[6:]` strip, so its `BacklightSetting` lands at payload offset 3, which
conflicts with infinitesp's backlight at offset 0): `BacklightSetting AutoMode
Unknown1 DeadBand CyclesPerHour SchedulePeriods ProgramsEnabled TempUnits
Unknown2 DealerName[20] DealerPhone[20]`. The two layouts cannot both be right;
infinitesp/infinitude explicitly call the infinitive-era `auto_mode`/
`temp_units` fields wrong decodes. Prefer infinitesp/infinitude; verify locally.

### `3B0E` — activity acknowledgment (1 byte, write-only thermostat→SAM)

Thermostat writes 0x01 after processing a SAM change notification. Bursts of 3
writes; ~3 for config commands, ~11 for zone commands (infinitude SAM.pm, wiki).

### `3B07–3B0D` — schedules (non-Touch only)

Wiki: 163 bytes each; 7 one-day schedules × 8 zones × 4 periods ×
(start_time, heat_sp, cool_sp, fan). No parser in any source.

---

## Thermostat-internal tables (device 0x20, tables 0x40–0x4A)

Sources: infinitesp `REG_TSTAT_*`, infinitude `Frame.pm` parsers, wiki
thermostat table list (SYSTXCCITC01 firmware CESR131493-14.02: DEVCONFG 0x01,
SYSTIME 0x02, INGUI 0x03, SSSBCAST 0x04, LINESET 0x06, STARTUP 0x2F, INGDATA
0x31, SAMINFO 0x3B, SCHEDULE 0x40, TEMP 0x41, LASTTEN 0x42, MISC1 0x46, RESTMR
0x47, HEALTH 0x48, SYSCTRL 0x49, MISC2 0x4A, SYSMETRC 0x4C, CHARGING 0x4E).

- `4002` zone-1 schedule (35 bytes): 7 days × 5 chunks of `min15s(1) mode(1)`;
  time = min15s ÷ 4 hours, ×15 min remainder; 0x60 = slot disabled; mode
  0=home 1=away 2=sleep 3=wake.
- `400A` zone-1 comfort profiles (35 bytes): 5 activities (home, away, sleep,
  wake, manual) × 7 bytes `[heat_sp, cool_sp, fan_mode, (rhtg<<4)|rclg
  dehumidify nibbles, hum/vent flags, 2× unknown (0x1E observed)]`. In °C mode
  the setpoint bytes are half-degrees (byte/2).
- `4012` zone-1 vacation (7 bytes): `min_temp max_temp fan unknown[4]`.
- `4102` (TEMP): zone ambient temps as float32 BE °F ×4 at bytes 10–25;
  4 live unknown floats at 50–65. `3107` (INGDATA) carries the same four zone
  temps as floats at offset 0.
- `3123` (INGDATA): cached blower CFM float32 at byte 12 (matches IDU 0306).
- `4903` (SYSCTRL): two float32[8] arrays of °F values (setpoint-like;
  identity unconfirmed) at 0–31 and 64–95.
- `4A04` (MISC2): blower/compressor stage curve — six RPM-like float32
  (1800…4680) then four CFM-like float32.
- `4608` WiFi (SSID@24, password@70, hostname@139, MAC@4 as C strings),
  `4609` cloud host@0/proxy@67, `460A` dealer name@0/brand@50/url@70,
  `460B`/`460C` WiFi profiles/scan: 4 × (ssid[32] unknown flag channel rssi).
- `4202` fault history — see §Faults below.

---

## Damper positions — zone controller (device class 0x6, tables 0x03/0x34)

Sources: infinitesp `REG_ZC_*` + `zc_*` mapping comments, infinitude
`ZoneController.pm`, wiki register map. ZC tables: 0x01 DEVCONFG, 0x02 SYSTIME,
0x03 RLCSMAIN, 0x04 "04ZONE STL" (SYSTXCC4ZC01); device 0x61 also observed
with tables 0x03 and 0x34.

### `0302` — zone sensor readings (24-byte TLV)

Six 4-byte entries `[tag, id, value_hi, value_lo]`, always in id order:
ids 0x01–0x04 = local zones 1–4, 0x14 = LAT (leaving-air thermistor),
0x1C = HPT thermistor port. tag 0x01 = sensor present (value valid),
0x04 = not installed (value 0x0000). Value = u16 BE, **°F = value / 16**
(infinitesp: verified against the thermostat's Furnace Status page).
infinitude's parser reads entry 1 as `zone_count(1) zone1_present(1) pad(2)` —
byte-compatible with the TLV reading for zone 1.

### `0308` — damper position command (WRITE, thermostat → ZC)

8 bytes, one per **system** zone 1–8; position 0x00 = closed … 0x0F = open
(0x0A observed mid-travel). Written identically to both controllers; 0x60 acts
on bytes 0–3, 0x61 on bytes 4–7 (infinitesp issue #9, verified on hardware).
Real ZC ACKs with a single 0x00 byte, no register prefix. The thermostat
re-sends the same 0308 every ~10–15 s.

### `0319` — damper state feedback (8 bytes)

Bytes 0–3 = damper state per local zone (0x0F open / 0x0A transitioning /
0x00 closed); bytes 4–7 = 0xFF (unused slots). The ZC mirrors 0308 commands
into 0319 with ~15–20 s per-zone delay; the thermostat reads it during duct
evaluation ("hangs at 'opening all zones'" without it). On a real 4-zone
install the power-up self-scan walked `0f00000f → … → 0f0f0f0f` over ~217 s
(infinitude ZoneController.pm timeline). The **secondary** controller (0x61)
returns all-0xFF on 0319, so infinitesp treats 0308 as the only reliable
open/closed source.

> **Hunterhill observation:** our 0x6001 answers register `000319` with
> `0f 0f 0f 0f ff ff ff ff` — exactly the source-documented "4 zones known,
> all open, slots 5–8 absent" pattern. First confirmed match between sources
> and this bus.

### Other ZC registers

- `3404` heartbeat/write flag (1 byte); writes ACKed with bare 0x00.
- `3405` presence probe (discovery).
- `030D` 7 bytes, always zeros on ZC.
- `0310` / `0311` cycle/runtime counters — 4-byte KV entries (see next section);
  ZC keys: 0x2B poweron_cycles, 0x2C poweron_hours, 0x38/0x39/0x3A/0x3B unknown.

---

## Blower / airflow — indoor unit (class 0x4, table 0x03 RLCSMAIN + 0x04 VARSPEED)

Sources: infinitive `api.go` snoop, infinitesp `REG_IDU_*` + accessors,
infinitude `IndoorUnit.pm`, wiki register map.

> infinitive snoops these from frames whose **source is 0x4000–0x42ff**; the
> Hunterhill air handler answers from 0x3e01, so vanilla infinitive never
> decoded it. The register IDs below should still apply if the payloads match.

### `0306` — blower / operating status (10 bytes)

| Offset | Size | Field | Source |
|---|---|---|---|
| 0 | 1 | status flags | infinitesp/infinitude |
| 1–2 | 2 | blower RPM (u16 BE) | all three (infinitive reads `data[1:5]` into a uint16 — effectively bytes 1–2 after Go's truncation quirk) |
| 3–4 | 2 | indoor airflow CFM (updates faster than 0316) | wiki only |
| 5–9 | 5 | undocumented (operating mode, stage?) | — |

### `0316` — airflow configuration (14 bytes)

| Offset | Size | Field | Source |
|---|---|---|---|
| 0 | 1 | flags; `& 0x03` ≠ 0 → electric heat present. Wiki: state enum 0=no_heat 1=low_heat 2=med_heat 3=high_heat | all |
| 4–5 | 2 | airflow CFM (u16 BE) | all |
| 6–7 | 2 | electric-heat CFM (u16 BE) | infinitude |
| 6–7(?) | 2 | wiki instead: static pressure, in-wc = value/65536 at "uint8 unknown; uint16 static_pressure" after CFM | wiki — conflicts with infinitude's elec_heat_cfm; unresolved |

### `0310` / `0311` — cycle counters / runtime hours (repeated on IDU, ODU, ZC)

Sequence of 4-byte entries: `key(1) value(u24 BE)`. IDU keys
(infinitude/infinitesp): 0x23 low_heat_cycles, 0x24 high_heat_cycles,
0x48 med_heat_cycles, 0x2B poweron_cycles, 0x2D blower_cycles;
hours: 0x25 low_heat, 0x26 high_heat, 0x49 med_heat, 0x2C poweron,
0x2E blower; 0x27/0x28/0x29/0x2A unknown.

### Other IDU registers (wiki only)

- `0305`: writes from thermostat to IDU, layout varies.
- `0307`: byte 0 = humidifier status (0x00 off / 0x01 on, Bryant 987M).
- `030A`: 14-byte constant read by 0x40 from sub-unit 0x41 (static config; the
  only device-to-device traffic not mediated by the thermostat).
- `0402`: byte 31 = furnace gas-valve % open (40–100 on a 987M; does NOT drop
  to 0 when closed — use 0316 state for that).

---

## Outdoor unit diagnostics (class 0x5, tables 0x03 RLCSMAIN + 0x06 VAR COMP)

Sources: infinitesp `REG_ODU_*` + accessors, infinitude `OutdoorUnit.pm`
(cross-referenced against thermostat service menus and Carrier cloud data),
wiki register map (24VNA9). infinitive's HP snoop (source range 0x5000–0x51ff —
**excludes Hunterhill's 0x5201**) uses different registers `3E01`/`3E02`, at
the end of this section.

### `0302` — temperatures (24 bytes, table 0x03)

12 × int16 BE, each /16 = °F (always °F regardless of display unit),
alternating (threshold-constant, live measurement):

| Offsets | Pair | Note |
|---|---|---|
| 0–3 | outdoor_threshold, **outdoor_temp (OAT)** | |
| 4–7 | coil_threshold, **coil_temp** | outdoor coil |
| 8–11 | suction_threshold, **suction_temp** | |
| 12–15 | liquid_threshold, **suction superheat ΔT** | infinitesp idx 3: confirmed superheat (matches display, 16–17 °F); infinitude names it subcooling_degf_int — a ΔT either way, not absolute |
| 16–19 | indoor_amb_thresh, **indoor ambient** | echo of zone ambient, not a coil thermistor |
| 20–23 | discharge_threshold, **discharge_temp** | compressor discharge |

### `0303` — short status (4 bytes)

`status(1)=0x01 run flag, status(1)=0x30, suction line pressure u16 BE /16 =
PSIG` (confirmed vs thermostat `suctpress`, r=0.95 vs R-410A saturation
pressure across ~27k frames).

### `0304` — status (16 bytes)

byte 7 = line voltage in whole volts (validated against Carrier cloud
`linevolt`). infinitesp exposes byte 10 as "operating mode" but infinitude
notes byte 10 was 0 in every observed frame — opmode's true source
undetermined. Wiki adds: bytes 0xEF–0xF5 region "cycles".

### Table 0x06 VAR COMP (variable-speed compressor)

| Register | Layout | Source |
|---|---|---|
| `0604` | `[0..1]` target RPM u16 BE (rated stage speeds 0/1500/1700/2460/2800/3650), `[2..3]` actual RPM | infinitesp (verified via stage crosstab), infinitude. Wiki adds: further u16 pairs may be a fixed speed table |
| `0605` | float32 BE at [0..3]: commanded stage 0.0 / 1.0–5.0. Write-only, thermostat→ODU; drives 060E with ~15 s lag | infinitesp/infinitude |
| `0608` | `[2]` expansion-valve position 0–100 % (ramps 39–95 % over 10–15 s on start/stop; discovered by feisley), `[5..6]` u16 BE drive frequency ×0.1 Hz (RPM = 3×Hz on the 4-pole motor, confirmed stages 1–4) | infinitesp/infinitude. Wiki's older theory (byte4=demand%, byte7=stage, byte8=modulation) superseded |
| `060B` | `[2]` target value, native °F whole degrees (25–115 °F; write-only; NOT confirmed a cooling setpoint — likely refrigerant-loop control target). Older guesses: data[4] (always 0, wrong), wiki byte 5 | infinitesp/infinitude |
| `060E` | byte 0 = actual stage index 0=off, 1–5=stage (verified vs RPM across ~16k frames); remaining ~124 bytes undecoded | infinitesp/infinitude |
| `061E` | 10-byte constant write `0f3f40000006000000` | wiki |
| `061F` | `[0]` 0x00 then six float32 BE: superheat target (~7.5), superheat actual, subcooling target (~14), subcooling actual, discharge-related control delta (−68…+15 while running, 0 when off — NOT literal discharge superheat; likely incorporates internal head-pressure), dimensionless ~0.039. All °F deltas; unit-toggle-invariant | infinitude (detailed stats), infinitesp, wiki float analysis |
| `0602`, `060F` | polled every ~60 s / ~30 s, undecoded | wiki |
| `0310`/`0311` | KV counters — ODU keys: 0x23 heat_cycles, 0x25 heat_hours, 0x28 cool_cycles, 0x2A cool_hours, 0x3C defrost_cycles, 0x3D defrost_hours, 0x2B poweron_cycles, 0x2C poweron_hours | infinitude/infinitesp |

### infinitive's heat-pump registers `3E01` / `3E02` (older units)

From `api.go` (snooped from sources 0x5000–0x51ff) and wiki ("OutdoorUnit 0x50
table 0x3E … does not follow standard structure"):

- `3E01`: `[0..1]` outside temp ×16 (u16 BE /16 = °F), `[2..3]` coil temp ×16.
- `3E02`: byte 0 >> 1 = heat-pump stage.

Note these are **register/table IDs**, coincidentally the same hex as
Hunterhill's air-handler *address* 0x3e01 — do not conflate.

---

## Faults and alarms

### `4202` — thermostat fault history (table 0x42 LASTTEN, row 2; 70–72 bytes, R/W)

Sources: infinitesp `REG_TSTAT_FAULTS` + `has_active_fault()` +
`text_sensor` decode; infinitude `Frame.pm` `'4202'` (cross-referenced against
the Carrier cloud `equipment_events` XML). infinitive has **nothing** on
faults. 10 entries × 7 bytes, newest first (entry 0 = latest):

| Offset | Size | Field |
|---|---|---|
| 0 | 1 | fault code (decimal, e.g. 12, 68, 186) |
| 1 | 1 | source device **bus address** (0x20 = UI/thermostat, 0x40 = furnace/IDU, 0x52 = AC/ODU; decode class by high nibble) |
| 2 | 1 | hour (0–23) |
| 3 | 1 | minute (0–59) |
| 4–5 | 2 | days since **2013-01-01** (u16 BE — nonstandard epoch, "nobody knows why", matches all observed data) |
| 6 | 1 | bit 7 = active flag, **inverted**: 0 = active, 1 = cleared; bits 0–6 = occurrence count (0–127) |

Empty slots are all-zero (skip when code=source=days=0). Timestamps are naive
local wall-clock.

**Fault code meanings**: not documented by any source. infinitesp's text sensor
comment: "Human-readable descriptions require a verified Carrier Infinity /
Bryant Evolution fault-code reference, which is not available here." The
cloud `equipment_events` XML carries descriptions if ever needed for
correlation.

### Active alarm frames — op `0x1E` ALARM

**No source decodes the 0x1E payload.** infinitive `frame.go` and the wiki
framing page name it (`AlarmPacket` / "ALARM") and nothing more; infinitesp and
infinitude's CarBus never handle op 0x1E at all. Payload layout is a Phase 2
experimental-decode target. Related but distinct: NACK (0x15) exception codes
0x04 / 0x0A (see op table), and infinitesp's "active fault" signal, which is
derived from polling 4202, not from alarm frames.

---

## Gaps — what no source documents (Phase 2 experimental-decode list)

1. **Op 0x1E ALARM payload** — named in every source, decoded in none.
2. **Fault-code → description mapping** — numeric codes only; no verified
   Carrier/Bryant code table in any repo.
3. **Register `00041F` writes to remote room sensors** — Hunterhill-observed,
   absent from all sources (zone-sensor table 0x04 = "SMT SNSR M" is the only
   clue). Whole zone-sensor register space (device class 0x2/0x34, tables
   0x03/0x04/0x30/0x32) is undecoded beyond tabledef names.
4. **Equipment-side filter-life accounting** — 3B05 is the SAM's cached %; how
   the thermostat computes it (blower hours? IDU register?) is undocumented.
5. **IDU 0306 bytes 3–9 / 0316 remaining bytes** — operating mode, stage,
   static pressure vs elec-heat CFM conflict unresolved.
6. **ODU 0304** beyond line voltage; true source of `opmode`; ODU 060E bytes
   1–124; 0602/060F entirely.
7. **Thermostat INGDATA/SYSCTRL float registers** (310D, 3117, 490B, parts of
   4102/4903) — clean floats, unknown identity.
8. **3B02 bytes 19, 23–24**; 3B03 byte 37; 3B06 unknowns.
9. **Non-Touch schedule registers 3B07–3B0D** — layout sketched in wiki only,
   no parser.
10. **Hunterhill address anomalies** — air handler at 0x3e01 (class 3) matches
    no source's class map; heat pump at 0x5201 (sources document 0x52 as
    "OutdoorUnit2" so this one is expected). All snoop filters in Phase 2 must
    match by observed address, not by the sources' class assumptions.
11. **Damper registers on >4-zone splits vs this 3-zone system** — sources
    verified 0x60/0x61 splitting on 4+4; Hunterhill has one controller and
    3 zones; per-zone byte mapping still needs local confirmation (the one
    `000319` observation matches).

---

## Hunterhill live verification — 2026-08-12 labeled session

First registers verified against **this** bus, from a hand-labeled experiment
session (setpoint steps, fan ladder, vacation preset) correlated with a ~496k
frame capture. Full evidence chains:
[experiments/2026-08-12-labeled-session.md](experiments/2026-08-12-labeled-session.md).
Everything in this section supersedes the "unverified" banner above for the
listed fields only.

### Confirmed prior-art claims

| Register (owner) | Field | Verified meaning | Scaling | Conf |
|---|---|---|---|---|
| `000605` (write 2001→5201) | [0..3] | commanded compressor stage | float32 BE, 1.0–5.0 (2.0→5.0→1.0 at labeled setpoint steps) | HIGH |
| `00060E` (5201) | [0] | actual stage index | u8 0=off 1–5 | HIGH |
| `000604` (5201) | [0..1]/[2..3] | target / actual compressor RPM | u16 BE | HIGH |
| `000306` (3e01) | [1..2] | blower RPM | u16 BE | HIGH |
| `000306` (3e01) | [3..4] | airflow CFM (echo of `000305` command) | u16 BE | HIGH |
| `000316` (3e01) | [4..5] | airflow CFM | u16 BE | HIGH |
| `000303` (5201) | [2..3] | suction pressure PSIG (closes superheat arithmetic: sat 41 °F + SH 16 = suction 57 °F ✓) | u16 BE /16 | HIGH |
| `000304` (5201) | [7] | line voltage (244–245 V) | u8 volts | HIGH |
| `000608` (5201) | [5..6] | drive frequency; RPM = 3 × raw (raw 1470 ↔ ~4400 RPM) | u16 BE | MED-HIGH |
| `000608` (5201) | [2] | EEV position % (pegged 100 all session) | u8 | MED |
| `000308`/`000319` (2001→6001 / 6001) | [0..7] | damper command / feedback, byte per zone, 0x00–0x0F; 0319 mirrors 0308 with ~10–15 s lag; absent slots 0xFF in 0319 | nibble-range u8 | HIGH |
| `000302` (6001) | TLV | tag 01=present / 04=not installed, value u16/16 °F (all six absent here) | /16 °F | HIGH |
| `000202` (f1f1) | — | hour, minute, **weekday** (CarBus reading right, wiki "HH MM SS" **wrong**) | — | HIGH |
| `000203` (f1f1) | — | day, month, year−2000 | — | HIGH |
| `00061F` (5201) | floats | superheat/subcool float family incl. the 0.039 constant | float32 BE | MED |

### Refined / corrected prior art

| Register (owner) | Correction | Conf |
|---|---|---|
| `000302` (5201) | **Not** flat (threshold, value) int16 pairs — it is the same 4-byte TLV as the ZC: `tag(01) id value(u16/16 °F)` × 6. ids: 0x11 OAT (89.7–91.4), 0x12 outdoor coil (95→102 at stage 5), 0x30 suction temp (56–58), 0x4A superheat ΔT (14–16), 0x4B unknown 79–81 °F, 0x45 discharge (153→167 at stage 5) | HIGH (0x4B LOW) |
| `000625` (5201) | Old "byte1 = stage/speed %" hypothesis **dead**: [0..1] is one u16 BE power-like analog — ~2090 at stage 2, ramps to 4802 at stage 5, ~1040 at stage 1 (watts plausible) | field HIGH, units MED |
| `000604` (5201) | [4..13] / [14..23] are two static 5-entry u16 stage-RPM tables: heat 1200/2600/3200/4140/5400, cool 1200/2180/2850/3700/4140 — this unit's ladder ≠ the sources' rated speeds | HIGH |
| `000305` (write 2001→3e01) | Layout pinned: bytes[4..5] u16 BE commanded CFM (813→1476→558 across labels), byte11 = 0x78 constant | HIGH |
| `000308`/`000319` zone bytes | Partial positions 0x04–0x0B used continuously for balancing (not just 00/0A/0F). **Hunterhill zone map: byte0 bedrooms, byte1 living_room, byte2 basement** | HIGH |
| NACK codes | All 19,896 exceptions in this capture are code **0x0A** (3e01 refusing the ~1 s `000715` poll) — infinitude's "all exceptions are 0x04" does not hold here | HIGH |
| `000316` (3e01) | [7..8] u16 is load-correlated but matches neither static-pressure nor elec-heat-CFM readings cleanly — conflict still open | LOW |

### New decodes (no prior art)

| Register | Layout | Conf |
|---|---|---|
| `00041F` (write 2001→2201/2301, 20 B) | **Zone config push to smart sensors**: [0] flags (bit7 = hold active; cleared by preset-resume), [5] fan mode 0=auto 1=low 2=med 3=high, [6] heat setpoint °F, [7] cool setpoint °F, [10]=0x04 const. This is **where setpoint changes land on the bus**; the wall-control's own zone's setpoints never appear on the bus at all | [5][7] HIGH, [0][6] MED |
| `00041E` (2201/2301, 20 B) | **Zone status from smart sensors**: [5] fan echo, [6] mirrors 41F[6], [7] zone temp whole °F, [9..10] u16 BE zone temp ×16 (73.25 / 70.25 observed), [11] zone temp whole °F, [12] RH % | [9..10] HIGH, rest MED |
| `000413` (3e01, 12 B) | Blower telemetry: [0..1] measured CFM, [2..3] RPM, [4..7] float32 static pressure in wc (0.32–0.66), [8..11] float32 blower power W (77–469) | CFM/RPM HIGH, floats MED |
| `000602` (5201, 14 B) | Outdoor fan: [12..13] target RPM (900/500 by stage), [4..5] actual RPM (ramps); [0] bit6 toggles, unknown | MED |
| `000406` (3e01) + `00060B` (5201) | Thermostat writes the identical `01 04 XX 00` payload to IDU **and** ODU; byte2 97–100, consistent with a °F loop-target (condensing-temp-like) | LOW-MED |
| `000302` (3e01, 12 B) | IDU thermistor TLV (same format): id 0x14 present, 69.5–70.7 °F /16 — LAT candidate but never approached supply-air temps during stage 5; possibly cabinet-mounted | scaling HIGH, identity MED |
| `00061F` (5201) | float32 at [30..33] = 357–489, tracks stage/head pressure (PSIG?) | MED-LOW |

### Session behavioral findings

- Setpoint chain latency: 41F write +13 s after touch, stage command +16 s,
  damper command +21 s, damper feedback +37 s, airflow step +82 s.
- Fan-mode selection (E3 ladder) changed **only** the 41F enum during active
  cooling — commanded CFM never left the 558–576 band (fan mode acts as a
  floor, not a direct blower command, at stage 1).
- Vacation preset produced **zero** observable bus traffic in 102 s; only
  artifact was the 41F hold-bit clear 12 s after resume. Vacation state is
  wall-control-internal (no SAM active on this bus).
