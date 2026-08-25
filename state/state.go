// Package state assembles protocol readings into a coherent SystemState
// with per-field staleness and zone presence tracking.
package state

import (
	"sync"
	"time"

	"github.com/utkjmitch/infinid/protocol"
)

// Field is one assembled value with its observation time.
type Field struct {
	Value float64   `json:"value"`
	Text  string    `json:"text,omitempty"`
	TS    time.Time `json:"ts"`
	Stale bool      `json:"stale"`
}

// Snapshot is an immutable copy of the assembled state.
type Snapshot struct {
	Sys   map[string]Field         `json:"sys"`
	Zones map[int]map[string]Field `json:"zones"`
}

// horizons: how old a field may be before it reports stale. Fast fields
// ride 10-16 s bus cadences; SAM accessory/fault fields refresh hourly.
var horizons = map[string]time.Duration{
	"filter_life_used":         3 * time.Hour,
	"uv_life_used":             3 * time.Hour,
	"humidifier_pad_life_used": 3 * time.Hour,
	"vent_filter_life_used":    3 * time.Hour,
}

const defaultHorizon = 5 * time.Minute

// State is the concurrent-safe assembler.
type State struct {
	mu    sync.Mutex
	sys   map[string]Field
	zones map[int]map[string]Field
	// maskOnly marks zones established solely by the SAM active_zones
	// bitmask (never by real zone-scoped sensor traffic). Only these zones
	// are eligible for retraction when a later mask clears their bit.
	maskOnly map[int]bool
}

// New returns a State with zone 1 pre-established: the wall control always
// owns a zone, and it is index 1 on every observed system. Zone presence
// after that is driven by two sources: real zone-scoped sensor traffic
// (which is permanent — once a zone is seen on the bus it stays), and the
// SAM active_zones bitmask, which extends the zone set for newly-set bits
// and can also retract a zone it established itself if a later mask clears
// that bit. A mask can never retract a sensor-established zone, and it can
// never remove zone 1.
func New() *State {
	return &State{
		sys:      map[string]Field{},
		zones:    map[int]map[string]Field{1: {}},
		maskOnly: map[int]bool{},
	}
}

// Apply folds one reading into the state. Zone-presence rule: a zone is
// established by zone-sensor traffic (owner 0x21xx-0x28xx) or the SAM
// active_zones bitmask — never by damper slots alone (a wired-but-absent
// slot can read 0x00, indistinguishable from a closed damper).
func (s *State) Apply(r protocol.Reading) {
	s.mu.Lock()
	defer s.mu.Unlock()

	f := Field{Value: r.Value, Text: r.Text, TS: r.TS}

	if r.Zone == 0 {
		// Out-of-order guard: the bus loop delivers readings monotonically
		// today; this protects a future replay/journal feed from letting an
		// older reading clobber a newer one. Equal timestamps still apply.
		if existing, ok := s.sys[r.Field]; ok && r.TS.Before(existing.TS) {
			return
		}
		if r.Field == "active_zones" {
			s.applyActiveZonesMask(int(r.Value))
		}
		s.sys[r.Field] = f
		return
	}

	isDamper := r.Field == "damper_position" || r.Field == "damper_cmd"
	zf := s.zones[r.Zone]
	if zf == nil {
		if isDamper {
			return // dampers alone don't establish zones
		}
		zf = map[string]Field{}
		s.zones[r.Zone] = zf
	}
	if existing, ok := zf[r.Field]; ok && r.TS.Before(existing.TS) {
		return // out-of-order guard, see above
	}
	if !isDamper {
		// Real zone-scoped sensor traffic: this zone is no longer solely
		// mask-established, so a later mask update must not retract it.
		delete(s.maskOnly, r.Zone)
	}
	zf[r.Field] = f

	// The 00041F decoder only emits hold_remaining_min while a timed hold is
	// active. When the hold ends, the wall control pushes hold=0 and the
	// countdown field simply stops arriving on the bus (08-23 evidence:
	// expiry zeroes-then-stops) — so without this, Snapshot would show a
	// stale countdown forever. hold_permanent (SAM 3B03 bitmap field) is a
	// distinct field and must not trigger this; nor does hold_remaining_min_sam
	// (SAM's own distinct key, cleared by its own unconditional-emit-0).
	if r.Field == "hold" && r.Value == 0 {
		delete(zf, "hold_remaining_min")
	}
}

// applyActiveZonesMask reconciles zone presence against a SAM active_zones
// bitmask read: it creates zones for newly-set bits (marking them
// mask-only) and retracts zones whose bit has cleared, but only if that
// zone is still mask-only — a zone that ever saw real sensor traffic
// survives a mask that clears its bit. Zone 1 is never removed.
func (s *State) applyActiveZonesMask(mask int) {
	for z := 1; z <= 8; z++ {
		if mask&(1<<(z-1)) != 0 {
			if s.zones[z] == nil {
				s.zones[z] = map[string]Field{}
				s.maskOnly[z] = true
			}
			continue
		}
		if z != 1 && s.maskOnly[z] {
			delete(s.zones, z)
			delete(s.maskOnly, z)
		}
	}
}

// Snapshot copies the state, marking staleness as of now.
func (s *State) Snapshot(now time.Time) Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := Snapshot{Sys: map[string]Field{}, Zones: map[int]map[string]Field{}}
	for k, f := range s.sys {
		snap.Sys[k] = staled(k, f, now)
	}
	for z, zf := range s.zones {
		out := map[string]Field{}
		for k, f := range zf {
			out[k] = staled(k, f, now)
		}
		snap.Zones[z] = out
	}
	return snap
}

func staled(field string, f Field, now time.Time) Field {
	h, ok := horizons[field]
	if !ok {
		h = defaultHorizon
	}
	f.Stale = now.Sub(f.TS) > h
	return f
}
