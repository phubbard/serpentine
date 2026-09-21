package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// A local copy of NREL's public charging stations, refreshed daily (ADR-028).
//
// The per-plan nearby-route call was the binding constraint on how many riders this can serve:
// api.data.gov allows 1,000 requests an hour on our key, which is sixteen charging plans a minute
// before charging breaks for everyone. The whole country is one call — 81,842 public stations —
// so we fetch it nightly and do the corridor search here instead. It also drops a runtime dependency:
// a plan no longer fails because someone else's API is down.

const (
	stationsRefresh = 24 * time.Hour
	stationsMaxAge  = 7 * 24 * time.Hour // older than this and we say so rather than trust it
	// Stations are bucketed into ~11 km cells so a corridor search touches a handful of them rather
	// than all eighty thousand.
	stationCellDeg = 0.1
)

type cell struct{ x, y int }

type stationStore struct {
	mu      sync.RWMutex
	grid    map[cell][]*nrelStation
	count   int
	fetched time.Time
	path    string
}

func newStationStore(path string) *stationStore {
	return &stationStore{grid: map[cell][]*nrelStation{}, path: path}
}

func cellOf(lon, lat float64) cell {
	return cell{x: int(math.Floor(lon / stationCellDeg)), y: int(math.Floor(lat / stationCellDeg))}
}

// ready reports whether the store can answer, and how old its data is.
func (s *stationStore) ready() (bool, time.Duration) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.count == 0 {
		return false, 0
	}
	return true, time.Since(s.fetched)
}

type stationsFile struct {
	Fetched  time.Time     `json:"fetched"`
	Stations []nrelStation `json:"stations"`
}

// loadFile reads the cached copy from disk, so a restart doesn't need the network.
func (s *stationStore) loadFile() error {
	b, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	var f stationsFile
	if err := json.Unmarshal(b, &f); err != nil {
		return fmt.Errorf("stations: unreadable cache: %w", err)
	}
	s.replace(f.Stations, f.Fetched)
	return nil
}

// refresh pulls every public US station and writes the cache. One request, not one per plan.
func (s *stationStore) refresh(ctx context.Context, c *nrelClient) error {
	stations, err := c.allStations(ctx)
	if err != nil {
		return err
	}
	if len(stations) < 1000 {
		// A near-empty answer is far more likely to be an API hiccup than the country losing its
		// chargers; keep what we have.
		return fmt.Errorf("stations: refused a suspiciously small refresh (%d stations)", len(stations))
	}
	now := time.Now()
	s.replace(stations, now)

	tmp := s.path + ".tmp"
	b, err := json.Marshal(stationsFile{Fetched: now, Stations: stations})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path) // atomic: a torn file would be worse than a stale one
}

func (s *stationStore) replace(stations []nrelStation, fetched time.Time) {
	grid := make(map[cell][]*nrelStation, len(stations)/4)
	for i := range stations {
		st := &stations[i]
		if st.Lat == 0 && st.Lon == 0 {
			continue
		}
		c := cellOf(st.Lon, st.Lat)
		grid[c] = append(grid[c], st)
	}
	s.mu.Lock()
	s.grid, s.count, s.fetched = grid, len(stations), fetched
	s.mu.Unlock()
}

// nearbyRoute answers the question the API's nearby-route endpoint used to: public stations within
// corridorMiles of this polyline, each carrying how far off the route it sits. Same shape, same
// units (OffRouteKM in km), so nothing downstream can tell the difference.
func (s *stationStore) nearbyRoute(coords [][]float64, corridorMiles float64) []nrelStation {
	corridorKM := corridorMiles * 1.609344
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Thin the route the way the API call did: a point every kilometre is plenty for a 2-mile
	// corridor, and it keeps the cell sweep small on a 300 km ride.
	var pts [][]float64
	last := -1
	for i, c := range coords {
		if last >= 0 && i != len(coords)-1 && haversineKM(coords[last], c) < 1.0 {
			continue
		}
		pts = append(pts, c)
		last = i
	}

	// Cells within the corridor of any sampled point. Longitude degrees shrink with latitude, so the
	// span is computed at the route's own latitude.
	reach := map[cell]bool{}
	for _, p := range pts {
		latSpan := corridorKM / 111.0
		lonSpan := corridorKM / (111.0 * math.Max(0.2, math.Cos(p[1]*math.Pi/180)))
		minC, maxC := cellOf(p[0]-lonSpan, p[1]-latSpan), cellOf(p[0]+lonSpan, p[1]+latSpan)
		for x := minC.x; x <= maxC.x; x++ {
			for y := minC.y; y <= maxC.y; y++ {
				reach[cell{x, y}] = true
			}
		}
	}

	// Keyed by station id: the same station sits in one cell but can be reached from several, and a
	// rider should see it once, at its closest approach to the route.
	found := map[int]nrelStation{}
	for c := range reach {
		for _, st := range s.grid[c] {
			d := math.Inf(1)
			for _, p := range pts {
				if dist := haversineKM(p, []float64{st.Lon, st.Lat}); dist < d {
					d = dist
				}
			}
			if d > corridorKM {
				continue
			}
			if prev, seen := found[st.ID]; seen && prev.OffRouteKM <= d {
				continue
			}
			near := *st
			near.OffRouteKM = round2(d)
			found[st.ID] = near
		}
	}
	out := make([]nrelStation, 0, len(found))
	for _, st := range found {
		out = append(out, st)
	}
	return out
}

// syncEvery keeps the local copy fresh. Failures are logged and retried at the next tick: a stale
// station list plans worse rides than a fresh one, but no list at all plans none.
func (s *stationStore) syncEvery(ctx context.Context, c *nrelClient, every time.Duration, log *slog.Logger) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			if err := s.refresh(ctx, c); err != nil {
				log.Warn("stations: refresh", "err", err)
			} else {
				n, _ := s.ready()
				log.Info("stations: refreshed", "ok", n, "count", s.count)
			}
		case <-ctx.Done():
			return
		}
	}
}

// canAnswer is the nil-safe form of ready, so the plan path reads as one condition.
func (s *stationStore) canAnswer() (bool, time.Duration) {
	if s == nil {
		return false, 0
	}
	return s.ready()
}

// writeStations persists the current set, used by refresh and by tests that don't want the network.
func writeStations(path string, s *stationStore) error {
	s.mu.RLock()
	var all []nrelStation
	for _, list := range s.grid {
		for _, st := range list {
			all = append(all, *st)
		}
	}
	fetched := s.fetched
	s.mu.RUnlock()
	b, err := json.Marshal(stationsFile{Fetched: fetched, Stations: all})
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}
