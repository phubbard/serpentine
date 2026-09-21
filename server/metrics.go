package main

import (
	"context"
	"log/slog"
	"maps"
	"slices"
	"sort"
	"sync"
	"time"
)

// Operational counters (ADR-026). Counters, never events: there is nowhere in this file to put a
// coordinate, a polyline, a distance, an IP or a timestamp finer than the hour, and a test asserts
// that stays true. A tally cannot be turned back into someone's Saturday.
//
// Served at /stats and /stats.json — outside /v1, which is the only prefix the Pi's Caddy proxies,
// so these are LAN-only by construction rather than by a password someone has to remember.

const (
	metricHours = 7 * 24 // a week of hourly buckets, a few kilobytes, lost on restart
)

// latencyEdges are the upper bounds, in seconds, of the histogram buckets.
var latencyEdges = []float64{0.1, 0.25, 0.5, 1, 2, 5, 10, 30}

type bucket struct {
	Plans    int            `json:"plans"`
	Cached   int            `json:"cached"`
	Bad      int            `json:"bad_request"`   // 4xx: the rider asked for something impossible
	Unroute  int            `json:"unroutable"`    // 422: no ride exists there
	Upstream int            `json:"upstream_fail"` // 5xx: GraphHopper, NREL or us
	Mode     map[string]int `json:"mode"`
	Budget   map[string]int `json:"budget"`

	Charging   int `json:"charging"`
	CustomBike int `json:"custom_bike"` // a bike that isn't the default, never which one
	Reserve    int `json:"reserve"`

	Latency []int `json:"latency"` // len(latencyEdges)+1

	GHFail   int `json:"gh_fail"`
	NRELFail int `json:"nrel_fail"`

	OCMLookups int `json:"ocm_lookups"`
	OCMHits    int `json:"ocm_cache_hits"`
	OCMFail    int `json:"ocm_fail"`
	OCMReplans int `json:"ocm_replans"` // a stop was reported dead and the plan was redone

	Feasible   int `json:"charge_feasible"`
	Infeasible int `json:"charge_infeasible"`
	NoBackup   int `json:"charge_no_backup"`

	Tiles    int `json:"tiles"`
	TileHits int `json:"tile_hits"`
}

func newBucket() *bucket {
	return &bucket{Mode: map[string]int{}, Budget: map[string]int{}, Latency: make([]int, len(latencyEdges)+1)}
}

type metrics struct {
	mu      sync.Mutex
	started time.Time
	hours   map[int64]*bucket
	store   *statsStore // nil: counters live only in memory and a restart clears them
	stored  int         // hours on disk, shown on the dashboard so a glance says persistence works
}

// restore seeds the in-memory week from disk, so a deploy doesn't erase the history.
func (m *metrics) restore(ctx context.Context, store *statsStore) error {
	hours, err := store.load(ctx, time.Now().Add(-metricHours*time.Hour))
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.store = store
	m.hours = hours
	m.stored = store.storedHours(ctx)
	return nil
}

// flush writes the hours that can still be changing. Whole-hour upserts, so this is idempotent and
// a crash costs at most the last few minutes of counts.
func (m *metrics) flush(ctx context.Context) error {
	m.mu.Lock()
	if m.store == nil {
		m.mu.Unlock()
		return nil
	}
	store := m.store
	recent := map[int64]*bucket{}
	cutoff := time.Now().Add(-2 * time.Hour).Unix()
	for h, b := range m.hours {
		if h >= cutoff {
			copy := *b
			copy.Mode, copy.Budget = maps.Clone(b.Mode), maps.Clone(b.Budget)
			copy.Latency = slices.Clone(b.Latency)
			recent[h] = &copy
		}
	}
	m.mu.Unlock()

	if err := store.save(ctx, recent); err != nil {
		return err
	}
	n := store.storedHours(ctx)
	m.mu.Lock()
	m.stored = n
	m.mu.Unlock()
	return nil
}

// flushEvery keeps the database current until the context is cancelled. The *final* flush is not
// done here: a goroutine racing process exit loses the write, which is exactly what happened the
// first time this shipped. main calls flush synchronously on the way out.
func (m *metrics) flushEvery(ctx context.Context, every time.Duration, log *slog.Logger) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			if err := m.flush(ctx); err != nil {
				log.Warn("stats: flush", "err", err)
			}
		case <-ctx.Done():
			return
		}
	}
}

func newMetrics() *metrics {
	return &metrics{started: time.Now(), hours: map[int64]*bucket{}}
}

// now returns this hour's bucket, pruning anything older than a week. Caller holds the lock.
func (m *metrics) now() *bucket {
	h := time.Now().Truncate(time.Hour).Unix()
	b, ok := m.hours[h]
	if !ok {
		b = newBucket()
		m.hours[h] = b
		cutoff := h - metricHours*3600
		for k := range m.hours {
			if k < cutoff {
				delete(m.hours, k)
			}
		}
	}
	return b
}

func (m *metrics) with(f func(b *bucket)) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	f(m.now())
}

// planShape is what a served plan tells us: nothing about where, only what kind.
type planShape struct {
	Mode     string
	Budget   string // "distance" or "time"
	Charging bool
	Custom   bool // a bike other than the default — never which bike (ADR-026)
	Reserve  bool
	Cached   bool
	Seconds  float64
}

func (m *metrics) plan(p planShape) {
	m.with(func(b *bucket) {
		b.Plans++
		b.Mode[p.Mode]++
		b.Budget[p.Budget]++
		if p.Cached {
			b.Cached++
		}
		if p.Charging {
			b.Charging++
		}
		if p.Custom {
			b.CustomBike++
		}
		if p.Reserve {
			b.Reserve++
		}
		i := sort.SearchFloat64s(latencyEdges, p.Seconds)
		b.Latency[i]++
	})
}

func (m *metrics) chargeOutcome(feasible, noBackup bool) {
	m.with(func(b *bucket) {
		if feasible {
			b.Feasible++
		} else {
			b.Infeasible++
		}
		if noBackup {
			b.NoBackup++
		}
	})
}

func (m *metrics) failure(kind string) {
	m.with(func(b *bucket) {
		switch kind {
		case "bad":
			b.Bad++
		case "unroutable":
			b.Unroute++
		case "upstream":
			b.Upstream++
		case "gh":
			b.GHFail++
		case "nrel":
			b.NRELFail++
		case "ocm":
			b.OCMFail++
		}
	})
}

func (m *metrics) ocm(lookups, cacheHits, replans int) {
	m.with(func(b *bucket) {
		b.OCMLookups += lookups
		b.OCMHits += cacheHits
		b.OCMReplans += replans
	})
}

func (m *metrics) tile(hit bool) {
	m.with(func(b *bucket) {
		b.Tiles++
		if hit {
			b.TileHits++
		}
	})
}

// snapshot is the dashboard's whole view of the world.
type snapshot struct {
	Version      string       `json:"version"`
	UptimeS      int64        `json:"uptime_s"`
	GraphDate    string       `json:"graph_data_date,omitempty"`
	GraphHopper  string       `json:"graphhopper,omitempty"`
	Healthy      bool         `json:"healthy"`
	Chargers     bool         `json:"chargers"`
	Reliability  bool         `json:"reliability"`
	StoredHours  int          `json:"stored_hours"` // rows in the database; 0 = memory only
	LatencyEdges []float64    `json:"latency_edges"`
	Hours        []hourPoint  `json:"hours"`
	Today        bucketTotals `json:"today"`
	Week         bucketTotals `json:"week"`
}

type hourPoint struct {
	Hour  int64 `json:"hour"` // unix seconds, hour-truncated — the finest time this file records
	Plans int   `json:"plans"`
	Fails int   `json:"fails"`
}

type bucketTotals struct {
	bucket
	P50 float64 `json:"p50_s"`
	P95 float64 `json:"p95_s"`
}

func (m *metrics) snapshot(version string, healthy, chargers, reliability bool, ghVersion, graphDate string) snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()

	s := snapshot{
		Hours:   []hourPoint{}, // never null: the dashboard iterates it
		Version: version, UptimeS: int64(time.Since(m.started).Seconds()),
		GraphHopper: ghVersion, GraphDate: graphDate,
		Healthy: healthy, Chargers: chargers, Reliability: reliability, StoredHours: m.stored,
		LatencyEdges: latencyEdges,
	}
	dayStart := time.Now().Truncate(time.Hour).Add(-23 * time.Hour).Unix()
	today, week := newBucket(), newBucket()
	var hours []int64
	for h := range m.hours {
		hours = append(hours, h)
	}
	sort.Slice(hours, func(i, j int) bool { return hours[i] < hours[j] })
	for _, h := range hours {
		b := m.hours[h]
		addTo(week, b)
		if h >= dayStart {
			addTo(today, b)
		}
		s.Hours = append(s.Hours, hourPoint{Hour: h, Plans: b.Plans, Fails: b.Bad + b.Unroute + b.Upstream})
	}
	s.Today = bucketTotals{bucket: *today, P50: percentile(today.Latency, 0.5), P95: percentile(today.Latency, 0.95)}
	s.Week = bucketTotals{bucket: *week, P50: percentile(week.Latency, 0.5), P95: percentile(week.Latency, 0.95)}
	return s
}

func addTo(dst, src *bucket) {
	dst.Plans += src.Plans
	dst.Cached += src.Cached
	dst.Bad += src.Bad
	dst.Unroute += src.Unroute
	dst.Upstream += src.Upstream
	dst.Charging += src.Charging
	dst.CustomBike += src.CustomBike
	dst.Reserve += src.Reserve
	dst.GHFail += src.GHFail
	dst.NRELFail += src.NRELFail
	dst.OCMLookups += src.OCMLookups
	dst.OCMHits += src.OCMHits
	dst.OCMFail += src.OCMFail
	dst.OCMReplans += src.OCMReplans
	dst.Feasible += src.Feasible
	dst.Infeasible += src.Infeasible
	dst.NoBackup += src.NoBackup
	dst.Tiles += src.Tiles
	dst.TileHits += src.TileHits
	for k, v := range src.Mode {
		dst.Mode[k] += v
	}
	for k, v := range src.Budget {
		dst.Budget[k] += v
	}
	for i, v := range src.Latency {
		dst.Latency[i] += v
	}
}

// percentile reads a latency histogram back as "at most this many seconds", using the bucket's upper
// edge. The last bucket has no upper bound, so it reports the largest edge — honest enough for "is
// it slow", which is all this is for.
func percentile(hist []int, p float64) float64 {
	total := 0
	for _, n := range hist {
		total += n
	}
	if total == 0 {
		return 0
	}
	want, seen := float64(total)*p, 0
	for i, n := range hist {
		seen += n
		if float64(seen) >= want {
			if i >= len(latencyEdges) {
				return latencyEdges[len(latencyEdges)-1]
			}
			return latencyEdges[i]
		}
	}
	return latencyEdges[len(latencyEdges)-1]
}
