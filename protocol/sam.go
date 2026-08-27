package protocol

import (
	"time"

	"github.com/utkjmitch/infinid/bus"
)

// SAM-served registers (wall control replies to an active 0x92 reader).
// Provenance varies by table — see each decoder's comment. All of them are
// live-validated (Task 16) before the SAM scheduler ships enabled, but the
// number of independent sources behind each layout differs, and that
// difference belongs in the code, not just the plan doc.

// modeNames — 3B02 byte 22 low nibble. Values 0-2 agree across all
// sources; 3+ conflict between source generations, so they decode to the
// raw value with Text "unknown" until live-verified.
var modeNames = []string{"heat", "cool", "auto"}

// systemState3B02 — active_zones(0), metric_units(1), temps u8[8]@3,
// RH u8[8]@11, OAT i8@20, stage/mode@22, minutes-since-midnight u16@26.
// Verified: infinitive/infinitesp/infinitude agree on every offset used here.
func systemState3B02(p []byte) []Reading {
	if len(p) < 29 {
		return nil
	}
	zones := p[0]
	rs := []Reading{
		{Field: "active_zones", Value: float64(zones)},
		{Field: "metric_units", Value: float64(p[1])}, // 0=°F 1=°C; lets downstream detect °C systems
	}
	for z := 1; z <= 8; z++ {
		if zones&(1<<(z-1)) == 0 {
			continue
		}
		rs = append(rs,
			// _sam suffix: 00041E already emits passive "temp"/"humidity"
			// readings for the same zones; suffixing the SAM duplicates keeps
			// the two sources as distinct state keys instead of colliding
			// last-writer-wins.
			Reading{Zone: z, Field: "temp_sam", Value: float64(p[3+z-1])},
			Reading{Zone: z, Field: "humidity_sam", Value: float64(p[11+z-1])},
		)
	}
	mode := p[22] & 0x0f
	text := "unknown"
	if int(mode) < len(modeNames) {
		text = modeNames[mode]
	}
	rs = append(rs,
		Reading{Field: "outdoor_temp_sam", Value: float64(int8(p[20]))},
		Reading{Field: "active_stages", Value: float64(p[22] >> 4)},
		Reading{Field: "system_mode", Value: float64(mode), Text: text},
	)
	return rs
}

// zoneSettings3B03 — fan u8[8]@3, holding bitmap@11, heat u8[8]@12,
// cool u8[8]@20, hold-duration-minutes u16 BE ×8 @38.
// Verified: infinitive/infinitesp/infinitude agree on every offset used here.
func zoneSettings3B03(p []byte) []Reading {
	if len(p) < 150 {
		return nil
	}
	zones := p[0]
	var rs []Reading
	for z := 1; z <= 8; z++ {
		if zones&(1<<(z-1)) == 0 {
			continue
		}
		// hold_permanent — the 3B03 holding bitmap, distinct from 00041F's
		// timed "hold": on Touch this bit means a *permanent* hold, not a
		// countdown. Naming it separately from zoneConfigPush's "hold" keeps
		// the two concepts as distinct HA entities downstream instead of
		// colliding last-writer-wins in state.
		holdPermanent := 0.0
		if p[11]&(1<<(z-1)) != 0 {
			holdPermanent = 1
		}
		// _sam suffix: 00041F already emits passive "fan_mode",
		// "heat_setpoint", "cool_setpoint", and (while a timed hold is
		// active) "hold_remaining_min" for the same zones; suffixing the
		// SAM duplicates keeps the two sources as distinct state keys
		// instead of colliding last-writer-wins.
		rs = append(rs,
			Reading{Zone: z, Field: "fan_mode_sam", Value: float64(p[3+z-1]), Text: fanText(p[3+z-1])},
			Reading{Zone: z, Field: "hold_permanent", Value: holdPermanent},
			Reading{Zone: z, Field: "heat_setpoint_sam", Value: float64(p[12+z-1])},
			Reading{Zone: z, Field: "cool_setpoint_sam", Value: float64(p[20+z-1])},
		)
		// hold_remaining_min_sam is emitted unconditionally, including 0.
		// Since it's a distinct key from 00041F's "hold_remaining_min", this
		// unconditional emit is correct as-is: an expiring SAM hold clears
		// its own key by simple overwrite (0 replaces the last nonzero
		// value) the next time this table is read. The state package's
		// delete-on-hold=0 rule applies only to the 00041F
		// "hold_remaining_min" key — SAM no longer touches it. 0xFFFF is
		// skipped: infinitesp normalizes permanent hold to 0xFFFF internally
		// (not a documented wire value). The documented wire encoding for
		// permanent is duration <= 1 — indistinguishable here from expired;
		// the hold_permanent bitmap field is what disambiguates it
		// downstream.
		if mins := u16(p, 38+2*(z-1)); mins != 0xffff {
			rs = append(rs, Reading{Zone: z, Field: "hold_remaining_min_sam", Value: float64(mins)})
		}
	}
	return rs
}

// accessoryLife3B05 — consumed % at fixed offsets (0 = new, 100 = replace).
// Provenance: two sources (infinitesp REG3B05_* and infinitude SAM.pm), but
// infinitude self-describes the decode as its own RE rather than a Carrier
// source, and no derivation between the two is recorded; infinitive has no
// 3B05 support at all. Only the metric-units flag is separately live-verified; the
// byte→accessory mapping is unconfirmed until Task 16's live gate.
func accessoryLife3B05(p []byte) []Reading {
	if len(p) < 11 {
		return nil
	}
	return []Reading{
		{Field: "filter_life_used", Value: float64(p[3])},
		{Field: "uv_life_used", Value: float64(p[4])},
		{Field: "humidifier_pad_life_used", Value: float64(p[5])},
		{Field: "vent_filter_life_used", Value: float64(p[6])},
	}
}

// Fault is one 4202 fault-history entry.
type Fault struct {
	Code   int
	Source byte // bus address class: 0x20 thermostat, 0x40 IDU, 0x52 ODU
	Time   time.Time
	Active bool
	Count  int
}

// faultEpochUTC — 4202 day counts are days since 2013-01-01 (nonstandard,
// verified against cloud equipment_events by prior art: two sources,
// infinitesp + infinitude). Day math is done in UTC and only converted to
// the caller's zone for the final wall-clock assembly, so a day boundary
// never shifts under a zone with a midnight DST transition (e.g.
// America/Santiago) the way it would if AddDate ran in local time.
var faultEpochUTC = time.Date(2013, 1, 1, 0, 0, 0, 0, time.UTC)

// DecodeFaults parses a 4202 fault-history reply: 10 entries × 7 bytes,
// newest first; 70 or 72 byte payloads observed (entries at the tail — see
// below). ok=false when f fails the op/register/length shape guards — a
// frame that passes them is a valid fault-table reply even when every slot
// is empty (e.g. right after a panel fault reset), so ok=true there too;
// callers distinguish "no faults" from "not decodable" via ok, not len(out).
//
// loc controls the zone the entry timestamps are assembled in; a nil loc
// defaults to time.Local.
func DecodeFaults(f bus.Frame, loc *time.Location) ([]Fault, bool) {
	if loc == nil {
		loc = time.Local
	}
	if f.Op != bus.OpAck06 || len(f.Data) < 3+70 {
		return nil, false
	}
	if f.Data[0] != 0x00 || f.Data[1] != 0x42 || f.Data[2] != 0x02 {
		return nil, false
	}
	p := f.Data[3:]
	// Tail-anchor assumption: on the one payload length seen (72 bytes) the
	// two leading bytes precede the entry table; prior art, no local
	// capture behind it. Confirm at the Task 16 live gate before trusting
	// this on a real 72-byte reply.
	p = p[len(p)-70:]
	var out []Fault
	for i := 0; i+7 <= len(p); i += 7 {
		e := p[i : i+7]
		days := int(u16(e, 4))
		if e[0] == 0 && e[1] == 0 && days == 0 {
			continue // empty slot
		}
		hour, min := int(e[2]), int(e[3])
		if hour > 23 || min > 59 {
			continue // half-corrupt slot: time.Date would silently normalize this
		}
		day := faultEpochUTC.AddDate(0, 0, days)
		out = append(out, Fault{
			Code:   int(e[0]),
			Source: e[1],
			Time:   time.Date(day.Year(), day.Month(), day.Day(), hour, min, 0, 0, loc),
			Active: e[6]&0x80 == 0, // bit 7 INVERTED: 0 = active
			Count:  int(e[6] & 0x7f),
		})
	}
	return out, true
}
