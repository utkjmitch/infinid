// Package rest serves the read-only debug surface: daemon status, assembled
// state, the raw-frame ring, and the event journal (the technician export).
// No auth — bind localhost or a container network, never the open LAN.
package rest

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/utkjmitch/infinid/capture"
	"github.com/utkjmitch/infinid/state"
)

// Handler builds the debug mux. statusFn supplies daemon counters (frames,
// CRC failures, unknown frames — owned by main's read loop).
func Handler(st *state.State, rec *capture.Recorder, journalPath string,
	statusFn func() map[string]any) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, statusFn())
	})

	mux.HandleFunc("GET /state", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, st.Snapshot(time.Now()))
	})

	mux.HandleFunc("GET /frames", func(w http.ResponseWriter, r *http.Request) {
		type frame struct {
			TS   time.Time `json:"ts"`
			Src  string    `json:"src"`
			Dst  string    `json:"dst"`
			Op   string    `json:"op"`
			Data string    `json:"data"`
		}
		out := []frame{} // not nil: an idle ring must render as [], not null
		for _, r := range rec.Snapshot() {
			out = append(out, frame{TS: r.TS,
				Src: fmt.Sprintf("%04x", r.Src), Dst: fmt.Sprintf("%04x", r.Dst),
				Op: fmt.Sprintf("%02x", r.Op), Data: hex.EncodeToString(r.Data)})
		}
		writeJSON(w, out)
	})

	mux.HandleFunc("GET /events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		f, err := os.Open(journalPath)
		if err != nil {
			http.Error(w, "no journal", http.StatusNotFound)
			return
		}
		defer f.Close()
		io.Copy(w, f)
	})

	return mux
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// Too late for a status code; a marshal failure would otherwise be
		// a silent empty 200 (statusFn is caller-owned map[string]any).
		log.Printf("rest: encode: %v", err)
	}
}
