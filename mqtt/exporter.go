package mqtt

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/utkjmitch/infinid/state"
)

// Publisher is the transport seam — the paho adapter in production, a fake
// in tests.
type Publisher interface {
	Publish(topic string, payload []byte, retain bool) error
}

// Config parameterizes topics and zone naming. ZoneNames[i] names zone
// index i+1; missing names fall back to "zone_<n>".
type Config struct {
	BaseTopic       string
	DiscoveryPrefix string
	ZoneNames       []string
	Version         string
}

const heartbeat = 60 * time.Second

// Exporter publishes discovery + state with change detection.
//
// Concurrency: not safe for concurrent use. All methods must be called
// from a single goroutine — the discovery/last-payload/heartbeat maps are
// unsynchronized by design. In production that single caller is the Task
// 11 publish loop. Publisher implementations only need to be safe to call
// from that same goroutine; the Exporter adds no synchronization of its
// own.
type Exporter struct {
	p          Publisher
	cfg        Config
	discovered map[string]bool   // object ids with discovery published
	last       map[string]string // state topic → last payload
	lastBeat   time.Time
}

// New builds an Exporter.
func New(p Publisher, cfg Config) *Exporter {
	return &Exporter{p: p, cfg: cfg,
		discovered: map[string]bool{}, last: map[string]string{}}
}

func (e *Exporter) zoneName(z int) string {
	if z-1 < len(e.cfg.ZoneNames) && z >= 1 && e.cfg.ZoneNames[z-1] != "" {
		return e.cfg.ZoneNames[z-1]
	}
	return fmt.Sprintf("zone_%d", z)
}

func (e *Exporter) availabilityTopic() string { return e.cfg.BaseTopic + "/availability" }

// device blocks: one hub device, one device per zone (via_device → hub).
func (e *Exporter) hubDevice() map[string]any {
	return map[string]any{
		"identifiers": []string{"infinid"}, "name": "infinid",
		"manufacturer": "infinid", "model": "ABCD bus daemon",
		"sw_version": e.cfg.Version,
	}
}

func (e *Exporter) zoneDevice(z int) map[string]any {
	return map[string]any{
		"identifiers":  []string{fmt.Sprintf("infinid_zone_%d", z)},
		"name":         "Zone " + e.zoneName(z),
		"manufacturer": "infinid", "model": "Infinity zone",
		"via_device": "infinid",
	}
}

// PublishDiscovery publishes retained discovery configs for the hub
// entities and each zone in zones. Idempotent per object id — call freely
// when new zones appear.
func (e *Exporter) PublishDiscovery(zones []int) {
	for _, def := range sysEntities {
		e.publishConfig(def, def.object, e.hubDevice())
	}
	for _, z := range zones {
		for _, def := range zoneEntities {
			object := fmt.Sprintf(def.object, e.zoneName(z))
			e.publishConfig(def, object, e.zoneDevice(z))
		}
	}
}

func (e *Exporter) publishConfig(def entityDef, object string, device map[string]any) {
	id := "infinid_" + object
	if e.discovered[id] {
		return
	}
	cfg := map[string]any{
		"name":               def.name,
		"unique_id":          id,
		"object_id":          id,
		"state_topic":        fmt.Sprintf("%s/%s/state", e.cfg.BaseTopic, object),
		"availability_topic": e.availabilityTopic(),
		"device":             device,
	}
	if def.unit != "" {
		cfg["unit_of_measurement"] = def.unit
	}
	if def.deviceClass != "" {
		cfg["device_class"] = def.deviceClass
	}
	if def.stateClass != "" {
		cfg["state_class"] = def.stateClass
	}
	payload, _ := json.Marshal(cfg)
	topic := fmt.Sprintf("%s/sensor/%s/config", e.cfg.DiscoveryPrefix, id)
	if e.p.Publish(topic, payload, true) == nil {
		e.discovered[id] = true
	}
}

// PublishState publishes changed fields (retained) and re-publishes
// everything on the heartbeat so a restarted broker/HA converges. Stale
// fields publish "None" — unknown beats stale-as-fresh. A field present in
// an earlier snapshot but absent from this one (a timed hold clearing, a
// mask-retracted zone) is actively retracted to "None" rather than left
// frozen at its last retained value.
func (e *Exporter) PublishState(snap state.Snapshot, now time.Time) {
	beat := now.Sub(e.lastBeat) >= heartbeat
	if beat {
		e.lastBeat = now
	}
	produced := map[string]bool{}
	for _, def := range sysEntities {
		if f, ok := snap.Sys[def.field]; ok {
			produced[e.publishField(def, def.object, f, beat)] = true
		}
	}
	for z, zf := range snap.Zones {
		for _, def := range zoneEntities {
			if f, ok := zf[def.field]; ok {
				produced[e.publishField(def, fmt.Sprintf(def.object, e.zoneName(z)), f, beat)] = true
			}
		}
	}
	for topic := range e.last {
		if produced[topic] {
			continue
		}
		// Retraction, not a normal publish: only drop the last-payload
		// record on success so a failed publish is retried next cycle
		// instead of silently forgotten.
		if e.p.Publish(topic, []byte("None"), true) == nil {
			delete(e.last, topic)
		}
	}
}

func (e *Exporter) publishField(def entityDef, object string, f state.Field, beat bool) string {
	var payload string
	switch {
	case f.Stale:
		payload = "None"
	case def.text:
		payload = f.Text
	case def.transform != nil:
		payload = def.transform(f.Value)
	default:
		payload = strconv.FormatFloat(f.Value, 'f', -1, 64)
	}
	topic := fmt.Sprintf("%s/%s/state", e.cfg.BaseTopic, object)
	if !beat && e.last[topic] == payload {
		return topic
	}
	if e.p.Publish(topic, []byte(payload), true) == nil {
		e.last[topic] = payload
	}
	return topic
}

// PublishAvailability publishes the retained availability flag. Bus
// silence maps to offline — HA must degrade to unavailable, never show
// stale data as fresh. The publish error is returned rather than
// swallowed: a failed startup "online" publish must be visible to the
// caller, not leave every HA entity silently Unavailable.
func (e *Exporter) PublishAvailability(online bool) error {
	v := "offline"
	if online {
		v = "online"
	}
	return e.p.Publish(e.availabilityTopic(), []byte(v), true)
}
