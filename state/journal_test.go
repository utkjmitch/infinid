package state

import (
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

func TestOutageClassification(t *testing.T) {
	j, path := testJournal(t)
	// Journal the reference counters, then simulate a restart over the same file.
	j.NotePowerCounters(map[string]float64{"idu_power_cycles": 24, "odu_power_cycles": 6}, t0)
	j.Close()

	j2, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	defer j2.Close()
	// Counters unchanged → the gap was ours (monitoring_gap).
	j2.ClassifyGap(map[string]float64{"idu_power_cycles": 24, "odu_power_cycles": 6}, t0.Add(time.Hour))
	joined := strings.Join(readLines(t, path), "\n")
	if !strings.Contains(joined, `"monitoring_gap"`) {
		t.Errorf("want monitoring_gap: %s", joined)
	}

	// Counter bumped → HVAC lost power during the gap.
	j2.NotePowerCounters(map[string]float64{"idu_power_cycles": 24, "odu_power_cycles": 6}, t0.Add(time.Hour))
	j2.Close()
	j3, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	defer j3.Close()
	j3.ClassifyGap(map[string]float64{"idu_power_cycles": 25, "odu_power_cycles": 7}, t0.Add(2*time.Hour))
	joined = strings.Join(readLines(t, path), "\n")
	if !strings.Contains(joined, `"hvac_power_loss"`) {
		t.Errorf("want hvac_power_loss: %s", joined)
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
