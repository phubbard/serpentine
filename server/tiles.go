package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Caching raster tile proxy for the test page's map (ADR-016). The browser only ever talks to
// serpentine.phfactor.net; OSM's tile server sees one cached fetch per tile from axiom, never the
// viewer. Stopgap until self-hosted vector tiles (roadmap). OSM tile usage policy: identify the
// app, cache, at most 2 connections, show attribution (the page does).

const (
	tileMaxZoom      = 17
	tileMaxAge       = 30 * 24 * time.Hour // refetch after this; serve stale if upstream fails
	tileUpstreamConc = 2
	tileUserAgent    = "serpentine-test-page/1.0 (+https://serpentine.phfactor.net/v1/)"
	tileDefaultURL   = "https://tile.openstreetmap.org/{z}/{x}/{y}.png"
)

type tileProxy struct {
	dir      string // cache root; "" disables the proxy
	upstream string // URL template with {z} {x} {y}
	http     *http.Client
	sem      chan struct{}

	mu       sync.Mutex
	inflight map[string]*tileFetch
}

type tileFetch struct {
	done chan struct{}
	err  error
}

func newTileProxy(dir, upstream string) *tileProxy {
	return &tileProxy{
		dir: dir, upstream: upstream,
		http:     &http.Client{Timeout: 20 * time.Second},
		sem:      make(chan struct{}, tileUpstreamConc),
		inflight: map[string]*tileFetch{},
	}
}

// parseTile validates z/x/y from the path; y arrives as "123.png".
func parseTile(zs, xs, ys string) (z, x, y int, err error) {
	ys, ok := strings.CutSuffix(ys, ".png")
	if !ok {
		return 0, 0, 0, errors.New("not a png tile")
	}
	if z, err = strconv.Atoi(zs); err != nil {
		return
	}
	if x, err = strconv.Atoi(xs); err != nil {
		return
	}
	if y, err = strconv.Atoi(ys); err != nil {
		return
	}
	n := 1 << z
	if z < 0 || z > tileMaxZoom || x < 0 || x >= n || y < 0 || y >= n {
		return 0, 0, 0, errors.New("tile out of range")
	}
	return z, x, y, nil
}

func (t *tileProxy) path(z, x, y int) string {
	return filepath.Join(t.dir, strconv.Itoa(z), strconv.Itoa(x), strconv.Itoa(y)+".png")
}

// fetch downloads one tile into the cache. Concurrent requests for the same tile share one fetch.
func (t *tileProxy) fetch(ctx context.Context, z, x, y int) error {
	key := fmt.Sprintf("%d/%d/%d", z, x, y)
	t.mu.Lock()
	if f, ok := t.inflight[key]; ok {
		t.mu.Unlock()
		<-f.done
		return f.err
	}
	f := &tileFetch{done: make(chan struct{})}
	t.inflight[key] = f
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		delete(t.inflight, key)
		t.mu.Unlock()
		close(f.done)
	}()

	t.sem <- struct{}{}
	defer func() { <-t.sem }()
	url := strings.NewReplacer("{z}", strconv.Itoa(z), "{x}", strconv.Itoa(x), "{y}", strconv.Itoa(y)).Replace(t.upstream)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		f.err = err
		return err
	}
	req.Header.Set("User-Agent", tileUserAgent)
	resp, err := t.http.Do(req)
	if err != nil {
		f.err = err
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		f.err = fmt.Errorf("tile upstream %d", resp.StatusCode)
		return f.err
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		f.err = err
		return err
	}
	p := t.path(z, x, y)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.err = err
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		f.err = err
		return err
	}
	f.err = os.Rename(tmp, p)
	return f.err
}

func (t *tileProxy) handle(w http.ResponseWriter, r *http.Request) {
	if t == nil || t.dir == "" {
		http.NotFound(w, r)
		return
	}
	z, x, y, err := parseTile(r.PathValue("z"), r.PathValue("x"), r.PathValue("y"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	p := t.path(z, x, y)
	st, statErr := os.Stat(p)
	if statErr != nil || time.Since(st.ModTime()) > tileMaxAge {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		ferr := t.fetch(ctx, z, x, y)
		cancel()
		if ferr != nil && statErr != nil { // nothing cached to fall back on
			http.Error(w, "tile unavailable", http.StatusBadGateway)
			return
		}
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=604800")
	http.ServeFile(w, r, p)
}
