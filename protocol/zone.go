package protocol

// fanModes — 00041F[5] / 3B03 fan enum. Acts as a floor, not a direct
// blower command.
var fanModes = []string{"auto", "low", "med", "high"}

func fanText(v byte) string {
	if int(v) < len(fanModes) {
		return fanModes[v]
	}
	return "unknown"
}

// zoneIndex maps a zone-sensor bus address (0x2101..0x2801) to its 1-based
// zone index; 0 when addr is not a zone sensor.
func zoneIndex(addr uint16) int {
	hi := int(addr >> 8)
	if hi >= 0x21 && hi <= 0x28 && addr&0xff == 0x01 {
		return hi - 0x20
	}
	return 0
}

// dampers — 000308 (command) / 000319 (feedback): one byte per zone index,
// 0x00-0x0F open fraction; 0xFF marks an absent slot (0x00 does NOT prove a
// zone absent — see the zone-presence rule in state).
func dampers(reg uint32, p []byte) []Reading {
	if len(p) < 8 {
		return nil
	}
	field := "damper_cmd"
	if reg == 0x000319 {
		field = "damper_position"
	}
	var rs []Reading
	for i := 0; i < len(p) && i < 8; i++ {
		if p[i] == 0xff {
			continue
		}
		rs = append(rs, Reading{Zone: i + 1, Field: field, Value: float64(p[i])})
	}
	return rs
}

// zoneConfigPush — 00041F WRITE 2001→sensor: [1]=0x18 timed-hold marker with
// [3..4] u16 BE remaining 2-second ticks, [5] fan enum, [6] heat setpoint
// °F, [7] cool setpoint °F. [0] bit7 was once thought to be an
// indefinite-hold flag (08-13 session), but 08-23 longitudinal evidence
// shows 0x80 is simply the steady-idle value of [0] on both zone sensors
// with no hold active — see
// docs/experiments/2026-08-23-longitudinal-findings.md. Permanent/indefinite
// hold has no verified bus indicator yet, so it stays undecoded (ADR-0001).
func zoneConfigPush(zone int, p []byte) []Reading {
	if zone == 0 || len(p) < 8 {
		return nil
	}
	hold := 0.0
	if p[1] == 0x18 {
		hold = 1
	}
	rs := []Reading{
		{Zone: zone, Field: "hold", Value: hold},
		{Zone: zone, Field: "fan_mode", Value: float64(p[5]), Text: fanText(p[5])},
		{Zone: zone, Field: "heat_setpoint", Value: float64(p[6])},
		{Zone: zone, Field: "cool_setpoint", Value: float64(p[7])},
	}
	if p[1] == 0x18 {
		rs = append(rs, Reading{Zone: zone, Field: "hold_remaining_min",
			Value: float64(u16(p, 3)) / 30.0}) // 2-second ticks → minutes
	}
	return rs
}

// zoneSensorStatus — 00041E from sensor: [9..10] u16 BE temp ×16 (HIGH
// confidence), [12] RH % (MED confidence per docs/protocol-tables.md).
func zoneSensorStatus(zone int, p []byte) []Reading {
	if zone == 0 || len(p) < 13 {
		return nil
	}
	return []Reading{
		{Zone: zone, Field: "temp", Value: float64(u16(p, 9)) / 16.0},
		{Zone: zone, Field: "humidity", Value: float64(p[12])},
	}
}
