package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// upstreamCall is what the fake tile server saw of one request.
type upstreamCall struct {
	path   string // escaped, as it arrived
	header http.Header
}

type fakeTiles struct {
	*httptest.Server
	mu    sync.Mutex
	calls []upstreamCall
}

func (f *fakeTiles) seen() []upstreamCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]upstreamCall(nil), f.calls...)
}

func gz(s string) []byte {
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	_, _ = w.Write([]byte(s))
	_ = w.Close()
	return b.Bytes()
}

// newFakeTiles behaves like `pmtiles serve` behind Caddy: gzipped tiles with an ETag, 304 on a
// matching If-None-Match, 204 for a tile that is not in the archive, plain files for the rest.
func newFakeTiles(t *testing.T) *fakeTiles {
	t.Helper()
	f := &fakeTiles{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.calls = append(f.calls, upstreamCall{path: r.URL.EscapedPath(), header: r.Header.Clone()})
		f.mu.Unlock()
		p := r.URL.Path
		switch {
		case p == "/planet/2/0/0.mvt":
			w.WriteHeader(http.StatusNoContent)
		case strings.HasPrefix(p, "/planet/") && strings.HasSuffix(p, ".mvt"):
			if r.Header.Get("If-None-Match") == `"abc"` {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("Content-Type", "application/x-protobuf")
			w.Header().Set("Content-Encoding", "gzip")
			w.Header().Set("ETag", `"abc"`)
			w.Header().Set("Cache-Control", "public, max-age=86400")
			w.Header().Set("Access-Control-Allow-Origin", "*") // must not be relayed
			_, _ = w.Write(gz("MVT:" + p))
		case strings.HasPrefix(p, "/styles/"), strings.HasPrefix(p, "/fonts/"), strings.HasPrefix(p, "/sprites/"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("FILE:" + p))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func tileAPI(t *testing.T, base string, logBuf *bytes.Buffer) (*httptest.Server, *metrics) {
	t.Helper()
	m := newMetrics()
	s := &server{tiles: newTileProxy(base), cache: newPlanCache(1, time.Hour), stats: m,
		log: slog.New(slog.NewJSONHandler(logBuf, nil))}
	s.tiles.stats = m
	api := httptest.NewServer(s.routes())
	t.Cleanup(api.Close)
	return api, m
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

func TestTileRelayPassesTilesThroughUntouched(t *testing.T) {
	up := newFakeTiles(t)
	var logs bytes.Buffer
	api, m := tileAPI(t, up.URL+"/", &logs) // trailing slash on the base must not double up

	// Ask as a browser would, carrying everything a viewer's request can carry.
	req, _ := http.NewRequest(http.MethodGet, api.URL+"/v1/tiles/12/701/1635.mvt", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("Origin", "https://serpentine.phfactor.net")
	req.Header.Set("Referer", "https://serpentine.phfactor.net/v1/")
	req.Header.Set("Cookie", "who=rider")
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	resp, err := http.DefaultTransport.RoundTrip(req) // no transparent gunzip: see the bytes as sent
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Encoding") != "gzip" || !bytes.Equal(raw, gz("MVT:/planet/12/701/1635.mvt")) {
		t.Fatalf("tile must be relayed still gzipped, byte for byte: %d enc=%q", resp.StatusCode, resp.Header.Get("Content-Encoding"))
	}
	for h, want := range map[string]string{"Content-Type": "application/x-protobuf", "ETag": `"abc"`, "Cache-Control": "public, max-age=86400"} {
		if got := resp.Header.Get(h); got != want {
			t.Errorf("%s: got %q, want %q", h, got, want)
		}
	}
	if resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Error("the tile server's CORS header is not ours to hand out")
	}

	calls := up.seen()
	if len(calls) != 1 || calls[0].path != "/planet/12/701/1635.mvt" {
		t.Fatalf("upstream saw %+v", calls)
	}
	// The promise on the privacy page: the tile server learns nothing about the viewer.
	for _, h := range []string{"Origin", "Referer", "Cookie", "X-Forwarded-For", "Forwarded", "X-Real-Ip"} {
		if v := calls[0].header.Get(h); v != "" {
			t.Errorf("viewer header %s reached the tile server: %q", h, v)
		}
	}
	if ua := calls[0].header.Get("User-Agent"); !strings.Contains(ua, "serpentine") {
		t.Errorf("upstream must see our own User-Agent, got %q", ua)
	}

	// A revalidation costs no bytes: If-None-Match goes up, 304 comes back.
	req, _ = http.NewRequest(http.MethodGet, api.URL+"/v1/tiles/12/701/1635.mvt", nil)
	req.Header.Set("If-None-Match", `"abc"`)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotModified {
		t.Errorf("If-None-Match: got %d, want 304", resp.StatusCode)
	}
	// Open ocean: the archive has no tile, and "no content" is the honest answer.
	if code, body := get(t, api.URL+"/v1/tiles/2/0/0.mvt"); code != http.StatusNoContent || len(body) != 0 {
		t.Errorf("empty tile: got %d with %d bytes, want 204", code, len(body))
	}
	if b := m.now(); b.Tiles != 3 || b.TileHits != 3 {
		t.Errorf("counters: %d tiles, %d served; want 3 and 3", b.Tiles, b.TileHits)
	}
	assertNoTileCoordinatesLogged(t, logs.String(), "12", "701", "1635")
}

func TestTileRelayServesStyleGlyphsAndSprites(t *testing.T) {
	up := newFakeTiles(t)
	var logs bytes.Buffer
	api, m := tileAPI(t, up.URL, &logs)
	for path, want := range map[string]string{
		"/v1/tiles/styles/light.json":                      "/styles/light.json",
		"/v1/tiles/styles/dark.json":                       "/styles/dark.json",
		"/v1/tiles/fonts/Noto%20Sans%20Regular/0-255.pbf":  "/fonts/Noto%20Sans%20Regular/0-255.pbf",
		"/v1/tiles/fonts/Noto%20Sans%20Medium/256-511.pbf": "/fonts/Noto%20Sans%20Medium/256-511.pbf",
		"/v1/tiles/sprites/v4/light.json":                  "/sprites/v4/light.json",
		"/v1/tiles/sprites/v4/dark@2x.png":                 "/sprites/v4/dark@2x.png",
	} {
		before := len(up.seen())
		code, body := get(t, api.URL+path)
		calls := up.seen()
		if code != 200 || len(calls) != before+1 || calls[len(calls)-1].path != want {
			t.Errorf("%s: got %d, upstream saw %v, want %s", path, code, calls[before:], want)
			continue
		}
		if !strings.HasPrefix(string(body), "FILE:") {
			t.Errorf("%s: body %q", path, body)
		}
	}
	if b := m.now(); b.Tiles != 0 {
		t.Errorf("styles, glyphs and sprites are not tiles; counted %d", b.Tiles)
	}
}

func TestTileRelayRefusesEverythingElse(t *testing.T) {
	up := newFakeTiles(t)
	var logs bytes.Buffer
	api, _ := tileAPI(t, up.URL, &logs)
	for _, bad := range []string{
		"/v1/tiles/16/0/0.mvt",                  // deeper than the archive
		"/v1/tiles/2/4/0.mvt",                   // x outside the zoom level
		"/v1/tiles/2/0/4.mvt",                   // y outside the zoom level
		"/v1/tiles/2/0/-1.mvt",                  //
		"/v1/tiles/a/0/0.mvt",                   //
		"/v1/tiles/12/701/1635.png",             // the raster endpoint this replaced
		"/v1/tiles/planet.pmtiles",              // the archive itself
		"/v1/tiles/planet.json",                 // TileJSON names the tile server's own host
		"/v1/tiles/planet/2/0/0.mvt",            //
		"/v1/tiles/styles/..%2f..%2fetc.json",   //
		"/v1/tiles/styles/Light.JSON",           //
		"/v1/tiles/fonts/..%2Fstyles/0-255.pbf", //
		"/v1/tiles/fonts/Noto/0-255.pbf/extra",  //
		"/v1/tiles/sprites/v4/light.svg",        //
		"/v1/tiles/vendor/maplibre-gl/maplibre-gl.js",
		"/v1/tiles/",
	} {
		if code, _ := get(t, api.URL+bad); code != 404 {
			t.Errorf("%s: got %d, want 404", bad, code)
		}
	}
	if calls := up.seen(); len(calls) != 0 {
		t.Errorf("refused paths must never reach the tile server; it saw %+v", calls)
	}
}

func TestTileRelayWhenTheTileServerIsDown(t *testing.T) {
	var logs bytes.Buffer
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "secret upstream detail", http.StatusInternalServerError)
	}))
	defer broken.Close()
	api, m := tileAPI(t, broken.URL, &logs)
	code, body := get(t, api.URL+"/v1/tiles/3/1/2.mvt")
	if code != http.StatusBadGateway || strings.Contains(string(body), "secret") {
		t.Errorf("upstream 500: got %d %q, want a plain 502", code, body)
	}
	if b := m.now(); b.Tiles != 1 || b.TileHits != 0 {
		t.Errorf("a failed tile counts as asked-for but not served: %d / %d", b.Tiles, b.TileHits)
	}

	gone := httptest.NewServer(http.NotFoundHandler())
	gone.Close() // nothing listening at all
	api2, _ := tileAPI(t, gone.URL, &logs)
	if code, _ := get(t, api2.URL+"/v1/tiles/3/1/2.mvt"); code != http.StatusBadGateway {
		t.Errorf("unreachable tile server: got %d, want 502", code)
	}
}

func TestTilesDisabledAndStatic(t *testing.T) {
	var calls atomic.Int32
	gh := fakeGH(t, nil, &calls)
	defer gh.Close()
	api := testServer(gh) // no tile server configured
	defer api.Close()
	if code, _ := get(t, api.URL+"/v1/tiles/1/0/0.mvt"); code != 404 {
		t.Errorf("tiles disabled: got %d, want 404", code)
	}
	if code, _ := get(t, api.URL+"/v1/tiles/styles/light.json"); code != 404 {
		t.Errorf("style with tiles disabled: got %d, want 404", code)
	}
	code, body := get(t, api.URL+"/v1/static/leaflet-1.9.4/leaflet.js")
	if code != 200 || !bytes.Contains(body, []byte("Leaflet 1.9.4")) {
		t.Errorf("vendored leaflet.js: %d", code)
	}
	if code, _ := get(t, api.URL+"/v1/static/leaflet-1.9.4/leaflet.css"); code != 200 {
		t.Errorf("leaflet.css: %d", code)
	}
	code, body = get(t, api.URL+"/v1/static/maplibre-gl-5.24.1/maplibre-gl.js")
	if code != 200 || !bytes.Contains(body, []byte("MapLibre GL JS")) || !bytes.Contains(body, []byte("v5.24.1")) {
		t.Errorf("vendored maplibre-gl.js: %d", code)
	}
	for _, p := range []string{"/v1/static/maplibre-gl-5.24.1/maplibre-gl.css", "/v1/static/maplibre-gl-leaflet-0.1.4/leaflet-maplibre-gl.js"} {
		if code, _ := get(t, api.URL+p); code != 200 {
			t.Errorf("%s: %d", p, code)
		}
	}
}

// The map page needs two things the other pages must not get: a blob: worker for MapLibre's tile
// decoding, and blob: images. Neither opens a door to another host, and that is what is asserted.
func TestMapPageCSPStillAllowsNoOtherHost(t *testing.T) {
	var calls atomic.Int32
	gh := fakeGH(t, nil, &calls)
	defer gh.Close()
	api := testServer(gh)
	defer api.Close()
	csp := func(path string) string {
		resp, err := http.Get(api.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.Header.Get("Content-Security-Policy")
	}
	page := csp("/v1/")
	for _, want := range []string{"default-src 'none'", "connect-src 'self'", "worker-src blob:", "img-src 'self' data: blob:"} {
		if !strings.Contains(page, want) {
			t.Errorf("map page CSP lacks %q: %s", want, page)
		}
	}
	for _, policy := range []string{page, csp("/v1/privacy"), csp("/v1/about")} {
		for _, bad := range []string{"http:", "https:", "*", "unsafe-eval"} {
			if strings.Contains(policy, bad) {
				t.Errorf("CSP must name no other host and allow no eval, found %q in %s", bad, policy)
			}
		}
	}
	if strings.Contains(csp("/v1/privacy"), "blob:") {
		t.Error("only the map page gets blob:, the other pages keep the stricter policy")
	}
}

// Where a rider is looking is location data, so the tile z/x/y must never reach the log (ADR-017;
// logRequests collapses the path to "/v1/tiles"). Checked on the parsed `path` field rather than by
// searching the whole line: the log carries a timestamp, and fractional seconds beginning "701" made
// a substring search fail about once in fifty runs — a false alarm on a real guarantee is worse than
// no alarm, because it teaches you to ignore it.
func assertNoTileCoordinatesLogged(t *testing.T, logText string, coords ...string) {
	t.Helper()
	seen := 0
	for _, line := range strings.Split(strings.TrimSpace(logText), "\n") {
		if line == "" {
			continue
		}
		var rec struct {
			Msg  string `json:"msg"`
			Path string `json:"path"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log line is not JSON: %q", line)
		}
		if rec.Msg != "http" {
			continue
		}
		seen++
		for _, c := range coords {
			if strings.Contains(rec.Path, c) {
				t.Errorf("tile coordinate %q reached the log in path %q", c, rec.Path)
			}
		}
	}
	if seen == 0 {
		t.Error("no request was logged at all, so this proves nothing")
	}
}
