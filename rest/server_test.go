package rest

import (
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/utkjmitch/infinid/capture"
	"github.com/utkjmitch/infinid/protocol"
	"github.com/utkjmitch/infinid/state"
)

func TestEndpoints(t *testing.T) {
	st := state.New()
	st.Apply(protocol.Reading{Owner: 0x2001, Zone: 1, Field: "temp",
		Value: 71.5, TS: time.Now()})
	rec := capture.New(8, nil)
	rec.Add(capture.Record{TS: time.Now(), Src: 0x5201, Dst: 0x2001, Op: 0x06,
		Data: []byte{0x00, 0x03, 0x03, 0x01}})
	journal := filepath.Join(t.TempDir(), "events.jsonl")
	os.WriteFile(journal, []byte(`{"ts":"2026-08-13T12:00:00Z","type":"daemon_start"}`+"\n"), 0o644)

	stats := func() map[string]any { return map[string]any{"frames": 42} }
	srv := httptest.NewServer(Handler(st, rec, journal, stats))
	defer srv.Close()

	for path, wantIn := range map[string]string{
		"/status": "frames",
		"/state":  "temp", // an applied reading, not just the struct tag
		"/frames": "5201",
		"/events": "daemon_start",
	} {
		resp, err := srv.Client().Get(srv.URL + path)
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("%s: %v %v", path, err, resp)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatalf("%s read: %v", path, err)
		}
		if !strings.Contains(string(body), wantIn) {
			t.Errorf("%s missing %q: %s", path, wantIn, body)
		}
	}
}

func TestEmptyFramesIsArray(t *testing.T) {
	srv := httptest.NewServer(Handler(state.New(), capture.New(8, nil), "", nil))
	defer srv.Close()
	resp, err := srv.Client().Get(srv.URL + "/frames")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if got := strings.TrimSpace(string(body)); got != "[]" {
		t.Errorf("empty ring = %q, want [] (null breaks JS consumers)", got)
	}
}

func TestMissingJournal404(t *testing.T) {
	srv := httptest.NewServer(Handler(state.New(), capture.New(8, nil), "", nil))
	defer srv.Close()
	resp, err := srv.Client().Get(srv.URL + "/events")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("missing journal status = %d, want 404", resp.StatusCode)
	}
}

func TestMethodFiltering(t *testing.T) {
	// Pins the go>=1.22 method-prefixed mux patterns this package relies on.
	srv := httptest.NewServer(Handler(state.New(), capture.New(8, nil), "", nil))
	defer srv.Close()
	resp, err := srv.Client().Post(srv.URL+"/status", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 405 {
		t.Errorf("POST /status status = %d, want 405", resp.StatusCode)
	}
}
