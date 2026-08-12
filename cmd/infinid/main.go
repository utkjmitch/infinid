// cmd/infinid/main.go
package main

import (
	"flag"
	"log"
	"os"
	"time"

	"github.com/utkjmitch/infinid/bus"
	"github.com/utkjmitch/infinid/capture"
)

func main() {
	serialDev := flag.String("serial", "", "RS-485 serial device (required)")
	capturePath := flag.String("capture", "", "append frames as JSONL to this file (optional)")
	ringSize := flag.Int("ring", 4096, "frames kept in the in-memory ring")
	flag.Parse()
	if *serialDev == "" {
		log.Fatal("-serial is required")
	}

	var w *os.File
	if *capturePath != "" {
		var err error
		w, err = os.OpenFile(*capturePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			log.Fatalf("open capture file: %v", err)
		}
		defer w.Close()
	}
	var rec *capture.Recorder
	if w != nil {
		rec = capture.New(*ringSize, w)
	} else {
		rec = capture.New(*ringSize, nil)
	}

	for {
		if err := run(*serialDev, rec); err != nil {
			log.Printf("bus error: %v — reopening in 5s", err)
			time.Sleep(5 * time.Second)
		}
	}
}

func run(device string, rec *capture.Recorder) error {
	port, err := bus.OpenSerial(device)
	if err != nil {
		return err
	}
	defer port.Close()
	log.Printf("listening on %s", device)

	d := bus.NewDecoder(port)
	for {
		f, err := d.Next()
		if err != nil {
			return err
		}
		log.Printf("frame: %s", f)
		rec.Add(capture.Record{
			TS: time.Now(), Src: f.Src, Dst: f.Dst, Op: f.Op, Data: f.Data,
		})
	}
}
