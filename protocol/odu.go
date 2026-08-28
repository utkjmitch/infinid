package protocol

import (
	"encoding/binary"
	"math"
)

func f32(p []byte, i int) float64 {
	return float64(math.Float32frombits(binary.BigEndian.Uint32(p[i : i+4])))
}

// oduShortStatus — 000303: [2..3] u16/16 suction pressure PSIG.
func oduShortStatus(p []byte) []Reading {
	if len(p) < 4 {
		return nil
	}
	return []Reading{{Field: "suction_pressure", Value: float64(u16(p, 2)) / 16.0}}
}

// oduStatus — 000304: [7] line voltage.
func oduStatus(p []byte) []Reading {
	if len(p) < 8 {
		return nil
	}
	return []Reading{{Field: "line_voltage", Value: float64(p[7])}}
}

// compressorRPM — 000604: [0..1] target RPM, [2..3] actual RPM.
// [4..23] are static per-stage tables — constants, not published.
func compressorRPM(p []byte) []Reading {
	if len(p) < 4 {
		return nil
	}
	return []Reading{
		{Field: "compressor_rpm_target", Value: float64(u16(p, 0))},
		{Field: "compressor_rpm", Value: float64(u16(p, 2))},
	}
}

// compressorStageCmd — 000605 WRITE: [0..3] float32 commanded stage,
// [4] mode flag 01=cool 00=heat.
func compressorStageCmd(p []byte) []Reading {
	if len(p) < 5 {
		return nil
	}
	stage := f32(p, 0)
	// NaN/±Inf are not real commanded-stage values (garbage or a bus
	// transient) and downstream JSON marshalling errors on them, so drop
	// the reading rather than publish an unencodable float.
	if math.IsNaN(stage) || math.IsInf(stage, 0) {
		return nil
	}
	return []Reading{
		{Field: "compressor_stage_cmd", Value: stage},
		{Field: "cool_mode_flag", Value: float64(p[4])},
	}
}

// compressorStage — 00060E: [0] actual stage index (u8, 0=off 1-5).
func compressorStage(p []byte) []Reading {
	if len(p) < 1 {
		return nil
	}
	return []Reading{{Field: "compressor_stage", Value: float64(p[0])}}
}

// oduAnalog — 000625: [0..1] u16 power-like analog (units unverified —
// decoded for the comparator/REST view, not published to MQTT).
func oduAnalog(p []byte) []Reading {
	if len(p) < 2 {
		return nil
	}
	return []Reading{{Field: "odu_power_analog", Value: float64(u16(p, 0))}}
}

// counterName maps verified KV counter keys to fields, per owner and
// register (000310 = cycle counts, 000311 = lifetime hours). Byte-verified
// against the panel's counter pages 2026-08-13. Unverified keys → "".
func counterName(reg uint32, owner uint16, key byte) string {
	switch {
	case reg == 0x000310 && owner == 0x3e01:
		switch key {
		case 0x23:
			return "heat_stage1_cycles"
		case 0x24:
			return "heat_stage2_cycles"
		case 0x2b:
			return "idu_power_cycles"
		case 0x2d:
			return "blower_cycles"
		}
	case reg == 0x000311 && owner == 0x3e01:
		switch key {
		case 0x25:
			return "heat_stage1_hours"
		case 0x26:
			return "heat_stage2_hours"
		case 0x2c:
			return "idu_power_hours"
		case 0x2e:
			return "blower_hours"
		}
	case reg == 0x000310 && owner == 0x5201:
		switch key {
		case 0x28:
			return "cool_cycles"
		case 0x2b:
			return "odu_power_cycles"
		}
	case reg == 0x000311 && owner == 0x5201:
		switch key {
		case 0x2a:
			return "cool_hours"
		case 0x2c:
			return "odu_power_hours"
		}
	}
	return ""
}

// counters — 000310/000311: rows of key(u8) value(u24 BE), stride 4 bytes.
func counters(reg uint32, owner uint16, p []byte) []Reading {
	var rs []Reading
	for i := 0; i+4 <= len(p); i += 4 {
		field := counterName(reg, owner, p[i])
		if field == "" {
			continue
		}
		v := uint32(p[i+1])<<16 | uint32(p[i+2])<<8 | uint32(p[i+3])
		rs = append(rs, Reading{Field: field, Value: float64(v)})
	}
	return rs
}
