package protocol

import (
	"math"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 0.01 }

func TestAirflowCommand(t *testing.T) {
	rs := decodeAll(t)
	cfm := find(rs, 0x3e01, "commanded_cfm")
	if len(cfm) != 4 || cfm[0].Value != 514 || cfm[3].Value != 512 {
		t.Fatalf("commanded_cfm = %+v", cfm)
	}
	heat := find(rs, 0x3e01, "heat_stage")
	if len(heat) != 4 || heat[0].Value != 0 {
		t.Fatalf("heat_stage = %+v", heat)
	}
	cool := find(rs, 0x3e01, "cool_demand")
	// First three fixtures 0x02 (cooling), last 0x00.
	if len(cool) != 4 || cool[0].Value != 1 || cool[3].Value != 0 {
		t.Fatalf("cool_demand = %+v", cool)
	}
}

func TestBlowerTelemetry(t *testing.T) {
	rs := decodeAll(t)
	cfm := find(rs, 0x3e01, "supply_cfm")
	rpm := find(rs, 0x3e01, "blower_rpm")
	sp := find(rs, 0x3e01, "static_pressure")
	w := find(rs, 0x3e01, "blower_watts")
	if len(cfm) != 3 || cfm[0].Value != 495 {
		t.Fatalf("supply_cfm = %+v", cfm)
	}
	if len(rpm) != 3 || rpm[0].Value != 495 || rpm[2].Value != 505 {
		t.Fatalf("blower_rpm = %+v", rpm)
	}
	if !near(sp[0].Value, 0.1871) {
		t.Errorf("static_pressure = %v, want ~0.1871", sp[0].Value)
	}
	if !near(w[0].Value, 55.22) {
		t.Errorf("blower_watts = %v, want ~55.22", w[0].Value)
	}
}

func TestBusClock(t *testing.T) {
	rs := decodeAll(t)
	h := find(rs, 0xf1f1, "bus_time_minutes")
	if len(h) != 2 || h[0].Value != 6*60+10 {
		t.Fatalf("bus_time_minutes = %+v, want first 370", h)
	}
	y := find(rs, 0xf1f1, "bus_year")
	if len(y) != 2 || y[0].Value != 2026 {
		t.Fatalf("bus_year = %+v", y)
	}
}
