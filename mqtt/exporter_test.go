package mqtt

import (
	"encoding/json"
	"errors"
	"strings"
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
		if cfg["object_id"] != id {
			t.Errorf("%s object_id = %v", id, cfg["object_id"])
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
	if err := json.Unmarshal([]byte(p.msgs[ztopic][0]), &zcfg); err != nil {
		t.Fatalf("zone config unmarshal: %v", err)
	}
	dev, ok := zcfg["device"].(map[string]any)
	if !ok {
		t.Fatalf("zone config device field missing/wrong type: %#v", zcfg["device"])
	}
	ids, ok := dev["identifiers"].([]any)
	if !ok || len(ids) == 0 {
		t.Fatalf("zone device identifiers missing/wrong type: %#v", dev["identifiers"])
	}
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
	if !p.ret[topic] {
		t.Errorf("state topic %s not retained", topic)
	}
	// Heartbeat boundary is inclusive (>=): exactly 60s after the first
	// publish must republish even though the value is unchanged.
	e.PublishState(snap, t0.Add(60*time.Second))
	if len(p.msgs[topic]) != 2 {
		t.Fatalf("heartbeat republish missing at exact 60s boundary")
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

// TestAbsentFieldRetraction pins the fix for a field that disappears from
// the snapshot (state.go deletes hold_remaining_min when a timed hold
// clears; a mask-retracted zone vanishes the same way): the exporter must
// actively retract the retained topic to "None" rather than leaving the
// last real value frozen on the broker forever. Once retracted, repeated
// absence must not re-send.
func TestAbsentFieldRetraction(t *testing.T) {
	p := newFake()
	e := New(p, testConfig())
	topic := "infinid/zone_bedrooms_hold_remaining/state"

	withHold := snapWith(nil, map[int]map[string]state.Field{
		1: {"hold_remaining_min": {Value: 5, TS: t0}},
	})
	e.PublishState(withHold, t0)
	if got := p.msgs[topic]; len(got) != 1 || got[0] != "5" {
		t.Fatalf("initial payload = %v, want [5]", got)
	}

	withoutHold := snapWith(nil, map[int]map[string]state.Field{1: {}})
	e.PublishState(withoutHold, t0.Add(time.Second))
	got := p.msgs[topic]
	if len(got) != 2 || got[1] != "None" {
		t.Fatalf("retraction payload = %v, want [5 None]", got)
	}

	// Field stays absent: no repeated retraction sends.
	e.PublishState(withoutHold, t0.Add(2*time.Second))
	if got := p.msgs[topic]; len(got) != 2 {
		t.Fatalf("retracted field republished: %v", got)
	}
}

// TestFloatFormattingTransforms pins fixed-precision formatting for
// float32-sourced fields that otherwise emit 17-digit garbage
// (0.3499999940395355, 412.70001220703125, 3.3333333333333335).
func TestFloatFormattingTransforms(t *testing.T) {
	p := newFake()
	e := New(p, testConfig())
	snap := snapWith(map[string]state.Field{
		"static_pressure": {Value: 0.3499999940395355, TS: t0},
		"blower_watts":    {Value: 412.70001220703125, TS: t0},
	}, map[int]map[string]state.Field{
		1: {"hold_remaining_min": {Value: 3.3333333333333335, TS: t0}},
	})
	e.PublishState(snap, t0)
	if got := p.msgs["infinid/static_pressure/state"]; len(got) != 1 || got[0] != "0.35" {
		t.Errorf("static_pressure payload = %v, want [0.35]", got)
	}
	if got := p.msgs["infinid/blower_watts/state"]; len(got) != 1 || got[0] != "413" {
		t.Errorf("blower_watts payload = %v, want [413]", got)
	}
	if got := p.msgs["infinid/zone_bedrooms_hold_remaining/state"]; len(got) != 1 || got[0] != "3" {
		t.Errorf("hold_remaining_min payload = %v, want [3]", got)
	}
}

// TestFilterLifeInverseTransform covers the only inverting transform:
// the bus reports "life used", HA shows "life remaining".
func TestFilterLifeInverseTransform(t *testing.T) {
	p := newFake()
	e := New(p, testConfig())
	snap := snapWith(map[string]state.Field{
		"filter_life_used": {Value: 30, TS: t0},
	}, nil)
	e.PublishState(snap, t0)
	if got := p.msgs["infinid/filter_life/state"]; len(got) != 1 || got[0] != "70" {
		t.Fatalf("filter_life payload = %v, want [70]", got)
	}
}

// TestStaleThenFreshRecoveryRepublishes pins the value/None/value sequence:
// a field going stale must publish "None", and recovering to the same
// fresh value afterward must republish it (not be swallowed by change
// detection against the pre-stale payload).
func TestStaleThenFreshRecoveryRepublishes(t *testing.T) {
	p := newFake()
	e := New(p, testConfig())
	topic := "infinid/zone_bedrooms_fan_mode/state"
	fresh := snapWith(nil, map[int]map[string]state.Field{
		1: {"fan_mode": {Value: 2, Text: "med", TS: t0}},
	})
	stale := snapWith(nil, map[int]map[string]state.Field{
		1: {"fan_mode": {Value: 2, Text: "med", TS: t0, Stale: true}},
	})
	e.PublishState(fresh, t0)
	e.PublishState(stale, t0.Add(time.Second))
	e.PublishState(fresh, t0.Add(2*time.Second))
	want := []string{"med", "None", "med"}
	got := p.msgs[topic]
	if len(got) != len(want) {
		t.Fatalf("payload sequence = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("payload sequence = %v, want %v", got, want)
		}
	}
}

// TestZoneNameFallbackBeyondConfigured pins the documented fallback:
// zones beyond len(ZoneNames) get zoneName "zone_<n>", which the zone_%s_*
// object pattern then embeds as "zone_zone_<n>_*".
func TestZoneNameFallbackBeyondConfigured(t *testing.T) {
	p := newFake()
	e := New(p, testConfig()) // only 3 zone names configured
	snap := snapWith(nil, map[int]map[string]state.Field{
		5: {"temp": {Value: 70, TS: t0}},
	})
	e.PublishState(snap, t0)
	if got := p.msgs["infinid/zone_zone_5_temp/state"]; len(got) != 1 || got[0] != "70" {
		t.Fatalf("zone_5 fallback payload = %v, want [70]", got)
	}
}

type errPub struct{ err error }

func (f *errPub) Publish(topic string, payload []byte, retain bool) error { return f.err }

// TestPublishAvailabilityReturnsError pins that a failed publish is
// surfaced to the caller rather than swallowed — a failed startup
// "online" publish must not silently leave every HA entity Unavailable.
func TestPublishAvailabilityReturnsError(t *testing.T) {
	wantErr := errors.New("boom")
	e := New(&errPub{err: wantErr}, testConfig())
	if err := e.PublishAvailability(true); err != wantErr {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}

func TestFaultAndHealthEntities(t *testing.T) {
	p := newFake()
	e := New(p, testConfig())
	e.PublishDiscovery(nil)
	for _, id := range []string{"infinid_last_fault", "infinid_fault_count", "infinid_frames_per_min", "infinid_unknown_frames"} {
		if len(p.msgs["homeassistant/sensor/"+id+"/config"]) == 0 {
			t.Errorf("no discovery for %s", id)
		}
	}
	for _, id := range []string{"infinid_fault_active", "infinid_bus_online"} {
		if len(p.msgs["homeassistant/binary_sensor/"+id+"/config"]) == 0 {
			t.Errorf("no discovery for binary_sensor %s", id)
		}
	}
	e.PublishHealth(Health{FramesPerMin: 1500, UnknownFrames: 12, SAMFailures: 0,
		BusOnline: true, FaultCount: 2, LastFault: "171 @ 2026-07-28 09:16 (x1)", FaultActive: false}, t0)
	if got := p.msgs["infinid/frames_per_min/state"]; len(got) != 1 || got[0] != "1500" {
		t.Errorf("frames_per_min = %v", got)
	}
	if got := p.msgs["infinid/bus_online/state"]; len(got) != 1 || got[0] != "ON" {
		t.Errorf("bus_online = %v", got)
	}
	if got := p.msgs["infinid/last_fault/state"]; len(got) != 1 || !strings.Contains(got[0], "171") {
		t.Errorf("last_fault = %v", got)
	}
}

// TestHealthNotRetractedByPublishState pins the amendment fixing a collision
// between PublishHealth and PublishState's absent-field retraction pass:
// health topics live in their own change-detection map so a subsequent
// PublishState call (which only knows about state.Snapshot fields) must not
// see health topics as "produced by nobody" and retract them to "None".
func TestHealthNotRetractedByPublishState(t *testing.T) {
	p := newFake()
	e := New(p, testConfig())
	snap := snapWith(map[string]state.Field{
		"suction_pressure": {Value: 121, TS: t0},
	}, nil)
	e.PublishState(snap, t0)
	e.PublishHealth(Health{FramesPerMin: 1500, UnknownFrames: 12, SAMFailures: 0,
		BusOnline: true, FaultCount: 2, LastFault: "171 @ 2026-07-28 09:16 (x1)", FaultActive: false}, t0)
	// A second PublishState call must not touch the health topics at all.
	e.PublishState(snap, t0.Add(time.Second))
	topic := "infinid/bus_online/state"
	got := p.msgs[topic]
	if len(got) != 1 {
		t.Fatalf("bus_online payload history = %v, want exactly one entry", got)
	}
	if got[0] != "ON" {
		t.Fatalf("bus_online payload = %v, want [ON]", got)
	}
}
