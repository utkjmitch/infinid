package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/utkjmitch/infinid/protocol"
)

func testJournal(t *testing.T) (*Journal, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.jsonl")
	j, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	return j, path
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func lastEvent(t *testing.T, path string) Event {
	t.Helper()
	lines := readLines(t, path)
	var e Event
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &e); err != nil {
		t.Fatalf("decode last line %q: %v", lines[len(lines)-1], err)
	}
	return e
}

func TestDeviceLiveness(t *testing.T) {
	j, path := testJournal(t)
	defer j.Close()
	j.NoteDevice(0x5201, t0)
	j.NoteDevice(0x3e01, t0)
	// ODU silent for 45s while IDU still talks → device_lost for ODU only.
	j.CheckLiveness(t0.Add(45*time.Second), []uint16{0x3e01})
	// It comes back.
	j.NoteDevice(0x5201, t0.Add(60*time.Second))
	lines := readLines(t, path)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, `"device_lost"`) || !strings.Contains(joined, `"5201"`) {
		t.Errorf("missing device_lost: %s", joined)
	}
	if !strings.Contains(joined, `"device_recovered"`) {
		t.Errorf("missing device_recovered: %s", joined)
	}
	lostCount := 0
	for _, l := range lines {
		if strings.Contains(l, `"device_lost"`) {
			lostCount++
			if strings.Contains(l, "3e01") {
				t.Errorf("device_lost fired for 3e01, which stayed live via activeNow: %s", l)
			}
		}
	}
	if lostCount != 1 {
		t.Errorf("want exactly one device_lost, got %d: %s", lostCount, joined)
	}
}

func TestBusSilence(t *testing.T) {
	j, path := testJournal(t)
	defer j.Close()
	j.NoteDevice(0x5201, t0)
	j.CheckLiveness(t0.Add(2*time.Minute), nil) // no frames at all → bus_silent
	j.NoteDevice(0x5201, t0.Add(3*time.Minute))
	joined := strings.Join(readLines(t, path), "\n")
	if !strings.Contains(joined, `"bus_silent"`) || !strings.Contains(joined, `"bus_recovered"`) {
		t.Errorf("missing bus events: %s", joined)
	}
}

func TestBusDeadAtStartup(t *testing.T) {
	j, path := testJournal(t)
	defer j.Close()
	j.CheckLiveness(t0, nil) // virgin journal: nothing heard yet, just arms firstCheck
	j.CheckLiveness(t0.Add(2*time.Minute), nil)
	joined := strings.Join(readLines(t, path), "\n")
	if !strings.Contains(joined, `"bus_silent"`) {
		t.Errorf("want bus_silent when nothing was ever heard at startup: %s", joined)
	}
	j.NoteDevice(0x5201, t0.Add(3*time.Minute))
	joined = strings.Join(readLines(t, path), "\n")
	if !strings.Contains(joined, `"bus_recovered"`) {
		t.Errorf("want bus_recovered once a device talks: %s", joined)
	}
}

func TestDeviceRecoveredViaActiveNow(t *testing.T) {
	j, path := testJournal(t)
	defer j.Close()
	j.NoteDevice(0x5201, t0)
	j.NoteDevice(0x3e01, t0)
	// ODU (0x5201) goes silent long enough to be marked lost, IDU keeps talking.
	j.CheckLiveness(t0.Add(45*time.Second), []uint16{0x3e01})
	joined := strings.Join(readLines(t, path), "\n")
	if !strings.Contains(joined, `"device_lost"`) {
		t.Fatalf("setup: want device_lost before recovery check: %s", joined)
	}
	// ODU starts talking again, delivered via CheckLiveness's activeNow list
	// rather than a direct NoteDevice call.
	j.CheckLiveness(t0.Add(46*time.Second), []uint16{0x5201, 0x3e01})
	joined = strings.Join(readLines(t, path), "\n")
	if !strings.Contains(joined, `"device_recovered"`) {
		t.Errorf("want device_recovered delivered via activeNow: %s", joined)
	}
}

func TestOutageClassification(t *testing.T) {
	j, path := testJournal(t)
	// Journal the reference counters, then simulate a restart over the same file.
	j.NotePowerCounters(map[string]float64{"idu_power_cycles": 24, "odu_power_cycles": 6}, t0)
	j.Close()

	j2, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	// Counters unchanged → the gap was ours (monitoring_gap).
	j2.ClassifyGap(map[string]float64{"idu_power_cycles": 24, "odu_power_cycles": 6}, t0.Add(time.Hour))
	if e := lastEvent(t, path); e.Type != "outage_classified" || e.Gap != "monitoring_gap" {
		t.Errorf("want monitoring_gap, got %+v", e)
	}

	// Counters actually change (25/7) → a fresh power_counters line is appended.
	before := len(readLines(t, path))
	j2.NotePowerCounters(map[string]float64{"idu_power_cycles": 25, "odu_power_cycles": 7}, t0.Add(time.Hour))
	j2.Close()
	after := readLines(t, path)
	if len(after) != before+1 {
		t.Fatalf("want exactly one new power_counters line: before=%d after=%d\n%s", before, len(after), strings.Join(after, "\n"))
	}
	if e := lastEvent(t, path); e.Type != "power_counters" {
		t.Errorf("want power_counters, got %+v", e)
	}

	j3, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	defer j3.Close()
	// Counter bumped from the 25/7 reference → HVAC lost power during the gap.
	j3.ClassifyGap(map[string]float64{"idu_power_cycles": 26, "odu_power_cycles": 8}, t0.Add(2*time.Hour))
	if e := lastEvent(t, path); e.Type != "outage_classified" || e.Gap != "hvac_power_loss" {
		t.Errorf("want hvac_power_loss, got %+v", e)
	}
}

func TestClassifyGapNoReference(t *testing.T) {
	j, path := testJournal(t)
	defer j.Close()
	j.ClassifyGap(map[string]float64{"idu_power_cycles": 5}, t0)
	if e := lastEvent(t, path); e.Type != "outage_classified" || e.Gap != "no_reference" {
		t.Errorf("want no_reference with no prior counters, got %+v", e)
	}
}

func TestFaultTransitions(t *testing.T) {
	j, path := testJournal(t)
	defer j.Close()
	active := []protocol.Fault{{Code: 12, Source: 0x40, Time: t0, Active: true, Count: 1}}
	j.NoteFaults(active, t0)
	j.NoteFaults(active, t0.Add(time.Hour)) // unchanged → no duplicate event
	cleared := []protocol.Fault{{Code: 12, Source: 0x40, Time: t0, Active: false, Count: 1}}
	j.NoteFaults(cleared, t0.Add(2*time.Hour))
	j.NoteFaults(nil, t0.Add(3*time.Hour)) // history wiped → manually_cleared
	lines := readLines(t, path)
	joined := strings.Join(lines, "\n")
	if strings.Count(joined, `"fault_seen"`) != 1 {
		t.Errorf("want exactly one fault_seen: %s", joined)
	}
	if !strings.Contains(joined, `"self_cleared"`) {
		t.Errorf("active→cleared with entry retained must be self_cleared: %s", joined)
	}
	if !strings.Contains(joined, `"manually_cleared"`) {
		t.Errorf("history wipe must be manually_cleared: %s", joined)
	}
}

func TestFaultRecurred(t *testing.T) {
	j, path := testJournal(t)
	faultA := protocol.Fault{Code: 12, Source: 0x40, Time: t0, Active: true, Count: 1}
	j.NoteFaults([]protocol.Fault{faultA}, t0)
	faultARecurred := protocol.Fault{Code: 12, Source: 0x40, Time: t0, Active: true, Count: 3}
	j.NoteFaults([]protocol.Fault{faultARecurred}, t0.Add(time.Hour))

	faultB := protocol.Fault{Code: 20, Source: 0x41, Time: t0, Active: true, Count: 1}
	j.NoteFaults([]protocol.Fault{faultARecurred, faultB}, t0.Add(2*time.Hour))
	faultBCleared := protocol.Fault{Code: 20, Source: 0x41, Time: t0, Active: false, Count: 1}
	j.NoteFaults([]protocol.Fault{faultARecurred, faultBCleared}, t0.Add(3*time.Hour))
	j.Close()

	lines := readLines(t, path)
	joined := strings.Join(lines, "\n")
	if strings.Count(joined, `"fault_recurred"`) != 1 {
		t.Errorf("want exactly one fault_recurred: %s", joined)
	}
	before := len(lines)

	j2, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	defer j2.Close()
	if err := j2.Err(); err != nil {
		t.Fatalf("reopen surfaced an error: %v", err)
	}
	// Replaying the identical current state must not re-emit anything,
	// including no duplicate fault_seen for the already-known faults.
	j2.NoteFaults([]protocol.Fault{faultARecurred, faultBCleared}, t0.Add(4*time.Hour))
	after := readLines(t, path)
	if len(after) != before {
		t.Errorf("replay round-trip re-emitted events: before=%d after=%d\n%s", before, len(after), strings.Join(after, "\n"))
	}
	active := j2.ActiveFaults()
	if len(active) != 1 || active[0].Source != 0x40 || active[0].Count != 3 {
		t.Errorf("replayed active fault wrong, want Source=0x40 Count=3: %+v", active)
	}
}
