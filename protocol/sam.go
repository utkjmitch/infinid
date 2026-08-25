package protocol

import (
	"time"

	"github.com/utkjmitch/infinid/bus"
)

// SAM-served registers (wall control replies to an active 0x92 reader).
// Layouts per prior art — infinitive/infinitesp/infinitude agree on every
// offset used here; live-validated before the SAM scheduler ships enabled.

// modeNames — 3B02 byte 22 low nibble. Values 0-2 agree across all
// sources; 3+ conflict between source generations, so they decode to the
// raw value with Text "unknown" until live-verified.
var modeNames = []string{"heat", "cool", "auto"}

// systemState3B02 — active_zones(0), temps u8[8]@3, RH u8[8]@11, OAT i8@20,
// stage/mode@22, minutes-since-midnight u16@26.
func systemState3B02(p []byte) []Reading {
	if len(p) < 29 {
		return nil
	}
	zones := p[0]
	rs := []Reading{{Field: "active_zones", Value: float64(zones)}}
	for z := 1; z <= 8; z++ {
		if zones&(1<<(z-1)) == 0 {
			continue
		}
		rs = append(rs,
			Reading{Zone: z, Field: "temp", Value: float64(p[3+z-1])},
			Reading{Zone: z, Field: "humidity", Value: float64(p[11+z-1])},
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
		hold := 0.0
		if p[11]&(1<<(z-1)) != 0 {
			hold = 1
		}
		rs = append(rs,
			Reading{Zone: z, Field: "fan_mode", Value: float64(p[3+z-1]), Text: fanText(p[3+z-1])},
			Reading{Zone: z, Field: "hold", Value: hold},
			Reading{Zone: z, Field: "heat_setpoint", Value: float64(p[12+z-1])},
			Reading{Zone: z, Field: "cool_setpoint", Value: float64(p[20+z-1])},
		)
		if mins := u16(p, 38+2*(z-1)); mins > 0 {
			rs = append(rs, Reading{Zone: z, Field: "hold_remaining_min", Value: float64(mins)})
		}
	}
	return rs
}

// accessoryLife3B05 — consumed % at fixed offsets (0 = new, 100 = replace).
func accessoryLife3B05(p []byte) []Reading {
	if len(p) < 7 {
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

// faultEpoch — 4202 day counts are days since 2013-01-01 (nonstandard,
// verified against cloud equipment_events by prior art).
var faultEpoch = time.Date(2013, 1, 1, 0, 0, 0, 0, time.Local)

// DecodeFaults parses a 4202 fault-history reply: 10 entries × 7 bytes,
// newest first; 70 or 72 byte payloads observed (entries at the tail).
// ok=false when f is not a 4202 reply or holds no entries.
func DecodeFaults(f bus.Frame) ([]Fault, bool) {
	if f.Op != bus.OpAck06 || len(f.Data) < 3+70 {
		return nil, false
	}
	if f.Data[0] != 0x00 || f.Data[1] != 0x42 || f.Data[2] != 0x02 {
		return nil, false
	}
	p := f.Data[3:]
	p = p[len(p)-70:] // entries occupy the final 70 bytes
	var out []Fault
	for i := 0; i+7 <= len(p); i += 7 {
		e := p[i : i+7]
		days := int(u16(e, 4))
		if e[0] == 0 && e[1] == 0 && days == 0 {
			continue // empty slot
		}
		day := faultEpoch.AddDate(0, 0, days)
		out = append(out, Fault{
			Code:   int(e[0]),
			Source: e[1],
			Time:   time.Date(day.Year(), day.Month(), day.Day(), int(e[2]), int(e[3]), 0, 0, time.Local),
			Active: e[6]&0x80 == 0, // bit 7 INVERTED: 0 = active
			Count:  int(e[6] & 0x7f),
		})
	}
	return out, len(out) > 0
}
