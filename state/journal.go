package state

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/utkjmitch/infinid/protocol"
)

// Event is one journal record. The journal is the unit's service history —
// append-only JSONL, replayed on open to recover reference values.
type Event struct {
	TS     time.Time          `json:"ts"`
	Type   string             `json:"type"`
	Device string             `json:"device,omitempty"` // hex bus address
	Fault  *FaultRecord       `json:"fault,omitempty"`
	Gap    string             `json:"gap,omitempty"`    // outage classification
	Values map[string]float64 `json:"values,omitempty"` // power counters
}

// FaultRecord mirrors protocol.Fault plus the resolution cause.
type FaultRecord struct {
	Code       int       `json:"code"`
	Source     string    `json:"source"`
	Time       time.Time `json:"time"`
	Count      int       `json:"count"`
	Active     bool      `json:"active"`
	Resolution string    `json:"resolution,omitempty"` // self_cleared | manually_cleared | unknown
}

const (
	deviceLostAfter = 30 * time.Second
	busSilentAfter  = 60 * time.Second
)

// Journal tracks liveness/faults and appends events to a JSONL file.
type Journal struct {
	mu        sync.Mutex
	f         *os.File
	w         *bufio.Writer
	lastSeen  map[uint16]time.Time
	lost      map[uint16]bool
	busSilent bool
	counters  map[string]float64        // last journaled power counters
	faults    map[string]protocol.Fault // key: code/source/first-time
}

// OpenJournal opens (or creates) the journal at path, replaying existing
// events to recover the last power counters and known-fault set.
func OpenJournal(path string) (*Journal, error) {
	j := &Journal{
		lastSeen: map[uint16]time.Time{},
		lost:     map[uint16]bool{},
		counters: map[string]float64{},
		faults:   map[string]protocol.Fault{},
	}
	if existing, err := os.Open(path); err == nil {
		sc := bufio.NewScanner(existing)
		for sc.Scan() {
			var e Event
			if json.Unmarshal(sc.Bytes(), &e) != nil {
				continue
			}
			if e.Type == "power_counters" {
				j.counters = e.Values
			}
			if e.Type == "fault_seen" && e.Fault != nil {
				var source byte
				if v, err := strconv.ParseUint(e.Fault.Source, 16, 8); err == nil {
					source = byte(v)
				}
				f := protocol.Fault{Code: e.Fault.Code, Source: source, Time: e.Fault.Time,
					Count: e.Fault.Count, Active: e.Fault.Active}
				j.faults[faultKey(f)] = f
			}
			if (e.Type == "fault_cleared" || e.Type == "fault_history_wiped") && e.Fault != nil {
				delete(j.faults, faultKey(protocol.Fault{Code: e.Fault.Code, Time: e.Fault.Time}))
			}
		}
		existing.Close()
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	j.f, j.w = f, bufio.NewWriter(f)
	return j, nil
}

func faultKey(f protocol.Fault) string {
	return fmt.Sprintf("%d/%s", f.Code, f.Time.Format(time.RFC3339))
}

func (j *Journal) append(e Event) {
	line, _ := json.Marshal(e)
	j.w.Write(append(line, '\n'))
	j.w.Flush()
}

// NoteDevice records that addr produced a frame at now.
func (j *Journal) NoteDevice(addr uint16, now time.Time) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.busSilent {
		j.busSilent = false
		j.append(Event{TS: now, Type: "bus_recovered"})
	}
	if j.lost[addr] {
		delete(j.lost, addr)
		j.append(Event{TS: now, Type: "device_recovered", Device: fmt.Sprintf("%04x", addr)})
	}
	j.lastSeen[addr] = now
}

// CheckLiveness runs periodically: activeNow lists devices known to have
// produced frames since the last check (their last-seen refreshes to now;
// callers that NoteDevice per frame pass nil). No devices at all past the
// silence horizon → bus_silent; individual devices silent past
// deviceLostAfter while others talk → device_lost.
func (j *Journal) CheckLiveness(now time.Time, activeNow []uint16) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, a := range activeNow {
		j.lastSeen[a] = now
	}
	newest := time.Time{}
	for _, ts := range j.lastSeen {
		if ts.After(newest) {
			newest = ts
		}
	}
	if !j.busSilent && !newest.IsZero() && now.Sub(newest) > busSilentAfter {
		j.busSilent = true
		j.append(Event{TS: now, Type: "bus_silent"})
		return
	}
	if j.busSilent {
		return // whole-bus outage; individual losses are meaningless
	}
	for addr, ts := range j.lastSeen {
		if !j.lost[addr] && now.Sub(ts) > deviceLostAfter {
			j.lost[addr] = true
			j.append(Event{TS: now, Type: "device_lost", Device: fmt.Sprintf("%04x", addr)})
		}
	}
}

// BusSilent reports whether the whole bus is currently silent.
func (j *Journal) BusSilent() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.busSilent
}

// NotePowerCounters journals the power-on cycle counters when they change —
// the reference values ClassifyGap compares against after a restart.
func (j *Journal) NotePowerCounters(vals map[string]float64, now time.Time) {
	j.mu.Lock()
	defer j.mu.Unlock()
	changed := len(j.counters) != len(vals)
	for k, v := range vals {
		if j.counters[k] != v {
			changed = true
		}
	}
	if !changed {
		return
	}
	j.counters = vals
	j.append(Event{TS: now, Type: "power_counters", Values: vals})
}

// ClassifyGap runs once per daemon start, after the first counter readings
// arrive: unchanged counters → the HVAC ran fine unobserved
// (monitoring_gap); a bumped counter → the HVAC lost power during the gap
// (hvac_power_loss). With no prior reference, the gap is unclassifiable.
// IDU counters commit on a ~daily rollup (08-23 longitudinal finding), so an
// HVAC power loss can be classified as monitoring_gap if it happened since
// the last daily commit — a documented limitation, revisited at the Task 16
// live gate.
func (j *Journal) ClassifyGap(vals map[string]float64, now time.Time) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(j.counters) == 0 {
		j.append(Event{TS: now, Type: "outage_classified", Gap: "no_reference"})
		return
	}
	gap := "monitoring_gap"
	for k, v := range vals {
		if prev, ok := j.counters[k]; ok && v > prev {
			gap = "hvac_power_loss"
		}
	}
	j.append(Event{TS: now, Type: "outage_classified", Gap: gap})
}

// NoteFaults diffs a fresh 4202 read against the known-fault set.
// New entry → fault_seen. Active→cleared with the entry retained →
// fault_cleared/self_cleared. Entire history emptied while faults were
// known → fault_history_wiped/manually_cleared (the panel's reset wipes
// resettable faults; the daemon never clears anything).
func (j *Journal) NoteFaults(current []protocol.Fault, now time.Time) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(current) == 0 && len(j.faults) > 0 {
		for _, f := range j.faults {
			j.append(Event{TS: now, Type: "fault_history_wiped", Fault: &FaultRecord{
				Code: f.Code, Time: f.Time, Count: f.Count,
				Resolution: "manually_cleared"}})
		}
		j.faults = map[string]protocol.Fault{}
		return
	}
	for _, f := range current {
		k := faultKey(f)
		prev, known := j.faults[k]
		switch {
		case !known:
			j.faults[k] = f
			j.append(Event{TS: now, Type: "fault_seen", Fault: recordOf(f, "")})
		case prev.Active && !f.Active:
			j.faults[k] = f
			j.append(Event{TS: now, Type: "fault_cleared", Fault: recordOf(f, "self_cleared")})
		case f.Count > prev.Count:
			j.faults[k] = f
			j.append(Event{TS: now, Type: "fault_recurred", Fault: recordOf(f, "")})
		}
	}
}

func recordOf(f protocol.Fault, resolution string) *FaultRecord {
	return &FaultRecord{Code: f.Code, Source: fmt.Sprintf("%02x", f.Source),
		Time: f.Time, Count: f.Count, Active: f.Active, Resolution: resolution}
}

// ActiveFaults returns the currently-active known faults (unordered).
func (j *Journal) ActiveFaults() []protocol.Fault {
	j.mu.Lock()
	defer j.mu.Unlock()
	var out []protocol.Fault
	for _, f := range j.faults {
		if f.Active {
			out = append(out, f)
		}
	}
	return out
}

// Close flushes and closes the journal file.
func (j *Journal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.w.Flush()
	return j.f.Close()
}
