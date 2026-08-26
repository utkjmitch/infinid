// Package mqtt publishes Home Assistant MQTT-discovery entities from state
// snapshots. Contract: docs/MQTT-CONTRACT.md — the 12 diagnostic ids are
// frozen verbatim; everything else is additive-only.
package mqtt

import "fmt"

// entityDef maps one state field to one HA entity.
type entityDef struct {
	field       string // key in Snapshot.Sys or Snapshot.Zones[z]
	object      string // object/unique id suffix after "infinid_"; zone defs use %s = zone name
	name        string
	unit        string
	deviceClass string
	stateClass  string
	transform   func(v float64) string // optional; default = trimmed float
	text        bool                   // publish Field.Text instead of Value
}

func pct15(v float64) string { return fmt.Sprintf("%.0f", v/15.0*100.0) }

// inv100 publishes bus-native "consumed %" fields as the remaining % every
// consumer convention expects (decided in plan grill; REST keeps the raw
// used value).
func inv100(v float64) string { return fmt.Sprintf("%.0f", 100.0-v) }

// sysEntities: system-level sensors. The first 9 + the damper pattern are
// the frozen dashboard contract.
var sysEntities = []entityDef{
	{field: "compressor_stage", object: "compressor_stage", name: "Compressor stage"},
	{field: "compressor_rpm", object: "compressor_rpm", name: "Compressor RPM", unit: "rpm"},
	{field: "supply_cfm", object: "supply_cfm", name: "Supply airflow", unit: "CFM"},
	{field: "blower_rpm", object: "blower_rpm", name: "Blower RPM", unit: "rpm"},
	{field: "static_pressure", object: "static_pressure", name: "Static pressure", unit: "inH2O", stateClass: "measurement"},
	{field: "blower_watts", object: "blower_watts", name: "Blower power", unit: "W", deviceClass: "power", stateClass: "measurement"},
	{field: "suction_pressure", object: "suction_pressure", name: "Suction pressure", unit: "psi", deviceClass: "pressure", stateClass: "measurement"},
	{field: "outdoor_coil_temp", object: "outdoor_coil_temp", name: "Outdoor coil", unit: "°F", deviceClass: "temperature", stateClass: "measurement"},
	{field: "discharge_temp", object: "discharge_temp", name: "Discharge temp", unit: "°F", deviceClass: "temperature", stateClass: "measurement"},
	// Additive beyond the contract:
	{field: "outdoor_temp", object: "outdoor_temp", name: "Outdoor temp", unit: "°F", deviceClass: "temperature", stateClass: "measurement"},
	{field: "supply_air_temp", object: "supply_air_temp", name: "Supply air temp", unit: "°F", deviceClass: "temperature", stateClass: "measurement"},
	{field: "suction_temp", object: "suction_temp", name: "Suction temp", unit: "°F", deviceClass: "temperature", stateClass: "measurement"},
	{field: "superheat", object: "superheat", name: "Superheat", unit: "°F", stateClass: "measurement"},
	{field: "line_voltage", object: "line_voltage", name: "Line voltage", unit: "V", deviceClass: "voltage", stateClass: "measurement"},
	{field: "filter_life_used", object: "filter_life", name: "Filter life", unit: "%", transform: inv100},
	{field: "heat_stage1_cycles", object: "heat_stage1_cycles", name: "Heat stage 1 cycles", stateClass: "total_increasing"},
	{field: "heat_stage2_cycles", object: "heat_stage2_cycles", name: "Heat stage 2 cycles", stateClass: "total_increasing"},
	{field: "blower_cycles", object: "blower_cycles", name: "Blower cycles", stateClass: "total_increasing"},
	{field: "cool_cycles", object: "cool_cycles", name: "Cool cycles", stateClass: "total_increasing"},
	{field: "heat_stage1_hours", object: "heat_stage1_hours", name: "Heat stage 1 hours", unit: "h", stateClass: "total_increasing"},
	{field: "heat_stage2_hours", object: "heat_stage2_hours", name: "Heat stage 2 hours", unit: "h", stateClass: "total_increasing"},
	{field: "blower_hours", object: "blower_hours", name: "Blower hours", unit: "h", stateClass: "total_increasing"},
	{field: "cool_hours", object: "cool_hours", name: "Cool hours", unit: "h", stateClass: "total_increasing"},
	{field: "idu_power_cycles", object: "idu_power_cycles", name: "Indoor unit power cycles", stateClass: "total_increasing"},
	{field: "odu_power_cycles", object: "odu_power_cycles", name: "Outdoor unit power cycles", stateClass: "total_increasing"},
	{field: "system_mode", object: "system_mode", name: "System mode", text: true},
}

// zoneEntities: per-zone sensors; object pattern "zone_<name>_<suffix>",
// except the damper which is the frozen "damper_<name>" contract id.
//
// The six *_sam fields produced by the SAM decoders (temp_sam,
// humidity_sam, fan_mode_sam, heat_setpoint_sam, cool_setpoint_sam,
// hold_remaining_min_sam — plus system-level outdoor_temp_sam) are
// deliberately not published here: they exist for the REST debug surface
// and the validation comparator only, to avoid duplicate HA entities that
// would diverge only by source.
var zoneEntities = []entityDef{
	{field: "temp", object: "zone_%s_temp", name: "Temperature", unit: "°F", deviceClass: "temperature", stateClass: "measurement"},
	{field: "humidity", object: "zone_%s_humidity", name: "Humidity", unit: "%", deviceClass: "humidity", stateClass: "measurement"},
	{field: "cool_setpoint", object: "zone_%s_cool_setpoint", name: "Cool setpoint", unit: "°F", deviceClass: "temperature"},
	{field: "heat_setpoint", object: "zone_%s_heat_setpoint", name: "Heat setpoint", unit: "°F", deviceClass: "temperature"},
	{field: "fan_mode", object: "zone_%s_fan_mode", name: "Fan mode", text: true},
	{field: "hold", object: "zone_%s_hold", name: "Hold"},
	{field: "hold_permanent", object: "zone_%s_hold_permanent", name: "Hold (permanent)"},
	{field: "hold_remaining_min", object: "zone_%s_hold_remaining", name: "Hold remaining", unit: "min"},
	{field: "damper_position", object: "damper_%s", name: "Damper", unit: "%", transform: pct15},
}
