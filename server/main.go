// serpentine-api: the only host the iPhone app talks to. Fronts GraphHopper (and later NREL),
// applies the per-request tuning model, generates and scores loops, and builds the Apple Maps
// handoff. Stdlib only. See docs/API.md and docs/DECISIONS.md.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

var version = "dev" // -ldflags "-X main.version=..."

// testPage is the browser test UI at /v1/ (server/web/index.html).
//
//go:embed web/index.html
var testPage []byte

// privacyPage is the privacy policy at /v1/privacy, linked from TestFlight and the App Store listing.
//
//go:embed web/privacy.html
var privacyPage []byte

// Vendored Leaflet for the test page's map, served at /v1/static/.
//
//go:embed web/vendor
var vendorFS embed.FS

// testPageCSP forbids loading anything from anywhere else: the one-host rule holds for the page
// too. Tiles come from our own /v1/tiles proxy.
const testPageCSP = "default-src 'none'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'none'"

type server struct {
	tiles *tileProxy // nil when -tile-cache is unset: /v1/tiles 404s
	gh    *ghClient
	nrel  *nrelClient // nil when no key is configured: charging requests get 503
	cache *planCache
	log   *slog.Logger
}

func main() {
	addr := flag.String("addr", envOr("SERPENTINE_ADDR", ":8990"), "listen address")
	ghURL := flag.String("gh", envOr("SERPENTINE_GH", "http://localhost:8989"), "GraphHopper base URL")
	nrelURL := flag.String("nrel", envOr("SERPENTINE_NREL", nrelDefaultBase), "NREL alt-fuel-stations base URL")
	nrelKeyFile := flag.String("nrel-key-file", "", "file holding the NREL API key (or set NREL_API_KEY)")
	tileCache := flag.String("tile-cache", "", "directory for cached map tiles (enables /v1/tiles)")
	tileURL := flag.String("tile-upstream", tileDefaultURL, "raster tile URL template")
	flag.Parse()

	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	s := &server{gh: newGHClient(strings.TrimRight(*ghURL, "/")), cache: newPlanCache(500, planCacheTTL), log: log}
	if *tileCache != "" {
		s.tiles = newTileProxy(*tileCache, *tileURL)
	}
	if key, err := nrelKey(*nrelKeyFile); err != nil {
		log.Error("nrel key", "err", err)
		os.Exit(1)
	} else if key != "" {
		s.nrel = newNRELClient(*nrelURL, key)
	} else {
		log.Warn("no NREL key: charging disabled")
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           s.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      90 * time.Second,
	}
	go func() {
		log.Info("listening", "addr", *addr, "graphhopper", *ghURL, "version", version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("listen", "err", err)
			os.Exit(1)
		}
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

// nrelKey reads NREL_API_KEY, else the key file. The key is never logged.
func nrelKey(file string) (string, error) {
	if k := os.Getenv("NREL_API_KEY"); k != "" {
		return k, nil
	}
	if file == "" {
		return "", nil
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/{$}", handleTestPage)
	mux.HandleFunc("GET /v1/privacy", servePage(privacyPage))
	mux.HandleFunc("GET /v1/tiles/{z}/{x}/{y}", s.tiles.handle)
	static, _ := fs.Sub(vendorFS, "web/vendor")
	mux.Handle("GET /v1/static/", http.StripPrefix("/v1/static/", staticHandler(http.FileServerFS(static))))
	mux.HandleFunc("GET /v1/health", s.handleHealth)
	mux.HandleFunc("POST /v1/plan", s.handlePlan)
	mux.HandleFunc("GET /v1/plan/{file}", s.handleGPX)
	return s.logRequests(mux)
}

func staticHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=86400")
		h.ServeHTTP(w, r)
	})
}

func handleTestPage(w http.ResponseWriter, r *http.Request) { servePage(testPage)(w, r) }

func servePage(page []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", testPageCSP)
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(page)
	}
}

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	info, err := s.gh.info(ctx)
	if err != nil {
		s.log.Warn("health: graphhopper down", "err", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "version": version, "graphhopper": "unreachable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "version": version, "graphhopper": info.Version,
		"graph_data_date": info.DataDate, "graph_import_date": info.ImportDate,
		"chargers": s.nrel != nil,
	})
}

func (s *server) handlePlan(w http.ResponseWriter, r *http.Request) {
	var req planRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if err := req.normalize(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Charging != nil && s.nrel == nil {
		writeError(w, http.StatusServiceUnavailable, "charging unavailable: server has no NREL key")
		return
	}
	id := req.cacheKey()
	if res, ok := s.cache.get(id); ok {
		s.log.Info("plan", "mode", req.Mode, "cached", true, "km", res.DistanceM/1000)
		writeJSON(w, http.StatusOK, res)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	start := time.Now()
	cm := customModelFor(*req.Twistiness, req.Avoid)
	var (
		path    *ghPath
		loop    *loopInfo
		ob      *outBackInfo
		turnIdx = -1
		err     error
	)
	switch req.Mode {
	case "point_to_point":
		path, err = s.gh.route(ctx, ghRequest{Points: [][2]float64{*req.Start, *req.End}, CustomModel: cm})
	case "loop":
		path, loop, err = s.planLoop(ctx, &req, cm)
	case "out_and_back":
		path, turnIdx, ob, err = s.planOutBack(ctx, &req, cm)
	}
	if err != nil {
		var ge *ghError
		switch {
		case errors.As(err, &ge):
			writeError(w, http.StatusUnprocessableEntity, ge.Message)
		case errors.Is(err, errNoLoop), errors.Is(err, errNoOutBack):
			writeError(w, http.StatusUnprocessableEntity, err.Error())
		default:
			s.log.Error("plan: routing engine", "err", err)
			writeError(w, http.StatusBadGateway, "routing engine unavailable")
		}
		s.log.Info("plan", "mode", req.Mode, "failed", true, "ms", time.Since(start).Milliseconds())
		return
	}
	var stations []nrelStation
	if req.Charging != nil {
		stations, err = s.nrel.nearbyRoute(ctx, path.Points.Coordinates, chargerCorridorMiles)
		if err != nil {
			s.log.Error("plan: charger data", "err", err)
			writeError(w, http.StatusBadGateway, "charger data unavailable")
			return
		}
	}
	res := buildResult(id, req.Mode, path, loop, ob, turnIdx, stations, req.Charging)
	s.cache.put(id, res)
	attrs := []any{"mode", req.Mode, "km", res.DistanceM / 1000, "ms", time.Since(start).Milliseconds(), "waypoints", len(res.Handoff.Waypoints)}
	if loop != nil {
		attrs = append(attrs, "target_km", loop.TargetM/1000, "candidates", loop.Candidates, "failed", loop.Failed, "score", loop.Score)
	}
	if ob != nil {
		attrs = append(attrs, "out_km", ob.OutKM, "back_km", ob.BackKM, "shared_km", ob.SharedKM, "candidates", ob.Candidates, "failed", ob.Failed)
	}
	if res.Energy != nil {
		attrs = append(attrs, "kwh", res.Energy.KWhEst, "chargers", len(res.Chargers), "stops", res.Energy.Stops, "feasible", res.Energy.Feasible)
	}
	s.log.Info("plan", attrs...)
	writeJSON(w, http.StatusOK, res)
}

func (s *server) handleGPX(w http.ResponseWriter, r *http.Request) {
	id, ok := strings.CutSuffix(r.PathValue("file"), ".gpx")
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	res, ok := s.cache.get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "plan expired; request it again")
		return
	}
	w.Header().Set("Content-Type", "application/gpx+xml")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="serpentine-%s.gpx"`, id))
	writeGPX(w, "serpentine "+res.Mode, res.Polyline)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// logRequests logs method, path, status and duration. No client IPs or coordinates (tile paths
// included): location data never goes in logs.
func (s *server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		path := r.URL.Path
		if strings.HasPrefix(path, "/v1/tiles/") {
			path = "/v1/tiles" // z/x/y is where someone is looking: keep it out of the log
		}
		s.log.Info("http", "method", r.Method, "path", path, "status", sw.status, "ms", time.Since(start).Milliseconds())
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// planCacheTTL is how long a plan (start point included) stays in memory. The privacy page states
// it (web/privacy.html, "24 hours"); change both together.
const planCacheTTL = 24 * time.Hour

// planCache is a bounded in-memory map: repeat requests (and GPX downloads) skip GraphHopper.
type planCache struct {
	mu    sync.Mutex
	max   int
	ttl   time.Duration
	items map[string]cacheItem
}

type cacheItem struct {
	res *planResult
	at  time.Time
}

func newPlanCache(max int, ttl time.Duration) *planCache {
	return &planCache{max: max, ttl: ttl, items: map[string]cacheItem{}}
}

func (c *planCache) get(id string) (*planResult, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	it, ok := c.items[id]
	if !ok || time.Since(it.at) > c.ttl {
		return nil, false
	}
	return it.res, true
}

func (c *planCache) put(id string, res *planResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.items) >= c.max {
		var oldest string
		var oldestAt time.Time
		for k, it := range c.items {
			if oldest == "" || it.at.Before(oldestAt) {
				oldest, oldestAt = k, it.at
			}
		}
		delete(c.items, oldest)
	}
	c.items[id] = cacheItem{res: res, at: time.Now()}
}

func writeGPX(w io.Writer, name string, coords [][]float64) {
	fmt.Fprintf(w, "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<gpx version=\"1.1\" creator=\"serpentine\" xmlns=\"http://www.topografix.com/GPX/1/1\">\n  <trk>\n    <name>%s</name>\n    <trkseg>\n", name)
	for _, c := range coords {
		if len(c) > 2 {
			fmt.Fprintf(w, "      <trkpt lat=\"%.6f\" lon=\"%.6f\"><ele>%.0f</ele></trkpt>\n", c[1], c[0], c[2])
		} else {
			fmt.Fprintf(w, "      <trkpt lat=\"%.6f\" lon=\"%.6f\"></trkpt>\n", c[1], c[0])
		}
	}
	fmt.Fprint(w, "    </trkseg>\n  </trk>\n</gpx>\n")
}
