package main

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func tileServer(t *testing.T, upstream string, logBuf *bytes.Buffer) (*httptest.Server, string) {
	t.Helper()
	dir := t.TempDir()
	s := &server{tiles: newTileProxy(dir, upstream+"/{z}/{x}/{y}.png"), cache: newPlanCache(1, time.Hour),
		log: slog.New(slog.NewJSONHandler(logBuf, nil))}
	return httptest.NewServer(s.routes()), dir
}

func get(t *testing.T, url string) (int, []byte) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func TestTileProxyCaches(t *testing.T) {
	var calls atomic.Int32
	var ua atomic.Value
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		ua.Store(r.Header.Get("User-Agent"))
		time.Sleep(20 * time.Millisecond)
		_, _ = w.Write([]byte("PNG:" + r.URL.Path))
	}))
	defer up.Close()
	var logs bytes.Buffer
	api, dir := tileServer(t, up.URL, &logs)
	defer api.Close()

	// Five concurrent requests for one tile: one upstream fetch.
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if code, body := get(t, api.URL+"/v1/tiles/12/701/1635.png"); code != 200 || string(body) != "PNG:/12/701/1635.png" {
				t.Errorf("got %d %q", code, body)
			}
		}()
	}
	wg.Wait()
	if n := calls.Load(); n != 1 {
		t.Errorf("concurrent requests made %d upstream fetches, want 1", n)
	}
	if code, _ := get(t, api.URL+"/v1/tiles/12/701/1635.png"); code != 200 || calls.Load() != 1 {
		t.Errorf("cached tile refetched (calls=%d)", calls.Load())
	}
	if !strings.Contains(ua.Load().(string), "serpentine") {
		t.Errorf("upstream must see an identifying User-Agent, got %q", ua.Load())
	}
	if _, err := os.Stat(filepath.Join(dir, "12", "701", "1635.png")); err != nil {
		t.Errorf("tile not on disk: %v", err)
	}
	for _, bad := range []string{"/v1/tiles/18/0/0.png", "/v1/tiles/2/4/0.png", "/v1/tiles/2/0/-1.png", "/v1/tiles/a/0/0.png", "/v1/tiles/2/0/0.jpg", "/v1/tiles/2/0/..%2f..%2fetc.png"} {
		if code, _ := get(t, api.URL+bad); code != 404 {
			t.Errorf("%s: got %d, want 404", bad, code)
		}
	}
	if strings.Contains(logs.String(), "701") || strings.Contains(logs.String(), "1635") {
		t.Error("tile coordinates must not appear in the log")
	}
}

func TestTileProxyServesStaleWhenUpstreamFails(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer up.Close()
	var logs bytes.Buffer
	api, dir := tileServer(t, up.URL, &logs)
	defer api.Close()
	if code, _ := get(t, api.URL+"/v1/tiles/3/1/2.png"); code != 502 {
		t.Errorf("uncached + upstream down: got %d, want 502", code)
	}
	p := filepath.Join(dir, "3", "1", "3.png")
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	_ = os.WriteFile(p, []byte("old"), 0o644)
	old := time.Now().Add(-2 * tileMaxAge)
	_ = os.Chtimes(p, old, old)
	if code, body := get(t, api.URL+"/v1/tiles/3/1/3.png"); code != 200 || string(body) != "old" {
		t.Errorf("stale tile should be served when upstream fails: %d %q", code, body)
	}
}

func TestTilesDisabledAndStatic(t *testing.T) {
	var calls atomic.Int32
	gh := fakeGH(t, nil, &calls)
	defer gh.Close()
	api := testServer(gh) // no tile cache configured
	defer api.Close()
	if code, _ := get(t, api.URL+"/v1/tiles/1/0/0.png"); code != 404 {
		t.Errorf("tiles disabled: got %d, want 404", code)
	}
	code, body := get(t, api.URL+"/v1/static/leaflet-1.9.4/leaflet.js")
	if code != 200 || !bytes.Contains(body, []byte("Leaflet 1.9.4")) {
		t.Errorf("vendored leaflet.js: %d", code)
	}
	if code, _ := get(t, api.URL+"/v1/static/leaflet-1.9.4/leaflet.css"); code != 200 {
		t.Errorf("leaflet.css: %d", code)
	}
}
