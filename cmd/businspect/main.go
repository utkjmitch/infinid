// Command businspect turns a capture.jsonl stream (capture/recorder.go) into
// decode leads: register inventory, change timelines, event diffs, and a
// best-effort faults view. It reads from -in (default stdin), so
// `ssh root@pi cat /share/infinid/capture.jsonl | businspect tables` works.
package main

import (
	"encoding/binary"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/utkjmitch/infinid/inspect"
)

// Bus ops relevant here. Mirrors bus.Op* without importing the bus package;
// businspect is stdlib-only.
const (
	opAck06 = uint8(0x06)
	opRead  = uint8(0x0b)
	opNack  = uint8(0x15)
	opAlarm = uint8(0x1e)
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch cmd := os.Args[1]; cmd {
	case "tables":
		runTables(os.Args[2:])
	case "timeline":
		runTimeline(os.Args[2:])
	case "diff":
		runDiff(os.Args[2:])
	case "alarms":
		runAlarms(os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "businspect: unknown subcommand %q\n\n", cmd)
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage: businspect <tables|timeline|diff|alarms> [flags]

  tables                                register inventory + READ-poll cadence
  timeline -reg <hex> [-owner <4-hex>]  chronological payload changes for a register
  diff -at <RFC3339> [-window 2m]       before/after diff around an instant
  alarms                                ALARM/NACK frames + fault-history (reg 004202) decode

shared flag: -in <path>   input file, default "-" (stdin)
`)
}

// loadRecs opens inPath (or stdin) and parses it, exiting the process on any
// I/O or usage error. A note about skipped malformed lines is printed to
// stderr when there were any.
func loadRecs(inPath string) []inspect.Rec {
	f, err := openInput(inPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "businspect: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	recs, skipped, err := inspect.ParseAll(f)
	if err != nil {
		fmt.Fprintf(os.Stderr, "businspect: %v\n", err)
		os.Exit(1)
	}
	if skipped > 0 {
		fmt.Fprintf(os.Stderr, "businspect: skipped %d malformed line(s)\n", skipped)
	}
	return recs
}

func openInput(path string) (io.ReadCloser, error) {
	if path == "" || path == "-" {
		return io.NopCloser(os.Stdin), nil
	}
	return os.Open(path)
}

func parseHex16(s string) (uint16, error) {
	v, err := strconv.ParseUint(s, 16, 16)
	if err != nil {
		return 0, err
	}
	return uint16(v), nil
}

func sortedGroupKeys(groups map[inspect.GroupKey]*inspect.Stats) []inspect.GroupKey {
	keys := make([]inspect.GroupKey, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Owner != keys[j].Owner {
			return keys[i].Owner < keys[j].Owner
		}
		return keys[i].Reg < keys[j].Reg
	})
	return keys
}

// runTables implements `businspect tables`.
func runTables(args []string) {
	fs := flag.NewFlagSet("tables", flag.ExitOnError)
	in := fs.String("in", "-", "input file (default stdin)")
	fs.Parse(args)

	recs := loadRecs(*in)
	groups := inspect.Group(recs)

	fmt.Printf("%-6s %-8s %6s %5s %8s %-16s %s\n", "owner", "reg", "count", "len", "distinct", "changed-offsets", "first..last")
	for _, k := range sortedGroupKeys(groups) {
		st := groups[k]
		fmt.Printf("%04x   %-8s %6d %5d %8d %-16v %s..%s\n",
			k.Owner, k.Reg, st.Count, st.PayloadLen, len(st.Distinct), st.ChangedOffsets,
			st.First.Format(time.RFC3339), st.Last.Format(time.RFC3339))
	}

	fmt.Println()
	fmt.Println("READ-poll cadence (avg interval, n polls; registers with <2 polls omitted):")
	cadence := readCadence(recs)
	ckeys := make([]inspect.GroupKey, 0, len(cadence))
	for k := range cadence {
		ckeys = append(ckeys, k)
	}
	sort.Slice(ckeys, func(i, j int) bool {
		if ckeys[i].Owner != ckeys[j].Owner {
			return ckeys[i].Owner < ckeys[j].Owner
		}
		return ckeys[i].Reg < ckeys[j].Reg
	})
	if len(ckeys) == 0 {
		fmt.Println("  (none)")
	}
	for _, k := range ckeys {
		c := cadence[k]
		fmt.Printf("  %04x %-8s avg %-10s n=%d\n", k.Owner, k.Reg, c.avg, c.n)
	}
}

type cadenceStat struct {
	avg time.Duration
	n   int
}

// readCadence computes, per (owner, reg), the average interval between
// consecutive READ-poll timestamps. Registers with fewer than two polls are
// omitted.
func readCadence(recs []inspect.Rec) map[inspect.GroupKey]cadenceStat {
	byKey := map[inspect.GroupKey][]time.Time{}
	for _, r := range recs {
		if r.Op != opRead {
			continue
		}
		reg, owner, ok := inspect.Key(r)
		if !ok {
			continue
		}
		gk := inspect.GroupKey{Owner: owner, Reg: reg}
		byKey[gk] = append(byKey[gk], r.TS)
	}

	out := map[inspect.GroupKey]cadenceStat{}
	for gk, tss := range byKey {
		if len(tss) < 2 {
			continue
		}
		sort.Slice(tss, func(i, j int) bool { return tss[i].Before(tss[j]) })
		var total time.Duration
		for i := 1; i < len(tss); i++ {
			total += tss[i].Sub(tss[i-1])
		}
		out[gk] = cadenceStat{avg: total / time.Duration(len(tss)-1), n: len(tss)}
	}
	return out
}

// runTimeline implements `businspect timeline`.
func runTimeline(args []string) {
	fs := flag.NewFlagSet("timeline", flag.ExitOnError)
	in := fs.String("in", "-", "input file (default stdin)")
	reg := fs.String("reg", "", "register hex, e.g. 000306 (required)")
	ownerStr := fs.String("owner", "", "owner device 4-hex; if omitted, all owners answering -reg are shown")
	fs.Parse(args)

	if *reg == "" {
		fmt.Fprintln(os.Stderr, "businspect: timeline requires -reg")
		os.Exit(1)
	}
	recs := loadRecs(*in)

	var owners []uint16
	if *ownerStr != "" {
		o, err := parseHex16(*ownerStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "businspect: bad -owner %q: %v\n", *ownerStr, err)
			os.Exit(1)
		}
		owners = []uint16{o}
	} else {
		seen := map[uint16]bool{}
		for _, r := range recs {
			rg, ow, ok := inspect.Key(r)
			if ok && rg == *reg && !seen[ow] {
				seen[ow] = true
				owners = append(owners, ow)
			}
		}
		sort.Slice(owners, func(i, j int) bool { return owners[i] < owners[j] })
	}

	if len(owners) == 0 {
		fmt.Printf("(no owner found for register %s)\n", *reg)
		return
	}

	multi := len(owners) > 1
	for _, owner := range owners {
		for _, c := range inspect.Changes(recs, owner, *reg) {
			if multi {
				fmt.Printf("%04x  %s  %s  changed:%v\n", owner, c.TS.Format(time.RFC3339Nano), hex.EncodeToString(c.Payload), c.ChangedFrom)
			} else {
				fmt.Printf("%s  %s  changed:%v\n", c.TS.Format(time.RFC3339Nano), hex.EncodeToString(c.Payload), c.ChangedFrom)
			}
		}
	}
}

// runDiff implements `businspect diff`.
func runDiff(args []string) {
	fs := flag.NewFlagSet("diff", flag.ExitOnError)
	in := fs.String("in", "-", "input file (default stdin)")
	atStr := fs.String("at", "", "instant, RFC3339 (required)")
	window := fs.Duration("window", 2*time.Minute, "window around -at to look for before/after samples")
	fs.Parse(args)

	if *atStr == "" {
		fmt.Fprintln(os.Stderr, "businspect: diff requires -at")
		os.Exit(1)
	}
	at, err := time.Parse(time.RFC3339, *atStr)
	if err != nil {
		at, err = time.Parse(time.RFC3339Nano, *atStr)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "businspect: bad -at %q: %v\n", *atStr, err)
		os.Exit(1)
	}

	recs := loadRecs(*in)
	diffs := inspect.DiffAt(recs, at, *window)
	if len(diffs) == 0 {
		fmt.Println("(no register changed within the window)")
		return
	}
	for _, d := range diffs {
		fmt.Printf("%04x %-8s  changed:%v\n", d.Owner, d.Reg, d.Changed)
		fmt.Printf("  - %s\n", hex.EncodeToString(d.Before))
		fmt.Printf("  + %s\n", hex.EncodeToString(d.After))
	}
}

// runAlarms implements `businspect alarms`.
func runAlarms(args []string) {
	fs := flag.NewFlagSet("alarms", flag.ExitOnError)
	in := fs.String("in", "-", "input file (default stdin)")
	fs.Parse(args)

	recs := loadRecs(*in)
	any := false

	var alarmFrames []inspect.Rec
	for _, r := range recs {
		if r.Op == opAlarm || r.Op == opNack {
			alarmFrames = append(alarmFrames, r)
		}
	}
	if len(alarmFrames) > 0 {
		any = true
		fmt.Println("ALARM/NACK frames:")
		for _, r := range alarmFrames {
			opName := "ALARM"
			if r.Op == opNack {
				opName = "NACK"
			}
			fmt.Printf("  %s  %04x->%04x  %-5s  %s\n", r.TS.Format(time.RFC3339Nano), r.Src, r.Dst, opName, hex.EncodeToString(r.Data))
		}
	}

	groups := inspect.Group(recs)
	var faultOwners []uint16
	for gk := range groups {
		if gk.Reg == "004202" {
			faultOwners = append(faultOwners, gk.Owner)
		}
	}
	sort.Slice(faultOwners, func(i, j int) bool { return faultOwners[i] < faultOwners[j] })

	if len(faultOwners) > 0 {
		any = true
		if len(alarmFrames) > 0 {
			fmt.Println()
		}
		fmt.Println("Fault history, reg 004202 LASTTEN (unverified decode):")
		for _, owner := range faultOwners {
			st := groups[inspect.GroupKey{Owner: owner, Reg: "004202"}]
			for _, payload := range st.Distinct {
				fmt.Printf("  owner %04x, payload %s:\n", owner, hex.EncodeToString(payload))
				entries := decodeFaultHistory(payload)
				if len(entries) == 0 {
					fmt.Println("    (no populated entries)")
					continue
				}
				for _, e := range entries {
					fmt.Printf("    %s\n", e)
				}
			}
		}
	}

	if !any {
		fmt.Println("(no ALARM/NACK frames and no fault-history data in this capture)")
	}
}

// faultEpoch is the nonstandard epoch used by the 4202 fault-history table's
// days-since field (docs/protocol-tables.md §Faults).
var faultEpoch = time.Date(2013, 1, 1, 0, 0, 0, 0, time.UTC)

// decodeFaultHistory best-effort decodes as many 7-byte LASTTEN entries as
// fit in payload, per docs/protocol-tables.md §Faults (UNVERIFIED layout):
// code, source bus address, hour, minute, days-since-2013-01-01 (u16 BE),
// and a status byte whose bit 7 is inverted (0 = active, 1 = cleared) with
// the low 7 bits an occurrence count. All-zero entries (empty slots) are
// skipped.
func decodeFaultHistory(payload []byte) []string {
	var out []string
	for off := 0; off+7 <= len(payload); off += 7 {
		e := payload[off : off+7]
		code, srcAddr, hour, minute := e[0], e[1], e[2], e[3]
		days := binary.BigEndian.Uint16(e[4:6])
		status := e[6]
		if code == 0 && srcAddr == 0 && days == 0 {
			continue
		}
		state := "active"
		if status&0x80 != 0 {
			state = "cleared"
		}
		count := status & 0x7f
		date := faultEpoch.AddDate(0, 0, int(days))
		out = append(out, fmt.Sprintf("code=%d src=0x%02x %02d:%02d %s (%s, count=%d)",
			code, srcAddr, hour, minute, date.Format("2006-01-02"), state, count))
	}
	return out
}
