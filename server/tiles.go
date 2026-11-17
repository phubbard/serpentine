package main

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Map relay for the test page (ADR-046, replacing ADR-016's cached OpenStreetMap rasters). The
// page draws a vector map and asks the host it came from for every piece of it: style, tiles,
// glyphs, sprites. Each request is passed to our own tile server (tiles.phfactor.net, a PMTiles
// planet on the Pi) and the answer handed back, so the browser talks to one host and no map
// company sees anyone. Nothing about the viewer travels upstream: no address, Origin, Referer or
// cookie.
//
// This is the dev-run path (`make run`, which passes -tile-server). In production the Pi's Caddy
// answers /v1/tiles/ itself, from the same four shapes (infra/caddy/serpentine.caddyfile), because
// macOS will not let the launchd agent on axiom connect to another machine on the LAN. Change the
// shapes in both places or not at all.

const (
	tileMaxZoom   = 15 // the archive's deepest level; MapLibre overzooms beyond it
	tileUserAgent = "serpentine-api (+https://serpentine.phfactor.net/v1/)"
	tileSet       = "planet"
	tileBodyLimit = 8 << 20
)

// The four shapes a MapLibre map asks for, as they appear after /v1/tiles/. Anything else is a
// 404 here and never reaches the tile server.
var (
	tilePath   = regexp.MustCompile(`^(\d{1,2})/(\d{1,7})/(\d{1,7})\.mvt$`)
	stylePath  = regexp.MustCompile(`^styles/[a-z]{1,20}\.json$`)
	glyphPath  = regexp.MustCompile(`^fonts/([A-Za-z0-9 ]{1,60})/(\d{1,5}-\d{1,5})\.pbf$`)
	spritePath = regexp.MustCompile(`^sprites/v\d/[a-z]{1,20}(@2x)?\.(json|png)$`)
)

// Headers worth handing back. Content-Encoding is one of them: tiles arrive gzipped and are
// relayed byte for byte rather than unpacked and repacked.
var tileRelayHeaders = []string{"Content-Type", "Content-Encoding", "Content-Length", "ETag", "Cache-Control"}

type tileProxy struct {
	stats *metrics
	base  string // tile server origin, e.g. https://tiles.phfactor.net; "" disables the relay
	http  *http.Client
}

func newTileProxy(base string) *tileProxy {
	return &tileProxy{base: strings.TrimRight(base, "/"), http: &http.Client{Timeout: 20 * time.Second}}
}

// upstreamPath turns what follows /v1/tiles/ into a path on the tile server. ok is false for
// anything outside the four shapes; isTile marks the requests that say where someone is looking.
func upstreamPath(rest string) (path string, isTile, ok bool) {
	if m := tilePath.FindStringSubmatch(rest); m != nil {
		z, _ := strconv.Atoi(m[1])
		x, _ := strconv.Atoi(m[2])
		y, _ := strconv.Atoi(m[3])
		if z > tileMaxZoom || x >= 1<<z || y >= 1<<z {
			return "", true, false
		}
		return "/" + tileSet + "/" + rest, true, true
	}
	if stylePath.MatchString(rest) || spritePath.MatchString(rest) {
		return "/" + rest, false, true
	}
	if m := glyphPath.FindStringSubmatch(rest); m != nil {
		return "/fonts/" + url.PathEscape(m[1]) + "/" + m[2] + ".pbf", false, true
	}
	return "", false, false
}

func (t *tileProxy) handle(w http.ResponseWriter, r *http.Request) {
	if t == nil || t.base == "" {
		http.NotFound(w, r)
		return
	}
	path, isTile, ok := upstreamPath(r.PathValue("rest"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.base+path, nil)
	if err != nil {
		http.Error(w, "map unavailable", http.StatusBadGateway)
		return
	}
	// A fresh request, not a forwarded one: these three headers are all the tile server learns.
	req.Header.Set("User-Agent", tileUserAgent)
	req.Header.Set("Accept-Encoding", "gzip") // set by hand so Go relays the bytes instead of unpacking them
	if inm := r.Header.Get("If-None-Match"); inm != "" {
		req.Header.Set("If-None-Match", inm)
	}
	resp, err := t.http.Do(req)
	if err != nil {
		if isTile {
			t.stats.tile(false)
		}
		http.Error(w, "map unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	// 204 is the tile server's "nothing here" (open ocean), 304 its answer to If-None-Match.
	served := resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotModified
	if isTile {
		t.stats.tile(served)
	}
	if !served {
		if resp.StatusCode == http.StatusNotFound {
			http.NotFound(w, r)
		} else {
			http.Error(w, "map unavailable", http.StatusBadGateway)
		}
		return
	}
	for _, h := range tileRelayHeaders {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.Header().Set("Vary", "Accept-Encoding")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, tileBodyLimit))
}
