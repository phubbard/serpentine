package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Hourly counters, persisted so a deploy doesn't erase the week (ADR-027).
//
// SQLite through its command-line tool rather than a Go driver: every driver is a dependency, this
// server is deliberately stdlib-only, and the write rate is one row an hour — exec cost is noise.
// The result is an ordinary .db file Paul can open with sqlite3, Datasette or anything else.
//
// Aggregates only. The same rule as the rest of ADR-026 applies: there is no column here that could
// hold a coordinate, a route or a rider.

const (
	statsRetentionDays = 90
	statsFlushEvery    = 5 * time.Minute
)

type statsStore struct {
	bin  string // sqlite3, found at startup
	path string
}

// newStatsStore prepares the database. A missing sqlite3 or an unwritable path is reported to the
// caller, which carries on without persistence rather than refusing to serve rides.
func newStatsStore(path string) (*statsStore, error) {
	bin, err := exec.LookPath("sqlite3")
	if err != nil {
		return nil, fmt.Errorf("sqlite3 not found: %w", err)
	}
	s := &statsStore{bin: bin, path: path}
	return s, s.exec(context.Background(), schemaSQL)
}

const schemaSQL = `
CREATE TABLE IF NOT EXISTS hourly (
  hour               INTEGER PRIMARY KEY,  -- unix seconds, hour-truncated
  plans              INTEGER NOT NULL DEFAULT 0,
  cached             INTEGER NOT NULL DEFAULT 0,
  bad_request        INTEGER NOT NULL DEFAULT 0,
  unroutable         INTEGER NOT NULL DEFAULT 0,
  upstream_fail      INTEGER NOT NULL DEFAULT 0,
  mode_loop          INTEGER NOT NULL DEFAULT 0,
  mode_out_and_back  INTEGER NOT NULL DEFAULT 0,
  mode_point_to_point INTEGER NOT NULL DEFAULT 0,
  budget_distance    INTEGER NOT NULL DEFAULT 0,
  budget_time        INTEGER NOT NULL DEFAULT 0,
  charging           INTEGER NOT NULL DEFAULT 0,
  custom_bike        INTEGER NOT NULL DEFAULT 0,
  reserve            INTEGER NOT NULL DEFAULT 0,
  gh_fail            INTEGER NOT NULL DEFAULT 0,
  nrel_fail          INTEGER NOT NULL DEFAULT 0,
  ocm_lookups        INTEGER NOT NULL DEFAULT 0,
  ocm_cache_hits     INTEGER NOT NULL DEFAULT 0,
  ocm_fail           INTEGER NOT NULL DEFAULT 0,
  ocm_replans        INTEGER NOT NULL DEFAULT 0,
  charge_feasible    INTEGER NOT NULL DEFAULT 0,
  charge_infeasible  INTEGER NOT NULL DEFAULT 0,
  charge_no_backup   INTEGER NOT NULL DEFAULT 0,
  tiles              INTEGER NOT NULL DEFAULT 0,
  tile_hits          INTEGER NOT NULL DEFAULT 0,
  latency            TEXT    NOT NULL DEFAULT '[]'  -- histogram, one count per latencyEdges bucket
);
`

// exec runs SQL with no result. The statements are built here from integers, never from anything a
// request carried, so there is nothing to inject.
func (s *statsStore) exec(ctx context.Context, sql string) error {
	cmd := exec.CommandContext(ctx, s.bin, s.path)
	cmd.Stdin = strings.NewReader(sql)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("sqlite3: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func (s *statsStore) query(ctx context.Context, sql string, out any) error {
	cmd := exec.CommandContext(ctx, s.bin, "-json", s.path, sql)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("sqlite3: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	body := bytes.TrimSpace(stdout.Bytes())
	if len(body) == 0 {
		return nil // no rows: sqlite3 prints nothing at all
	}
	return json.Unmarshal(body, out)
}

// row is one persisted hour. The JSON tags match both the column names and the dashboard's, so a
// row and an in-memory bucket stay describable in the same words.
type row struct {
	Hour             int64  `json:"hour"`
	Plans            int    `json:"plans"`
	Cached           int    `json:"cached"`
	Bad              int    `json:"bad_request"`
	Unroutable       int    `json:"unroutable"`
	Upstream         int    `json:"upstream_fail"`
	ModeLoop         int    `json:"mode_loop"`
	ModeOutAndBack   int    `json:"mode_out_and_back"`
	ModePointToPoint int    `json:"mode_point_to_point"`
	BudgetDistance   int    `json:"budget_distance"`
	BudgetTime       int    `json:"budget_time"`
	Charging         int    `json:"charging"`
	CustomBike       int    `json:"custom_bike"`
	Reserve          int    `json:"reserve"`
	GHFail           int    `json:"gh_fail"`
	NRELFail         int    `json:"nrel_fail"`
	OCMLookups       int    `json:"ocm_lookups"`
	OCMHits          int    `json:"ocm_cache_hits"`
	OCMFail          int    `json:"ocm_fail"`
	OCMReplans       int    `json:"ocm_replans"`
	ChargeFeasible   int    `json:"charge_feasible"`
	ChargeInfeasible int    `json:"charge_infeasible"`
	ChargeNoBackup   int    `json:"charge_no_backup"`
	Tiles            int    `json:"tiles"`
	TileHits         int    `json:"tile_hits"`
	Latency          string `json:"latency"`
}

func rowOf(hour int64, b *bucket) row {
	lat, _ := json.Marshal(b.Latency)
	return row{
		Hour: hour, Plans: b.Plans, Cached: b.Cached, Bad: b.Bad, Unroutable: b.Unroute,
		Upstream: b.Upstream,
		ModeLoop: b.Mode["loop"], ModeOutAndBack: b.Mode["out_and_back"],
		ModePointToPoint: b.Mode["point_to_point"],
		BudgetDistance:   b.Budget["distance"], BudgetTime: b.Budget["time"],
		Charging: b.Charging, CustomBike: b.CustomBike, Reserve: b.Reserve,
		GHFail: b.GHFail, NRELFail: b.NRELFail,
		OCMLookups: b.OCMLookups, OCMHits: b.OCMHits, OCMFail: b.OCMFail, OCMReplans: b.OCMReplans,
		ChargeFeasible: b.Feasible, ChargeInfeasible: b.Infeasible, ChargeNoBackup: b.NoBackup,
		Tiles: b.Tiles, TileHits: b.TileHits, Latency: string(lat),
	}
}

func (r row) bucket() *bucket {
	b := newBucket()
	b.Plans, b.Cached, b.Bad, b.Unroute, b.Upstream = r.Plans, r.Cached, r.Bad, r.Unroutable, r.Upstream
	b.Mode["loop"], b.Mode["out_and_back"], b.Mode["point_to_point"] = r.ModeLoop, r.ModeOutAndBack, r.ModePointToPoint
	b.Budget["distance"], b.Budget["time"] = r.BudgetDistance, r.BudgetTime
	b.Charging, b.CustomBike, b.Reserve = r.Charging, r.CustomBike, r.Reserve
	b.GHFail, b.NRELFail = r.GHFail, r.NRELFail
	b.OCMLookups, b.OCMHits, b.OCMFail, b.OCMReplans = r.OCMLookups, r.OCMHits, r.OCMFail, r.OCMReplans
	b.Feasible, b.Infeasible, b.NoBackup = r.ChargeFeasible, r.ChargeInfeasible, r.ChargeNoBackup
	b.Tiles, b.TileHits = r.Tiles, r.TileHits
	var lat []int
	if json.Unmarshal([]byte(r.Latency), &lat) == nil && len(lat) == len(b.Latency) {
		copy(b.Latency, lat)
	}
	return b
}

// save upserts whole hours. An hour is rewritten rather than incremented, so a flush is idempotent
// and a crash between flushes costs at most the last few minutes of counts.
func (s *statsStore) save(ctx context.Context, hours map[int64]*bucket) error {
	if len(hours) == 0 {
		return nil
	}
	var sb strings.Builder
	sb.WriteString("BEGIN;\n")
	for h, b := range hours {
		r := rowOf(h, b)
		fmt.Fprintf(&sb, `INSERT INTO hourly (hour,plans,cached,bad_request,unroutable,upstream_fail,
mode_loop,mode_out_and_back,mode_point_to_point,budget_distance,budget_time,charging,custom_bike,
reserve,gh_fail,nrel_fail,ocm_lookups,ocm_cache_hits,ocm_fail,ocm_replans,charge_feasible,
charge_infeasible,charge_no_backup,tiles,tile_hits,latency) VALUES
(%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,'%s')
ON CONFLICT(hour) DO UPDATE SET plans=excluded.plans,cached=excluded.cached,
bad_request=excluded.bad_request,unroutable=excluded.unroutable,upstream_fail=excluded.upstream_fail,
mode_loop=excluded.mode_loop,mode_out_and_back=excluded.mode_out_and_back,
mode_point_to_point=excluded.mode_point_to_point,budget_distance=excluded.budget_distance,
budget_time=excluded.budget_time,charging=excluded.charging,custom_bike=excluded.custom_bike,
reserve=excluded.reserve,gh_fail=excluded.gh_fail,nrel_fail=excluded.nrel_fail,
ocm_lookups=excluded.ocm_lookups,ocm_cache_hits=excluded.ocm_cache_hits,ocm_fail=excluded.ocm_fail,
ocm_replans=excluded.ocm_replans,charge_feasible=excluded.charge_feasible,
charge_infeasible=excluded.charge_infeasible,charge_no_backup=excluded.charge_no_backup,
tiles=excluded.tiles,tile_hits=excluded.tile_hits,latency=excluded.latency;`+"\n",
			r.Hour, r.Plans, r.Cached, r.Bad, r.Unroutable, r.Upstream,
			r.ModeLoop, r.ModeOutAndBack, r.ModePointToPoint, r.BudgetDistance, r.BudgetTime,
			r.Charging, r.CustomBike, r.Reserve, r.GHFail, r.NRELFail,
			r.OCMLookups, r.OCMHits, r.OCMFail, r.OCMReplans,
			r.ChargeFeasible, r.ChargeInfeasible, r.ChargeNoBackup, r.Tiles, r.TileHits, r.Latency)
	}
	fmt.Fprintf(&sb, "DELETE FROM hourly WHERE hour < %d;\nCOMMIT;\n",
		time.Now().Add(-statsRetentionDays*24*time.Hour).Unix())
	return s.exec(ctx, sb.String())
}

// load reads back the recent hours the dashboard shows, so a restart is invisible.
func (s *statsStore) load(ctx context.Context, since time.Time) (map[int64]*bucket, error) {
	var rows []row
	q := fmt.Sprintf("SELECT * FROM hourly WHERE hour >= %d ORDER BY hour;", since.Unix())
	if err := s.query(ctx, q, &rows); err != nil {
		return nil, err
	}
	out := make(map[int64]*bucket, len(rows))
	for _, r := range rows {
		out[r.Hour] = r.bucket()
	}
	return out, nil
}

// storedHours is what the dashboard shows as "history kept", so a glance says whether persistence is
// actually working.
func (s *statsStore) storedHours(ctx context.Context) int {
	var rows []struct {
		N int `json:"n"`
	}
	if err := s.query(ctx, "SELECT COUNT(*) AS n FROM hourly;", &rows); err != nil || len(rows) == 0 {
		return 0
	}
	return rows[0].N
}
