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
	p   Publisher
	cfg Config
	// discovered is keyed by "component/id" (e.g. "sensor/infinid_bus_online"),
	// not just id, so a future object-name collision between a sensor and a
	// binary_sensor can't silently skip one of the two discovery publishes.
	discovered map[string]bool
	last       map[string]string // state topic → last payload
	lastHealth map[string]string // health state topic → last payload; kept
	// separate from last so PublishState's absent-field retraction pass
	// (which only knows about state.Snapshot fields) never sees health
	// topics as unproduced and retracts them to "None".
	lastBeat       time.Time
	lastHealthBeat time.Time
}

// New builds an Exporter.
func New(p Publisher, cfg Config) *Exporter {
	return &Exporter{p: p, cfg: cfg,
		discovered: map[string]bool{}, last: map[string]string{}, lastHealth: map[string]string{}}
}

// Reassert clears all discovery and change-detection state so the next
// publish cycle re-sends everything, including discovery configs. Call this
// after an MQTT reconnect — a broker restart or a new session may have
// dropped every retained message the previous session believed was still
// there — from the same single goroutine that calls the other Exporter
// methods.
func (e *Exporter) Reassert() {
	e.discovered = map[string]bool{}
	e.last = map[string]string{}
	e.lastHealth = map[string]string{}
	e.lastBeat = time.Time{}
	e.lastHealthBeat = time.Time{}
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
		e.publishConfig(def, def.object, e.hubDevice(), "sensor")
	}
	for _, def := range healthEntities {
		e.publishConfig(def, def.object, e.hubDevice(), "sensor")
	}
	for _, def := range binaryEntities {
		e.publishConfig(def, def.object, e.hubDevice(), "binary_sensor")
	}
	for _, z := range zones {
		for _, def := range zoneEntities {
			object := fmt.Sprintf(def.object, e.zoneName(z))
			e.publishConfig(def, object, e.zoneDevice(z), "sensor")
		}
	}
}

func (e *Exporter) publishConfig(def entityDef, object string, device map[string]any, component string) {
	id := "infinid_" + object
	discKey := component + "/" + id
	if e.discovered[discKey] {
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
	topic := fmt.Sprintf("%s/%s/%s/config", e.cfg.DiscoveryPrefix, component, id)
	if e.p.Publish(topic, payload, true) == nil {
		e.discovered[discKey] = true
	}
}

// PublishState publishes changed fields (retained) and re-publishes
// everything on the heartbeat so a restarted broker/HA converges. Stale
// fields publish "None" — unknown beats stale-as-fresh. A field present in
// an earlier snapshot but absent from this one (a timed hold clearing, a
// mask-retracted zone) is actively retracted to "None" rather than left
// frozen at its last retained value.
//
// Beat convergence: the produced/last retraction pass below only reaches
// topics this Exporter remembers publishing — e.last, which starts empty on
// construction and is wiped by Reassert. A field that vanished from state
// while the exporter's memory was reset (a daemon restart, a reconnect)
// would otherwise never be touched again, leaving its last retained broker
// value frozen forever regardless of what the snapshot now says. So on a
// beat (including the always-a-beat first call after New/Reassert, since
// lastBeat is zero), this walks every sysEntities def and, for each zone
// present in the snapshot, every zoneEntities def — not just the fields
// actually present — publishing "None" for any def whose field is absent.
// That "None" goes through publishField's own last-payload map with
// beat=false, so it only sends once: repeated beats with the field still
// absent stay quiet, exactly like every other change-detected publish. Zones
// absent from the snapshot entirely are left untouched (no topics invented
// for a zone that doesn't exist), and a zone that vanished entirely while
// the daemon was down staying frozen at its last value is an accepted
// residual — see docs/MQTT-CONTRACT.md's Staleness section.
//
// Side effect kept deliberately: a sys field that has never once been seen
// (e.g. a SAM-only field on a passive-only install) now publishes "None" on
// the very first beat — honest Unknown instead of publishing nothing at
// all.
func (e *Exporter) PublishState(snap state.Snapshot, now time.Time) {
	beat := now.Sub(e.lastBeat) >= heartbeat
	if beat {
		e.lastBeat = now
	}
	produced := map[string]bool{}
	for _, def := range sysEntities {
		if f, ok := snap.Sys[def.field]; ok {
			produced[e.publishField(def, def.object, f, beat)] = true
		} else if beat {
			produced[e.publishAbsentField(def, def.object)] = true
		}
	}
	for z, zf := range snap.Zones {
		for _, def := range zoneEntities {
			object := fmt.Sprintf(def.object, e.zoneName(z))
			if f, ok := zf[def.field]; ok {
				produced[e.publishField(def, object, f, beat)] = true
			} else if beat {
				produced[e.publishAbsentField(def, object)] = true
			}
		}
	}
	for topic := range e.last {
		if produced[topic] {
			continue
		}
		if e.last[topic] == "None" {
			// Already converged to None — by this same pass on an earlier
			// cycle, by a beat's absent-field convergence above, or by the
			// retraction send below on some prior call. Leave it recorded
			// rather than deleting it: deleting here would erase the
			// change-detection memory that keeps the next beat's
			// publishAbsentField call quiet, turning "repeat beats don't
			// spam" into "spam every heartbeat forever" for any field that
			// stays absent indefinitely.
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

// publishAbsentField records a synthetic "None" for a def whose field is
// absent from this snapshot. It always calls publishField with beat=false
// — even when PublishState itself is on a beat — so the send goes through
// ordinary change-detection: the first observed absence publishes "None"
// and records it in e.last, and every subsequent beat with the field still
// absent finds e.last already equal to "None" and stays quiet.
func (e *Exporter) publishAbsentField(def entityDef, object string) string {
	return e.publishField(def, object, state.Field{Stale: true}, false)
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

// Health is the daemon's own vitals, published alongside decoded state.
type Health struct {
	FramesPerMin  float64
	UnknownFrames float64
	SAMFailures   float64
	BusOnline     bool
	FaultActive   bool
	FaultCount    float64
	LastFault     string
}

// PublishHealth publishes daemon vitals and fault summary entities. It uses
// its own change-detection map (lastHealth) and its own heartbeat
// (lastHealthBeat), distinct from PublishState's (last, lastBeat) —
// health fields are always present so they need no retraction, and sharing
// the state-topic map would cause PublishState to retract every health
// topic to "None" on its next call. The heartbeat mirrors PublishState's:
// without it, an unchanging health snapshot would never republish, so a
// broker that lost its retained messages (restart, reconnect without a
// persistent session) would leave fault/bus indicators absent until the
// underlying value next changes.
func (e *Exporter) PublishHealth(h Health, now time.Time) {
	beat := now.Sub(e.lastHealthBeat) >= heartbeat
	if beat {
		e.lastHealthBeat = now
	}
	pub := func(object, payload string) {
		topic := fmt.Sprintf("%s/%s/state", e.cfg.BaseTopic, object)
		if !beat && e.lastHealth[topic] == payload {
			return
		}
		if e.p.Publish(topic, []byte(payload), true) == nil {
			e.lastHealth[topic] = payload
		}
	}
	num := func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
	onoff := func(b bool) string {
		if b {
			return "ON"
		}
		return "OFF"
	}
	lastFault := h.LastFault
	if lastFault == "" {
		// An empty retained payload is an MQTT retained-delete: publishing
		// "" would erase the previous run's fault string on the broker
		// instead of showing "no fault", and — before any fault has ever
		// occurred — change detection would see ""=="" for the missing key
		// and never publish anything at all.
		lastFault = "None"
	}
	pub("frames_per_min", num(h.FramesPerMin))
	pub("unknown_frames", num(h.UnknownFrames))
	pub("sam_failures", num(h.SAMFailures))
	pub("fault_count", num(h.FaultCount))
	pub("last_fault", lastFault)
	pub("fault_active", onoff(h.FaultActive))
	pub("bus_online", onoff(h.BusOnline))
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
