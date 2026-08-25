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
	Stale bool      `json:"stale,omitempty"`
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
}

// New returns a State with zone 1 pre-established: the wall control always
// owns a zone, and it is index 1 on every observed system. The SAM
// active_zones bitmask corrects the zone set when SAM reads are enabled.
func New() *State {
	return &State{
		sys:   map[string]Field{},
		zones: map[int]map[string]Field{1: {}},
	}
}

// Apply folds one reading into the state. Zone-presence rule: a zone is
// established by zone-sensor traffic (owner 0x21xx-0x28xx) or the SAM
// active_zones bitmask — never by damper slots alone (a wired-but-absent
// slot can read 0x00, indistinguishable from a closed damper).
func (s *State) Apply(r protocol.Reading) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if r.Field == "active_zones" {
		mask := int(r.Value)
		for z := 1; z <= 8; z++ {
			if mask&(1<<(z-1)) != 0 && s.zones[z] == nil {
				s.zones[z] = map[string]Field{}
			}
		}
	}

	f := Field{Value: r.Value, Text: r.Text, TS: r.TS}
	if r.Zone == 0 {
		s.sys[r.Field] = f
		return
	}
	zf := s.zones[r.Zone]
	if zf == nil {
		if r.Field == "damper_position" || r.Field == "damper_cmd" {
			return // dampers alone don't establish zones
		}
		zf = map[string]Field{}
		s.zones[r.Zone] = zf
	}
	zf[r.Field] = f

	// The 00041F decoder only emits hold_remaining_min while a timed hold is
	// active. When the hold ends, the wall control pushes hold=0 and the
	// countdown field simply stops arriving on the bus (08-23 evidence:
	// expiry zeroes-then-stops) — so without this, Snapshot would show a
	// stale countdown forever. hold_permanent (SAM 3B03 bitmap field) is a
	// distinct field and must not trigger this.
	if r.Field == "hold" && r.Value == 0 {
		delete(zf, "hold_remaining_min")
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
