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
	"math"
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

// supportPage is the App Store "Support URL" at /v1/support.
//
//go:embed web/support.html
var supportPage []byte

// aboutPage is the public product page at /v1/about, and at / once Caddy forwards the root.
//
//go:embed web/about.html
var aboutPage []byte

// Screenshots for the about page, served at /v1/img/.
//
//go:embed web/img
var imgFS embed.FS

// statsPage is the operational dashboard at /stats (LAN-only; ADR-026).
//
//go:embed web/stats.html
var statsPage []byte

// vehicleCatalog is the bike list the app caches (ADR-020). Data only: planning still uses the
// vehicle in the request, and today that is always the SR/S.
//
//go:embed vehicles.json
var vehicleCatalog []byte

// Vendored Leaflet for the test page's map, served at /v1/static/.
//
//go:embed web/vendor
var vendorFS embed.FS

// testPageCSP forbids loading anything from anywhere else: the one-host rule holds for the page
// too. Tiles come from our own /v1/tiles proxy.
const testPageCSP = "default-src 'none'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'none'"

type server struct {
	tiles    *tileProxy // nil when -tile-cache is unset: /v1/tiles 404s
	gh       *ghClient
	nrel     *nrelClient   // nil when no key is configured: charging requests get 503
	ocm      *ocmClient    // nil when no key is configured: plans simply carry no reliability data
	stations *stationStore // local copy of NREL's stations; nil falls back to the per-plan API call
	cache    *planCache
	limits   *limiter
	stats    *metrics
	log      *slog.Logger
}

func main() {
	addr := flag.String("addr", envOr("SERPENTINE_ADDR", ":8990"), "listen address")
	ghURL := flag.String("gh", envOr("SERPENTINE_GH", "http://localhost:8989"), "GraphHopper base URL")
	nrelURL := flag.String("nrel", envOr("SERPENTINE_NREL", nrelDefaultBase), "NREL alt-fuel-stations base URL")
	nrelKeyFile := flag.String("nrel-key-file", "", "file holding the NREL API key (or set NREL_API_KEY)")
	ocmURL := flag.String("ocm", envOr("SERPENTINE_OCM", ocmDefaultBase), "Open Charge Map API base URL")
	ocmKeyFile := flag.String("ocm-key-file", "", "file holding the Open Charge Map API key (or set OCM_API_KEY)")
	statsDB := flag.String("stats-db", envOr("SERPENTINE_STATS_DB", ""), "SQLite file for hourly counters (empty: memory only)")
	stationsFile := flag.String("stations-file", envOr("SERPENTINE_STATIONS", ""), "local copy of NREL's stations, refreshed daily (empty: call the API per plan)")
	tileCache := flag.String("tile-cache", "", "directory for cached map tiles (enables /v1/tiles)")
	tileURL := flag.String("tile-upstream", tileDefaultURL, "raster tile URL template")
	flag.Parse()

	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	s := &server{gh: newGHClient(strings.TrimRight(*ghURL, "/")), cache: newPlanCache(500, planCacheTTL),
		stats: newMetrics(), limits: newLimiter(), log: log}
	if *tileCache != "" {
		s.tiles = newTileProxy(*tileCache, *tileURL)
		s.tiles.stats = s.stats
	}
	if key, err := nrelKey(*nrelKeyFile); err != nil {
		log.Error("nrel key", "err", err)
		os.Exit(1)
	} else if key != "" {
		s.nrel = newNRELClient(*nrelURL, key)
	} else {
		log.Warn("no NREL key: charging disabled")
	}
	if key, err := apiKey("OCM_API_KEY", *ocmKeyFile); err != nil {
		log.Error("ocm key", "err", err)
		os.Exit(1)
	} else if key != "" {
		s.ocm = newOCMClient(*ocmURL, key)
		s.ocm.stats = s.stats
	} else {
		log.Warn("no Open Charge Map key: plans carry no charger reliability data")
	}

	// The whole country's chargers, held locally and refreshed daily (ADR-028). The per-plan API call
	// is capped at 1,000/hour, which is the limit on how many riders this can serve.
	syncCtx, stopSync := context.WithCancel(context.Background())
	defer stopSync()
	if *stationsFile != "" && s.nrel != nil {
		store := newStationStore(*stationsFile)
		if err := store.loadFile(); err != nil {
			log.Info("stations: no usable cache, fetching", "err", err)
		}
		s.stations = store
		go func() {
			if ok, age := store.ready(); !ok || age > stationsRefresh {
				if err := store.refresh(syncCtx, s.nrel); err != nil {
					log.Warn("stations: first refresh failed; falling back to per-plan API calls", "err", err)
				} else {
					log.Info("stations: loaded", "count", store.count)
				}
			}
			store.syncEvery(syncCtx, s.nrel, stationsRefresh, log)
		}()
	}

	// Counters survive a restart when a database is configured; without one they are memory-only and
	// a deploy clears them (ADR-027). Either way, a failure here never stops the server serving rides.
	statsCtx, stopStats := context.WithCancel(context.Background())
	defer stopStats()
	if *statsDB != "" {
		if store, err := newStatsStore(*statsDB); err != nil {
			log.Warn("stats: no persistence", "err", err)
		} else if err := s.stats.restore(statsCtx, store); err != nil {
			log.Warn("stats: could not read history", "err", err)
		} else {
			log.Info("stats: persisting", "db", *statsDB, "hours", s.stats.stored)
			go s.stats.flushEvery(statsCtx, statsFlushEvery, log)
		}
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
	stopSync()
	stopStats()                                                 // stop the ticker, then write the current hour synchronously — a goroutine would
	if err := s.stats.flush(context.Background()); err != nil { // lose the race with process exit
		log.Warn("stats: final flush", "err", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

// nrelKey reads NREL_API_KEY, else the key file. The key is never logged.
func nrelKey(file string) (string, error) { return apiKey("NREL_API_KEY", file) }

// apiKey reads the environment variable, else the key file. Keys are never logged.
func apiKey(env, file string) (string, error) {
	if k := os.Getenv(env); k != "" {
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
	mux.HandleFunc("GET /v1/support", servePage(supportPage))
	mux.HandleFunc("GET /v1/about", servePage(aboutPage))
	mux.HandleFunc("GET /v1/vehicles", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		_, _ = w.Write(vehicleCatalog)
	})
	mux.HandleFunc("GET /{$}", servePage(aboutPage))
	imgs, _ := fs.Sub(imgFS, "web/img")
	mux.Handle("GET /v1/img/", http.StripPrefix("/v1/img/", staticHandler(http.FileServerFS(imgs))))
	mux.HandleFunc("GET /v1/tiles/{z}/{x}/{y}", s.tiles.handle)
	static, _ := fs.Sub(vendorFS, "web/vendor")
	mux.Handle("GET /v1/static/", http.StripPrefix("/v1/static/", staticHandler(http.FileServerFS(static))))
	mux.HandleFunc("GET /v1/health", s.handleHealth)
	// Outside /v1 on purpose: the Pi's Caddy proxies /v1/* and /, so these stay on the LAN with no
	// password to leak (ADR-026).
	mux.HandleFunc("GET /stats", servePage(statsPage))
	mux.HandleFunc("GET /stats.json", s.handleStats)
	mux.HandleFunc("POST /v1/plan", s.limitPlans(s.handlePlan))
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

func (s *server) handleStats(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	healthy, ghVersion, graphDate := false, "", ""
	if info, err := s.gh.info(ctx); err == nil {
		healthy, ghVersion, graphDate = true, info.Version, info.DataDate
	}
	writeJSON(w, http.StatusOK, s.stats.snapshot(version, healthy, s.nrel != nil, s.ocm != nil, ghVersion, graphDate))
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
		s.stats.failure("bad")
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Charging != nil && s.nrel == nil {
		writeError(w, http.StatusServiceUnavailable, "charging unavailable: server has no NREL key")
		return
	}
	id := req.cacheKey()
	if res, ok := s.cache.get(id); ok {
		s.stats.plan(req.shape(true, 0))
		s.log.Info("plan", "mode", req.Mode, "cached", true, "km", res.DistanceM/1000)
		writeJSON(w, http.StatusOK, res)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	start := time.Now()
	cm := customModelFor(*req.Twistiness, req.Avoid)
	res, attempts, err := s.planForBudget(ctx, &req, cm, id)
	if err != nil {
		var ge *ghError
		switch {
		case errors.As(err, &ge):
			s.stats.failure("unroutable")
			writeError(w, http.StatusUnprocessableEntity, ge.Message)
		case errors.Is(err, errChargerData):
			s.stats.failure("nrel")
			s.stats.failure("upstream")
			writeError(w, http.StatusBadGateway, "charger data unavailable")
		case errors.Is(err, errNoLoop), errors.Is(err, errNoOutBack):
			s.stats.failure("unroutable")
			writeError(w, http.StatusUnprocessableEntity, err.Error())
		default:
			s.stats.failure("gh")
			s.stats.failure("upstream")
			s.log.Error("plan: routing engine", "err", err)
			writeError(w, http.StatusBadGateway, "routing engine unavailable")
		}
		s.log.Info("plan", "mode", req.Mode, "failed", true, "ms", time.Since(start).Milliseconds())
		return
	}
	s.cache.put(id, res)
	s.stats.plan(req.shape(false, time.Since(start).Seconds()))
	if e := res.Energy; e != nil {
		s.stats.chargeOutcome(e.Feasible, strings.Contains(e.Warning, "no other charger"))
	}
	attrs := []any{"mode", req.Mode, "km", res.DistanceM / 1000, "ms", time.Since(start).Milliseconds(), "waypoints", len(res.Handoff.Waypoints)}
	if loop := res.Loop; loop != nil {
		attrs = append(attrs, "target_km", loop.TargetM/1000, "candidates", loop.Candidates, "failed", loop.Failed, "score", loop.Score)
	}
	if ob := res.OutAndBack; ob != nil {
		attrs = append(attrs, "out_km", ob.OutKM, "back_km", ob.BackKM, "shared_km", ob.SharedKM, "candidates", ob.Candidates, "failed", ob.Failed)
	}
	if d := res.Detour; d != nil {
		attrs = append(attrs, "fastest_s", d.FastestS, "extra_s", d.ExtraS, "twistiness", d.Twistiness)
	}
	if b := res.Budget; b != nil {
		attrs = append(attrs, "budget_s", b.TargetS, "budget_used_s", b.TotalS, "budget_fits", b.Fits, "attempts", attempts)
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

// planForBudget plans the ride, and when the request gave a time budget rather than a distance,
// corrects it: GraphHopper's own time for the winning route replaces budgetSpeedKMH's guess. Up to
// budgetTries attempts, keeping the best plan that fits. Charging dwell counts towards the budget —
// a 90-minute charge stop is 90 minutes the rider doesn't have — so a charging plan that overruns
// shrinks the ride until it fits or runs out of attempts.
func (s *server) planForBudget(ctx context.Context, req *planRequest, cm *customModel, id string) (*planResult, int, error) {
	res, err := s.planOnce(ctx, req, cm, id)
	if err != nil || req.DurationS == 0 {
		return res, 1, err
	}
	// Aim just under: finishing early is fine, running over is a broken promise.
	target := req.DurationS * 0.97
	best, attempts := res, 1
	for attempts < budgetTries && !withinBudget(res, target) {
		scaled := clamp(req.DistanceM*target/budgetUsedS(res), 20_000, 500_000)
		if math.Abs(scaled-req.DistanceM) < 1_000 {
			break // as close as the distance limits allow
		}
		retry := *req
		retry.DistanceM = math.Round(scaled)
		next, err := s.planOnce(ctx, &retry, cm, id)
		if err != nil {
			break // the corrected ride didn't route; keep the one that did
		}
		attempts++
		res = next
		if closerToBudget(next, best, req.DurationS) {
			best = next
		}
	}
	used := budgetUsedS(best)
	b := &budgetInfo{TargetS: req.DurationS, TotalS: math.Round(used), Fits: used <= req.DurationS}
	// Coming back at a third of the time asked for is not "fitting the budget", it is failing to
	// find a ride that fills it. Say which, rather than quietly handing over a token ride.
	if used < req.DurationS*0.6 {
		if best.Energy != nil && best.Energy.Stops > 0 {
			b.Note = "This bike needs a charge stop that won't fit your time. Longer rides here mean charging."
		} else if best.Energy != nil && !best.Energy.Feasible {
			b.Note = "A longer ride would need a charge stop, and there isn't a reachable charger for one."
		} else {
			b.Note = "No longer ride was available from here — this is the best that fits."
		}
	}
	best.Budget = b
	return best, attempts, nil
}

// budgetTries: the first plan plus two corrections. Each is a full fan-out, so this is the ceiling
// on a time-budget request's cost.
const budgetTries = 3

// budgetUsedS is what the rider spends: riding, plus charging and charger detours when planned.
func budgetUsedS(res *planResult) float64 {
	if res.Energy != nil {
		return res.Energy.TotalTimeS
	}
	return res.TimeS
}

// closerToBudget prefers a plan that fits the budget; between two that fit, the longer ride; between
// two that don't, the shorter overrun.
func closerToBudget(a, b *planResult, budget float64) bool {
	ua, ub := budgetUsedS(a), budgetUsedS(b)
	if (ua <= budget) != (ub <= budget) {
		return ua <= budget
	}
	if ua <= budget {
		return ua > ub
	}
	return ua < ub
}

// withinBudget accepts anything from target (budget less its safety margin) up to the budget itself.
func withinBudget(res *planResult, target float64) bool {
	used := budgetUsedS(res)
	return used >= target*0.9 && used <= target/0.97
}

// planOnce is one whole plan: route, chargers, result.
func (s *server) planOnce(ctx context.Context, req *planRequest, cm *customModel, id string) (*planResult, error) {
	var (
		path    *ghPath
		loop    *loopInfo
		ob      *outBackInfo
		detour  *detourInfo
		turnIdx = -1
		err     error
	)
	switch req.Mode {
	case "point_to_point":
		path, detour, err = s.planAtoB(ctx, req, cm)
	case "loop":
		path, loop, err = s.planLoop(ctx, req, cm)
	case "out_and_back":
		path, turnIdx, ob, err = s.planOutBack(ctx, req, cm)
	}
	if err != nil {
		return nil, err
	}
	var stations []nrelStation
	if req.Charging != nil {
		if ok, age := s.stations.canAnswer(); ok {
			stations = s.stations.nearbyRoute(path.Points.Coordinates, chargerCorridorMiles)
			if age > stationsMaxAge {
				s.log.Warn("stations: serving from a stale local copy", "age_h", age.Hours())
			}
		} else if stations, err = s.nrel.nearbyRoute(ctx, path.Points.Coordinates, chargerCorridorMiles,
			req.Vehicle.searchConnectors()); err != nil {
			s.log.Error("plan: charger data", "err", err)
			return nil, errChargerData
		}
	}
	// Plan the stops, then ask Open Charge Map whether riders report them working. A stop reported
	// out of service is dropped and the charge plan is made again without it — once, so a run of bad
	// listings can't loop (ADR-022).
	for attempt := 0; ; attempt++ {
		res := buildResult(id, req.Mode, path, loop, ob, turnIdx, stations, req.Charging, req.Vehicle)
		res.Detour = detour
		if req.Charging == nil || s.ocm == nil {
			return res, nil
		}
		dead := s.addReliability(ctx, res)
		if len(dead) == 0 || attempt > 0 {
			return res, nil
		}
		s.stats.ocm(0, 0, len(dead))
		s.log.Info("plan: charger reported out of service, replanning stops", "dropped", len(dead))
		stations = withoutStations(stations, dead)
	}
}

var errChargerData = errors.New("charger data unavailable")
