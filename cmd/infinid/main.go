package main

import (
	"flag"
	"log"
	"os"
	"time"

	"github.com/utkjmitch/infinid/bus"
	"github.com/utkjmitch/infinid/capture"
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
	flag.Parse()
	if *serialDev == "" {
		log.Fatal("-serial is required")
	}

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

	for {
		if err := run(*serialDev, rec, *verbose); err != nil {
			log.Printf("bus error: %v — reopening in 5s", err)
			time.Sleep(5 * time.Second)
		}
	}
}

func run(device string, rec *capture.Recorder, verbose bool) error {
	port, err := bus.OpenSerial(device)
	if err != nil {
		return err
	}
	defer port.Close()
	log.Printf("listening on %s", device)

	d := bus.NewDecoder(port)
	frames := 0
	lastStats := time.Now()
	for {
		f, err := d.Next()
		if err != nil {
			return err
		}
		frames++
		if verbose {
			log.Printf("frame: %s", f)
		} else if time.Since(lastStats) >= time.Minute {
			log.Printf("stats: %d frames this interval, %d resync bytes total", frames, d.Resyncs())
			frames = 0
			lastStats = time.Now()
		}
		rec.Add(capture.Record{
			TS: time.Now(), Src: f.Src, Dst: f.Dst, Op: f.Op, Data: f.Data, Raw: f.Raw,
		})
	}
}
