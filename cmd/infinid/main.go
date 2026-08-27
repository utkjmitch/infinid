package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/utkjmitch/infinid/bus"
	"github.com/utkjmitch/infinid/capture"
	"github.com/utkjmitch/infinid/mqtt"
	"github.com/utkjmitch/infinid/protocol"
	"github.com/utkjmitch/infinid/rest"
	"github.com/utkjmitch/infinid/sam"
	"github.com/utkjmitch/infinid/state"
)

// cappedWriter appends to f until the byte budget is exhausted or a write
// fails, then latches off. It logs once on latch and never surfaces errors
// upstream — losing capture must not take down the bus reader, but it also
// must not fill the HAOS data partition or fail silently forever.
type cappedWriter struct {
	f       *os.File
	remain  int64
	stopped bool
}

func (c *cappedWriter) Write(p []byte) (int, error) {
	if c.stopped {
		return len(p), nil
	}
	if int64(len(p)) > c.remain {
		c.stopped = true
		log.Printf("capture: size cap reached, capture stopped (bus reading continues)")
		return len(p), nil
	}
	n, err := c.f.Write(p)
	if err != nil {
		c.stopped = true
		log.Printf("capture: write failed, capture stopped (bus reading continues): %v", err)
		return len(p), nil
	}
	c.remain -= int64(n)
	return len(p), nil
}

func main() {
	serialDev := flag.String("serial", "", "RS-485 serial device (required)")
	capturePath := flag.String("capture", "", "append frames as JSONL to this file (optional)")
	captureMaxMB := flag.Int64("capture-max-mb", 1024, "stop capturing once the file reaches this size")
	ringSize := flag.Int("ring", 4096, "frames kept in the in-memory ring")
	verbose := flag.Bool("verbose", false, "log every frame (default: one stats line per minute)")
	mqttBroker := flag.String("mqtt-broker", "", "MQTT broker URL, e.g. tcp://broker:1883 (empty = MQTT off)")
	mqttUser := flag.String("mqtt-user", "", "MQTT username")
	mqttPassEnv := flag.String("mqtt-pass-env", "INFINID_MQTT_PASS", "env var holding the MQTT password (never a flag — flags leak into process lists)")
	discoveryPrefix := flag.String("mqtt-discovery-prefix", "homeassistant", "HA discovery prefix")
	baseTopic := flag.String("mqtt-base-topic", "infinid", "state topic base")
	zoneNamesFlag := flag.String("zone-names", "", "comma-separated zone names by index, e.g. bedrooms,living_room,basement")
	samEnabled := flag.Bool("sam", false, "enable active SAM reads (default: passive-only)")
	journalPath := flag.String("journal", "", "event journal JSONL path (empty = journal off)")
	restAddr := flag.String("rest", "127.0.0.1:8099", "REST debug listen address (empty = REST off)")
	flag.Parse()
	if *serialDev == "" {
		log.Fatal("-serial is required")
	}

	zoneNames := parseZoneNames(*zoneNamesFlag)

	var w *cappedWriter
	if *capturePath != "" {
		f, err := os.OpenFile(*capturePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			log.Fatalf("open capture file: %v", err)
		}
		remain := *captureMaxMB << 20
		if st, err := f.Stat(); err == nil {
			remain -= st.Size()
		}
		w = &cappedWriter{f: f, remain: remain}
		if remain <= 0 {
			w.stopped = true
			log.Printf("capture: file already at size cap, capture disabled")
		}
	}
	var rec *capture.Recorder
	if w != nil {
		rec = capture.New(*ringSize, w)
	} else {
		rec = capture.New(*ringSize, nil)
	}

	st := state.New()

	var journal *state.Journal
	if *journalPath != "" {
		var err error
		journal, err = state.OpenJournal(*journalPath)
		if err != nil {
			log.Fatalf("open journal: %v", err)
		}
	}

	var exporter *mqtt.Exporter
	var pub *mqtt.PahoPublisher
	if *mqttBroker != "" {
		avail := *baseTopic + "/availability"
		var err error
		pub, err = mqtt.Connect(*mqttBroker, *mqttUser, os.Getenv(*mqttPassEnv), "infinid", avail)
		if err != nil {
			log.Fatalf("mqtt connect: %v", err)
		}
		exporter = mqtt.New(pub, mqtt.Config{
			BaseTopic: *baseTopic, DiscoveryPrefix: *discoveryPrefix,
			ZoneNames: zoneNames, Version: version})
		// No startup PublishAvailability(true) here (review item 5): the
		// publish loop below owns the availability topic exclusively and
		// publishes the real bus-online value within its first second —
		// an eager "online" here would just flap to "offline" a moment
		// later, before any frame has actually been decoded.
	}

	d := &daemon{
		rec: rec, st: st, journal: journal, exporter: exporter, pub: pub,
		samEnabled: *samEnabled, verbose: *verbose,
	}

	if *restAddr != "" {
		go func() {
			h := rest.Handler(st, rec, *journalPath, d.statusMap)
			// Amendment A5: an http.Server with ReadHeaderTimeout guards this
			// unauthenticated debug port against slowloris-style header
			// stalls; the bare http.ListenAndServe helper has no timeout.
			srv := &http.Server{Addr: *restAddr, Handler: h, ReadHeaderTimeout: 5 * time.Second}
			log.Printf("rest: listening on %s", *restAddr)
			if err := srv.ListenAndServe(); err != nil {
				log.Printf("rest: %v", err)
			}
		}()
	}

	go d.publishLoop()

	for {
		if err := d.run(*serialDev); err != nil {
			log.Printf("bus error: %v — reopening in 5s", err)
			time.Sleep(5 * time.Second)
		}
	}
}

const version = "0.2.0"

// parseZoneNames validates entity-id-safe slugs: lowercase a-z, 0-9, _.
func parseZoneNames(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	for _, p := range parts {
		for _, c := range p {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
				log.Fatalf("zone name %q: must be lowercase letters, digits, underscores", p)
			}
		}
		if p == "" {
			log.Fatal("empty zone name in -zone-names")
		}
	}
	return parts
}

// daemon holds the wired pipeline shared between the read loop and the
// publish loop.
type daemon struct {
	rec        *capture.Recorder
	st         *state.State
	journal    *state.Journal
	exporter   *mqtt.Exporter
	pub        *mqtt.PahoPublisher // amendment A2: kept so publishLoop can poll Reconnected()
	samEnabled bool
	verbose    bool

	mu            sync.Mutex
	frames        uint64
	framesWindow  uint64
	unknownFrames uint64
	crcResyncs    uint64
	lastFrame     time.Time
	samFailures   int
	classified    bool
	counters      map[string]float64

	// schedMu guards every call into the per-run sam.Scheduler (NoteFrame,
	// Tick, Failures). sam.Scheduler documents itself as not safe for
	// concurrent use, and amendment A1 adds a ticker goroutine that drives
	// Tick independently of the frame-read loop, so both call sites must
	// serialize through this lock (amendment A1).
	schedMu sync.Mutex

	// journalErrLogged latches so a degraded journal (state.Journal.Err) is
	// logged once instead of once per second (amendment A3). Touched only
	// from publishLoop, which runs on a single goroutine, so it needs no
	// lock of its own.
	journalErrLogged bool
}

func (d *daemon) statusMap() map[string]any {
	d.mu.Lock()
	defer d.mu.Unlock()
	return map[string]any{
		"frames":         d.frames,
		"unknown_frames": d.unknownFrames,
		"resync_bytes":   d.crcResyncs,
		"last_frame":     d.lastFrame,
		"sam_failures":   d.samFailures,
		"version":        version,
	}
}

func (d *daemon) run(device string) error {
	port, err := bus.OpenSerial(device)
	if err != nil {
		return err
	}
	defer port.Close()
	log.Printf("listening on %s (sam=%v)", device, d.samEnabled)

	var sched *sam.Scheduler
	// lastSchedFailures is the scheduler's own cumulative Failures() count as
	// of the last time either goroutine below observed it. sched is rebuilt
	// fresh on every reconnect and so restarts its own counter at 0; review
	// item 3 turns d.samFailures into a daemon-level running total across
	// reconnects by accumulating (Failures()-lastSchedFailures) deltas
	// instead of overwriting. Every read/write of lastSchedFailures happens
	// while schedMu is held (both sites below), so the plain int needs no
	// atomic/lock of its own beyond that.
	var lastSchedFailures int
	if d.samEnabled {
		sched = sam.New(port, []sam.Target{
			{Reg: [3]byte{0x00, 0x3b, 0x02}, Interval: 10 * time.Second},
			{Reg: [3]byte{0x00, 0x3b, 0x03}, Interval: 10 * time.Second},
			{Reg: [3]byte{0x00, 0x3b, 0x05}, Interval: time.Hour},
			{Reg: [3]byte{0x00, 0x42, 0x02}, Interval: time.Hour},
		})

		// Amendment A1: the plan only calls sched.Tick after a frame
		// arrives, so on a silent bus (dec.Next blocking forever) a pending
		// request never times out and the scheduler wedges. This ticker
		// drives Tick once a second regardless of bus traffic; schedMu
		// serializes it against the frame-driven NoteFrame/Tick calls below,
		// since Scheduler is documented not safe for concurrent use.
		//
		// Review item 1: closing stop only signals the goroutine to exit —
		// it does not wait for it. Without a join, a ticker fire that is
		// already inside (or about to enter) the schedMu critical section
		// when run() returns can call sched.Tick → port.Write on a port that
		// the deferred port.Close() (registered above, so it runs after this
		// defer in LIFO order) has already closed; the serial library's
		// Write has no closed-check the way Read does, so worst case that
		// write lands on a since-recycled fd. done is closed by the
		// goroutine right before it returns, and the defer below blocks on
		// it, so the goroutine is guaranteed to have exited before
		// port.Close() runs.
		stop, done := make(chan struct{}), make(chan struct{})
		defer func() {
			close(stop)
			<-done
		}()
		go func() {
			defer close(done)
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-stop:
					return
				case now := <-ticker.C:
					// Review item 2: fails/delta are computed and folded
					// into d.samFailures while schedMu is still held (lock
					// order schedMu→d.mu, matching the frame-driven site
					// below), so the read-modify-write can't interleave
					// with the other goroutine's and lose an update.
					d.schedMu.Lock()
					sched.Tick(now)
					fails := sched.Failures()
					delta := fails - lastSchedFailures
					lastSchedFailures = fails
					d.mu.Lock()
					d.samFailures += delta
					d.mu.Unlock()
					d.schedMu.Unlock()
				}
			}
		}()
	}

	dec := bus.NewDecoder(port)
	// lastResyncs mirrors lastSchedFailures for dec.Resyncs(): dec is also
	// rebuilt fresh on every reconnect, so d.crcResyncs accumulates deltas
	// across reconnects the same way (review item 3). Only the frame loop
	// below touches dec or lastResyncs, so it needs no lock of its own.
	var lastResyncs uint64
	lastStats := time.Now()
	for {
		f, err := dec.Next()
		if err != nil {
			return err
		}
		now := time.Now()
		if d.verbose {
			log.Printf("frame: %s", f)
		}
		d.rec.Add(capture.Record{TS: now, Src: f.Src, Dst: f.Dst, Op: f.Op, Data: f.Data, Raw: f.Raw})

		readings, ok := protocol.Decode(f, now)
		d.mu.Lock()
		d.frames++
		d.framesWindow++
		d.lastFrame = now
		// Running total across reconnects (review item 3): dec restarts its
		// own Resyncs() counter at 0 on every run(), so fold in the delta
		// since this run's last observation instead of overwriting.
		curResyncs := uint64(dec.Resyncs())
		d.crcResyncs += curResyncs - lastResyncs
		lastResyncs = curResyncs
		if !ok {
			d.unknownFrames++
		}
		d.mu.Unlock()

		for _, r := range readings {
			d.st.Apply(r)
			d.trackCounter(r, now)
		}
		// protocol.DecodeFaults takes a *time.Location to assemble fault
		// timestamps in (the plan text predates this parameter); time.Local
		// matches DecodeFaults' own documented default for a nil loc.
		if faults, fok := protocol.DecodeFaults(f, time.Local); fok && d.journal != nil {
			d.journal.NoteFaults(faults, now)
		}
		if d.journal != nil {
			d.journal.NoteDevice(f.Src, now)
		}
		if sched != nil {
			// Review items 2+3: same schedMu→d.mu nested order and
			// delta-accumulation as the ticker goroutine above, so the two
			// call sites can't race each other into a lost or regressed
			// sam_failures update.
			d.schedMu.Lock()
			sched.NoteFrame(f, now)
			sched.Tick(now) // between frames = the natural inter-frame gap
			fails := sched.Failures()
			delta := fails - lastSchedFailures
			lastSchedFailures = fails
			d.mu.Lock()
			d.samFailures += delta
			d.mu.Unlock()
			d.schedMu.Unlock()
		}

		if !d.verbose && time.Since(lastStats) >= time.Minute {
			// Review item 6: snapshot under the lock, log after releasing it
			// — log.Printf (I/O) has no business running while d.mu is held.
			d.mu.Lock()
			windowFrames := d.framesWindow
			totalUnknown := d.unknownFrames
			totalResyncs := d.crcResyncs
			d.framesWindow = 0
			d.mu.Unlock()
			log.Printf("stats: %d frames this interval, %d unknown total, %d resync bytes",
				windowFrames, totalUnknown, totalResyncs)
			lastStats = time.Now()
		}
	}
}

// trackCounter feeds power-on counters to the journal: reference values for
// outage classification, and the one-shot gap classification at startup.
func (d *daemon) trackCounter(r protocol.Reading, now time.Time) {
	if d.journal == nil {
		return
	}
	if r.Field != "idu_power_cycles" && r.Field != "odu_power_cycles" {
		return
	}
	d.mu.Lock()
	if d.counters == nil {
		d.counters = map[string]float64{}
	}
	d.counters[r.Field] = r.Value
	both := len(d.counters) == 2
	classified := d.classified
	vals := map[string]float64{}
	for k, v := range d.counters {
		vals[k] = v
	}
	if both && !classified {
		d.classified = true
	}
	d.mu.Unlock()
	if both && !classified {
		d.journal.ClassifyGap(vals, now)
	}
	if both {
		d.journal.NotePowerCounters(vals, now)
	}
}

// publishLoop pushes snapshots to MQTT once a second and maintains
// availability from bus liveness.
func (d *daemon) publishLoop() {
	if d.exporter == nil && d.journal == nil {
		return
	}
	var lastFrames uint64
	var lastCount time.Time
	fpm := 0.0
	for range time.Tick(time.Second) {
		now := time.Now()
		d.mu.Lock()
		frames := d.frames
		last := d.lastFrame
		unknown := d.unknownFrames
		samFail := d.samFailures
		d.mu.Unlock()

		if d.journal != nil {
			d.journal.CheckLiveness(now, nil)
			// Amendment A3: state.Journal never logs its own write errors
			// (see Journal.Err's doc comment) — surface degradation here,
			// once, so a full disk or permissions failure isn't silent.
			if err := d.journal.Err(); err != nil && !d.journalErrLogged {
				d.journalErrLogged = true
				log.Printf("journal degraded: %v", err)
			}
		}
		if d.exporter == nil {
			continue
		}

		// Amendment A2: re-assert discovery/state after an MQTT reconnect —
		// a broker restart or a new session may have dropped every retained
		// message the previous session believed was still there. The
		// OnConnect handler backing Reconnected also fires on the very
		// first connect, so a redundant Reassert here before anything has
		// been published yet is expected and harmless.
		if d.pub != nil && d.pub.Reconnected() {
			d.exporter.Reassert()
		}

		if lastCount.IsZero() || now.Sub(lastCount) >= time.Minute {
			if !lastCount.IsZero() {
				fpm = float64(frames-lastFrames) / now.Sub(lastCount).Minutes()
			}
			lastFrames, lastCount = frames, now
		}

		snap := d.st.Snapshot(now)
		zones := make([]int, 0, len(snap.Zones))
		for z := range snap.Zones {
			zones = append(zones, z)
		}
		d.exporter.PublishDiscovery(zones)
		d.exporter.PublishState(snap, now)

		// Amendment A6: "bus online" here is deliberately its own
		// MQTT-facing definition (last decoded frame < 60s ago), kept
		// separate from journal.BusSilent — the journal tracks silence on
		// its own horizon/hysteresis for outage classification, which is a
		// different concern from what HA should show as available right now.
		busOnline := !last.IsZero() && now.Sub(last) < 60*time.Second
		// Error ignored deliberately (amendment A4): this loop republishes
		// availability every second, so a transient publish failure
		// self-heals on the next cycle.
		_ = d.exporter.PublishAvailability(busOnline)

		h := mqtt.Health{FramesPerMin: fpm, UnknownFrames: float64(unknown),
			SAMFailures: float64(samFail), BusOnline: busOnline}
		if d.journal != nil {
			active := d.journal.ActiveFaults()
			h.FaultActive = len(active) > 0
			h.FaultCount = float64(len(active))
			if len(active) > 0 {
				f := active[0]
				h.LastFault = fmt.Sprintf("%d @ %s (x%d)", f.Code,
					f.Time.Format("2006-01-02 15:04"), f.Count)
			}
		}
		d.exporter.PublishHealth(h, now)
	}
}
