package protocol

import "math"

// airflowCmd — 000305 WRITE 2001→IDU: [0] commanded heat stage (gas furnace
// 01 low / 02 high), [2] cool-demand flag (0x02 cooling), [4..5] u16 BE
// commanded CFM (0 cedes airflow control to the IDU).
func airflowCmd(p []byte) []Reading {
	if len(p) < 6 {
		return nil
	}
	cool := 0.0
	if p[2] == 0x02 {
		cool = 1
	}
	return []Reading{
		{Field: "heat_stage", Value: float64(p[0])},
		{Field: "cool_demand", Value: cool},
		{Field: "commanded_cfm", Value: float64(u16(p, 4))},
	}
}

// blowerStatus — 000306: [1..2] blower RPM, [3..4] CFM echo of the 000305
// command. Redundant with 000413 (measured) — decoded for the comparator
// and REST view, not published.
func blowerStatus(p []byte) []Reading {
	if len(p) < 5 {
		return nil
	}
	return []Reading{
		{Field: "blower_rpm_echo", Value: float64(u16(p, 1))},
		{Field: "commanded_cfm_echo", Value: float64(u16(p, 3))},
	}
}

// blowerTelemetry — 000413: [0..1] measured CFM, [2..3] RPM,
// [4..7] float32 static pressure in-wc, [8..11] float32 blower watts.
// Float fields are MED confidence per docs/protocol-tables.md.
func blowerTelemetry(p []byte) []Reading {
	if len(p) < 12 {
		return nil
	}
	rs := []Reading{
		{Field: "supply_cfm", Value: float64(u16(p, 0))},
		{Field: "blower_rpm", Value: float64(u16(p, 2))},
	}
	// NaN/±Inf are not real sensor values (garbage or a bus transient) and
	// downstream JSON marshalling errors on them, so drop just the affected
	// field rather than the whole frame (mirrors compressorStageCmd).
	if sp := f32(p, 4); !math.IsNaN(sp) && !math.IsInf(sp, 0) {
		rs = append(rs, Reading{Field: "static_pressure", Value: sp})
	}
	if w := f32(p, 8); !math.IsNaN(w) && !math.IsInf(w, 0) {
		rs = append(rs, Reading{Field: "blower_watts", Value: w})
	}
	return rs
}

// busTime — 000202 broadcast: hour, minute, weekday.
func busTime(p []byte) []Reading {
	if len(p) < 3 {
		return nil
	}
	return []Reading{
		{Field: "bus_time_minutes", Value: float64(int(p[0])*60 + int(p[1]))},
		{Field: "bus_weekday", Value: float64(p[2])},
	}
}

// busDate — 000203 broadcast: day, month, year-2000.
func busDate(p []byte) []Reading {
	if len(p) < 3 {
		return nil
	}
	return []Reading{
		{Field: "bus_day", Value: float64(p[0])},
		{Field: "bus_month", Value: float64(p[1])},
		{Field: "bus_year", Value: float64(2000 + int(p[2]))},
	}
}
