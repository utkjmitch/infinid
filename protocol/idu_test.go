package protocol

import (
	"math"
	"testing"
	"time"

	"github.com/utkjmitch/infinid/bus"
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

// TestAirflowCommandHeatStageHigh pins heat_stage=0x02 (gas furnace high
// stage): no fixture exercises this value (all fixtures carry 0x00), so
// this is a synthetic frame.
func TestAirflowCommandHeatStageHigh(t *testing.T) {
	f := bus.Frame{Src: 0x2001, Dst: 0x3e01, Op: bus.OpWrite,
		Data: []byte{0x00, 0x03, 0x05, 0x02, 0x00, 0x02, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x78}}
	rs, ok := Decode(f, time.Now())
	if !ok {
		t.Fatal("ok = false, want true")
	}
	heat := find(rs, 0x3e01, "heat_stage")
	if len(heat) != 1 || heat[0].Value != 2 {
		t.Fatalf("heat_stage = %+v, want [2]", heat)
	}
}

// TestBlowerStatusEcho — 000306: [1..2] blower RPM echo, [3..4] commanded
// CFM echo of the 000305 command.
func TestBlowerStatusEcho(t *testing.T) {
	rs := decodeAll(t)
	rpm := find(rs, 0x3e01, "blower_rpm_echo")
	cfm := find(rs, 0x3e01, "commanded_cfm_echo")
	if len(rpm) != 3 || rpm[0].Value != 500 || rpm[2].Value != 505 {
		t.Fatalf("blower_rpm_echo = %+v, want [500 495 505]", rpm)
	}
	if len(cfm) != 3 || cfm[0].Value != 514 {
		t.Fatalf("commanded_cfm_echo = %+v, want first 514", cfm)
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

// TestBlowerTelemetryNaN pins per-field NaN/±Inf rejection: a non-finite
// static_pressure must not suppress the still-good supply_cfm/blower_rpm/
// blower_watts readings in the same frame (mirrors compressorStageCmd).
func TestBlowerTelemetryNaN(t *testing.T) {
	// float32 NaN (0x7fc00000) at the static-pressure offset, valid watts.
	f := bus.Frame{Src: 0x3e01, Dst: 0x2001, Op: bus.OpAck06,
		Data: []byte{
			0x00, 0x04, 0x13, // register
			0x01, 0xf4, // supply_cfm = 500
			0x02, 0x00, // blower_rpm = 512
			0x7f, 0xc0, 0x00, 0x00, // static_pressure = NaN
			0x42, 0x5c, 0xdc, 0x4a, // blower_watts = ~55.22
		}}
	rs, ok := Decode(f, time.Now())
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if got := find(rs, 0x3e01, "supply_cfm"); len(got) != 1 || got[0].Value != 500 {
		t.Errorf("supply_cfm = %+v, want [500]", got)
	}
	if got := find(rs, 0x3e01, "blower_rpm"); len(got) != 1 || got[0].Value != 512 {
		t.Errorf("blower_rpm = %+v, want [512]", got)
	}
	if got := find(rs, 0x3e01, "blower_watts"); len(got) != 1 {
		t.Errorf("blower_watts = %+v, want present", got)
	}
	if got := find(rs, 0x3e01, "static_pressure"); len(got) != 0 {
		t.Errorf("static_pressure = %+v, want absent (NaN dropped)", got)
	}
}

func TestBusClock(t *testing.T) {
	rs := decodeAll(t)
	h := find(rs, 0xf1f1, "bus_time_minutes")
	if len(h) != 2 || h[0].Value != 6*60+10 {
		t.Fatalf("bus_time_minutes = %+v, want first 370", h)
	}
	wd := find(rs, 0xf1f1, "bus_weekday")
	if len(wd) != 2 || wd[0].Value != 4 || wd[1].Value != 4 {
		t.Fatalf("bus_weekday = %+v, want [4 4]", wd)
	}
	y := find(rs, 0xf1f1, "bus_year")
	if len(y) != 2 || y[0].Value != 2026 {
		t.Fatalf("bus_year = %+v", y)
	}
	d := find(rs, 0xf1f1, "bus_day")
	if len(d) != 2 || d[0].Value != 13 || d[1].Value != 12 {
		t.Fatalf("bus_day = %+v, want [13 12]", d)
	}
	m := find(rs, 0xf1f1, "bus_month")
	if len(m) != 2 || m[0].Value != 8 || m[1].Value != 8 {
		t.Fatalf("bus_month = %+v, want [8 8]", m)
	}
}
