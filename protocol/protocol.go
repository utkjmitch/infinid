// Package protocol turns verified bus frames into typed readings. Only
// registers whose byte layouts are verified in docs/protocol-tables.md get
// decoders; everything else returns ok=false so callers archive it
// (ADR-0001: the fix is verification, not transcription).
package protocol

import (
	"time"

	"github.com/utkjmitch/infinid/bus"
)

// Reading is one decoded field observation.
type Reading struct {
	Owner uint16 // bus address of the register's owner
	Reg   uint32 // register, e.g. 0x000302
	Zone  int    // 1-based zone index; 0 = not zone-scoped
	Field string // stable snake_case identifier
	Value float64
	Text  string // set for enum fields; Value carries the raw number
	TS    time.Time
}

func u16(p []byte, i int) uint16 { return uint16(p[i])<<8 | uint16(p[i+1]) }

// Decode returns readings for f when (owner, register) has a verified
// decoder. ok=false means "not decodable" — the caller counts and archives.
func Decode(f bus.Frame, ts time.Time) ([]Reading, bool) {
	var owner uint16
	switch f.Op {
	case bus.OpAck06:
		owner = f.Src
	case bus.OpWrite:
		owner = f.Dst
	default:
		return nil, false
	}
	if len(f.Data) < 4 {
		return nil, false
	}
	reg := uint32(f.Data[0])<<16 | uint32(f.Data[1])<<8 | uint32(f.Data[2])
	p := f.Data[3:]

	// found reflects "a verified decoder matched" the register, independent
	// of how many readings it produced — a decoder that legitimately sees
	// zero present entries (e.g. an all-absent TLV set) is still ok=true.
	var rs []Reading
	found := false
	switch reg {
	case 0x000302:
		rs = tlvTemps(owner, p)
		found = true
	case 0x000303:
		if owner == 0x5201 {
			rs = oduShortStatus(p)
			found = true
		}
	case 0x000304:
		if owner == 0x5201 {
			rs = oduStatus(p)
			found = true
		}
	case 0x000604:
		if owner == 0x5201 {
			rs = compressorRPM(p)
			found = true
		}
	case 0x000605:
		if owner == 0x5201 && f.Op == bus.OpWrite {
			rs = compressorStageCmd(p)
			found = true
		}
	case 0x00060e:
		if owner == 0x5201 {
			rs = compressorStage(p)
			found = true
		}
	case 0x000625:
		if owner == 0x5201 {
			rs = oduAnalog(p)
			found = true
		}
	case 0x000310, 0x000311:
		if owner == 0x3e01 || owner == 0x5201 {
			rs = counters(reg, owner, p)
			found = true
		}
	case 0x000305:
		if owner == 0x3e01 && f.Op == bus.OpWrite {
			rs = airflowCmd(p)
			found = true
		}
	case 0x000306:
		if owner == 0x3e01 {
			rs = blowerStatus(p)
			found = true
		}
	case 0x000413:
		if owner == 0x3e01 {
			rs = blowerTelemetry(p)
			found = true
		}
	case 0x000308:
		if owner == 0x6001 && f.Op == bus.OpWrite {
			rs = dampers(reg, p)
			found = true
		}
	case 0x000319:
		if owner == 0x6001 && f.Op == bus.OpAck06 {
			rs = dampers(reg, p)
			found = true
		}
	case 0x00041f:
		if f.Op == bus.OpWrite {
			if z := zoneIndex(f.Dst); z != 0 {
				rs = zoneConfigPush(z, p)
				found = true
			}
		}
	case 0x00041e:
		if f.Op == bus.OpAck06 {
			if z := zoneIndex(owner); z != 0 {
				rs = zoneSensorStatus(z, p)
				found = true
			}
		}
	case 0x000202:
		if owner == 0xf1f1 {
			rs = busTime(p)
			found = true
		}
	case 0x000203:
		if owner == 0xf1f1 {
			rs = busDate(p)
			found = true
		}
	default:
		return nil, false
	}
	if !found {
		return nil, false
	}

	for i := range rs {
		rs[i].Owner = owner
		rs[i].Reg = reg
		rs[i].TS = ts
	}
	return rs, true
}

// tlvNames maps verified TLV temperature ids (000302, any owner) to fields.
// Verified: 0x11/0x12/0x30/0x4a/0x45 on the ODU, 0x14 (supply air / LAT) on
// the IDU. Id 0x4b is observed but unidentified — deliberately absent.
var tlvNames = map[byte]string{
	0x11: "outdoor_temp",
	0x12: "outdoor_coil_temp",
	0x14: "supply_air_temp",
	0x30: "suction_temp",
	0x45: "discharge_temp",
	0x4a: "superheat",
}

// tlvTemps decodes the 4-byte TLV rows: tag(01=present) id value(int16 BE /16 °F).
// Signed per docs/protocol-tables.md Conventions (int16 BE / 16, prior-art
// verified against a hooked thermistor); sub-zero range isn't yet
// live-verified on this bus (winter capture pending), but the layout is.
func tlvTemps(_ uint16, p []byte) []Reading {
	var rs []Reading
	for i := 0; i+4 <= len(p); i += 4 {
		if p[i] != 0x01 {
			continue
		}
		field, known := tlvNames[p[i+1]]
		if !known {
			continue
		}
		rs = append(rs, Reading{Field: field, Value: float64(int16(u16(p, i+2))) / 16.0})
	}
	return rs
}
