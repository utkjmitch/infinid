package mqtt

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/utkjmitch/infinid/state"
)

var t0 = time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)

type fakePub struct {
	msgs map[string][]string // topic → payloads in order
	ret  map[string]bool
}

func newFake() *fakePub { return &fakePub{msgs: map[string][]string{}, ret: map[string]bool{}} }

func (f *fakePub) Publish(topic string, payload []byte, retain bool) error {
	f.msgs[topic] = append(f.msgs[topic], string(payload))
	f.ret[topic] = retain
	return nil
}

func testConfig() Config {
	return Config{BaseTopic: "infinid", DiscoveryPrefix: "homeassistant",
		ZoneNames: []string{"bedrooms", "living_room", "basement"}, Version: "test"}
}

func snapWith(sys map[string]state.Field, zones map[int]map[string]state.Field) state.Snapshot {
	if sys == nil {
		sys = map[string]state.Field{}
	}
	if zones == nil {
		zones = map[int]map[string]state.Field{}
	}
	return state.Snapshot{Sys: sys, Zones: zones}
}

func TestDiscoveryContractIDs(t *testing.T) {
	p := newFake()
	e := New(p, testConfig())
	e.PublishDiscovery([]int{1, 2, 3})
	// The 12 frozen contract ids — object_id and unique_id must match verbatim.
	contract := []string{
		"infinid_compressor_stage", "infinid_compressor_rpm", "infinid_supply_cfm",
		"infinid_blower_rpm", "infinid_static_pressure", "infinid_blower_watts",
		"infinid_suction_pressure", "infinid_outdoor_coil_temp", "infinid_discharge_temp",
		"infinid_damper_bedrooms", "infinid_damper_living_room", "infinid_damper_basement",
	}
	for _, id := range contract {
		topic := "homeassistant/sensor/" + id + "/config"
		if len(p.msgs[topic]) == 0 {
			t.Errorf("no discovery for %s", id)
			continue
		}
		if !p.ret[topic] {
			t.Errorf("%s discovery not retained", id)
		}
		var cfg map[string]any
		if err := json.Unmarshal([]byte(p.msgs[topic][0]), &cfg); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if cfg["unique_id"] != id {
			t.Errorf("%s unique_id = %v", id, cfg["unique_id"])
		}
		if cfg["availability_topic"] != "infinid/availability" {
			t.Errorf("%s availability = %v", id, cfg["availability_topic"])
		}
	}
	// Zone devices: zone 2 temp entity exists and belongs to the zone device.
	ztopic := "homeassistant/sensor/infinid_zone_living_room_temp/config"
	if len(p.msgs[ztopic]) == 0 {
		t.Fatal("no zone temp discovery")
	}
	var zcfg map[string]any
	json.Unmarshal([]byte(p.msgs[ztopic][0]), &zcfg)
	dev := zcfg["device"].(map[string]any)
	ids := dev["identifiers"].([]any)
	if ids[0] != "infinid_zone_2" {
		t.Errorf("zone device identifiers = %v", ids)
	}
}

func TestStatePublishChangeDetection(t *testing.T) {
	p := newFake()
	e := New(p, testConfig())
	snap := snapWith(map[string]state.Field{
		"suction_pressure": {Value: 121, TS: t0},
	}, nil)
	e.PublishState(snap, t0)
	e.PublishState(snap, t0.Add(time.Second)) // unchanged → no republish
	topic := "infinid/suction_pressure/state"
	if len(p.msgs[topic]) != 1 {
		t.Fatalf("published %d times, want 1 (change detection)", len(p.msgs[topic]))
	}
	if p.msgs[topic][0] != "121" {
		t.Errorf("payload = %q", p.msgs[topic][0])
	}
	// Heartbeat: after 60s everything republishes even unchanged.
	e.PublishState(snap, t0.Add(61*time.Second))
	if len(p.msgs[topic]) != 2 {
		t.Fatalf("heartbeat republish missing")
	}
}

func TestDamperPercentTransform(t *testing.T) {
	p := newFake()
	e := New(p, testConfig())
	snap := snapWith(nil, map[int]map[string]state.Field{
		2: {"damper_position": {Value: 7, TS: t0}},
	})
	e.PublishState(snap, t0)
	got := p.msgs["infinid/damper_living_room/state"]
	if len(got) != 1 || got[0] != "47" { // round(7/15*100)
		t.Fatalf("damper payload = %v, want [47]", got)
	}
}

func TestTextAndStaleFields(t *testing.T) {
	p := newFake()
	e := New(p, testConfig())
	snap := snapWith(nil, map[int]map[string]state.Field{
		1: {"fan_mode": {Value: 2, Text: "med", TS: t0}},
	})
	e.PublishState(snap, t0)
	if got := p.msgs["infinid/zone_bedrooms_fan_mode/state"]; len(got) != 1 || got[0] != "med" {
		t.Fatalf("fan_mode payload = %v", got)
	}
	// Stale fields publish "None" so HA shows unknown instead of stale-as-fresh.
	stale := snapWith(nil, map[int]map[string]state.Field{
		1: {"fan_mode": {Value: 2, Text: "med", TS: t0, Stale: true}},
	})
	e.PublishState(stale, t0.Add(2*time.Minute))
	msgs := p.msgs["infinid/zone_bedrooms_fan_mode/state"]
	if msgs[len(msgs)-1] != "None" {
		t.Fatalf("stale payload = %q, want None", msgs[len(msgs)-1])
	}
}

// TestHoldPermanentField pins the amendment splitting the SAM 3B03 bitmap's
// permanent hold from the timed hold field: both must publish, on their own
// distinct topic.
func TestHoldPermanentField(t *testing.T) {
	p := newFake()
	e := New(p, testConfig())
	snap := snapWith(nil, map[int]map[string]state.Field{
		1: {"hold_permanent": {Value: 1, TS: t0}},
	})
	e.PublishState(snap, t0)
	got := p.msgs["infinid/zone_bedrooms_hold_permanent/state"]
	if len(got) != 1 || got[0] != "1" {
		t.Fatalf("hold_permanent payload = %v, want [1]", got)
	}
}
