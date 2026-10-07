package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func testStore(t *testing.T) *statsStore {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("no sqlite3 on this machine")
	}
	store, err := newStatsStore(filepath.Join(t.TempDir(), "stats.db"))
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// A deploy restarts the process; the week's counts must come back (ADR-027).
func TestCountersSurviveARestart(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	before := newMetrics()
	if err := before.restore(ctx, store); err != nil {
		t.Fatal(err)
	}
	before.plan(planShape{Mode: "loop", Budget: "distance", Seconds: 0.3})
	before.plan(planShape{Mode: "out_and_back", Budget: "time", Charging: true, Custom: true, Reserve: true, Seconds: 3})
	before.plan(planShape{Mode: "loop", Budget: "distance", Cached: true})
	before.failure("bad")
	before.chargeOutcome(true, false)
	before.ocm(2, 1, 1)
	before.tile(true)
	if err := before.flush(ctx); err != nil {
		t.Fatal(err)
	}

	// A second process, same database.
	after := newMetrics()
	if err := after.restore(ctx, store); err != nil {
		t.Fatal(err)
	}
	got := after.snapshot("test", true, true, true, "", "").Today
	if got.Plans != 3 || got.Cached != 1 || got.Bad != 1 {
		t.Errorf("plans %d, cached %d, bad %d; want 3, 1, 1", got.Plans, got.Cached, got.Bad)
	}
	if got.Mode["loop"] != 2 || got.Mode["out_and_back"] != 1 {
		t.Errorf("modes came back wrong: %v", got.Mode)
	}
	if got.Budget["time"] != 1 || got.Budget["distance"] != 2 {
		t.Errorf("budgets came back wrong: %v", got.Budget)
	}
	if got.Charging != 1 || got.CustomBike != 1 || got.Reserve != 1 {
		t.Errorf("charging flags came back wrong: %+v", got)
	}
	if got.Feasible != 1 || got.OCMLookups != 2 || got.OCMHits != 1 || got.OCMReplans != 1 || got.TileHits != 1 {
		t.Errorf("upstream counters came back wrong: %+v", got)
	}
	// The latency histogram is what p50/p95 are read from, so it has to survive too.
	if got.P95 == 0 {
		t.Error("latency histogram didn't survive the restart")
	}
	// And counting continues on top rather than starting again.
	after.plan(planShape{Mode: "loop", Budget: "distance"})
	if again := after.snapshot("test", true, true, true, "", "").Today; again.Plans != 4 {
		t.Errorf("after restart plans = %d, want 4", again.Plans)
	}
}

// Flushing twice must not double-count: hours are upserted whole, not incremented.
func TestFlushIsIdempotent(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	m := newMetrics()
	if err := m.restore(ctx, store); err != nil {
		t.Fatal(err)
	}
	m.plan(planShape{Mode: "loop", Budget: "distance"})
	for range 3 {
		if err := m.flush(ctx); err != nil {
			t.Fatal(err)
		}
	}
	reloaded := newMetrics()
	if err := reloaded.restore(ctx, store); err != nil {
		t.Fatal(err)
	}
	if got := reloaded.snapshot("test", true, true, true, "", "").Today.Plans; got != 1 {
		t.Errorf("three flushes produced %d plans, want 1", got)
	}
}

// The database holds counts and nothing else (ADR-026): if a column ever appears that could carry a
// ride, this fails.
func TestDatabaseSchemaHoldsNothingAboutARide(t *testing.T) {
	store := testStore(t)
	var cols []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	if err := store.query(context.Background(), "SELECT name, type FROM pragma_table_info('hourly');", &cols); err != nil {
		t.Fatal(err)
	}
	if len(cols) < 20 {
		t.Fatalf("expected the full counter table, got %d columns", len(cols))
	}
	for _, c := range cols {
		switch c.Name {
		case "hour", "latency":
			continue // an hour bucket and a histogram, both aggregates
		}
		if c.Type != "INTEGER" {
			t.Errorf("column %q is %s; only counts belong here", c.Name, c.Type)
		}
		for _, banned := range []string{"lat", "lon", "coord", "start", "route", "ip", "agent", "bike", "name"} {
			if c.Name == banned {
				t.Errorf("column %q could describe a ride or a rider", c.Name)
			}
		}
	}
}

func TestOldHoursArePruned(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	old := time.Now().Add(-(statsRetentionDays + 5) * 24 * time.Hour).Truncate(time.Hour).Unix()
	b := newBucket()
	b.Plans = 7
	if err := store.save(ctx, map[int64]*bucket{old: b}); err != nil {
		t.Fatal(err)
	}
	// The save that follows carries the retention sweep with it.
	if err := store.save(ctx, map[int64]*bucket{time.Now().Truncate(time.Hour).Unix(): newBucket()}); err != nil {
		t.Fatal(err)
	}
	rows, err := store.load(ctx, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rows[old]; ok {
		t.Errorf("an hour older than %d days is still there", statsRetentionDays)
	}
}

// Platform counts have to survive a restart like everything else, or the day and week totals read
// as "nobody used an iPad since the last deploy".
func TestPlatformCountsSurviveARestart(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	before := newMetrics()
	if err := before.restore(ctx, store); err != nil {
		t.Fatal(err)
	}
	before.plan(planShape{Mode: "loop", Budget: "distance", Platform: "ios"})
	before.plan(planShape{Mode: "loop", Budget: "distance", Platform: "ios"})
	before.plan(planShape{Mode: "loop", Budget: "distance", Platform: "android"})
	before.reach(false, 3, "ipad")
	if err := before.flush(ctx); err != nil {
		t.Fatal(err)
	}

	after := newMetrics()
	if err := after.restore(ctx, store); err != nil {
		t.Fatal(err)
	}
	got := after.snapshot("test", true, true, true, "", "").Today.Platform
	for platform, want := range map[string]int{"ios": 2, "android": 1, "ipad": 1} {
		if got[platform] != want {
			t.Errorf("%s: got %d, want %d (all: %v)", platform, got[platform], want, got)
		}
	}
}

// The case that actually breaks in production: axiom's stats.db already exists with the old schema,
// and `CREATE TABLE IF NOT EXISTS` will not add a column to it. Build a database the old way, then
// open it with the current code and check the new columns both appear and round-trip.
func TestNewColumnsReachAnExistingDatabase(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/old.db"
	bin, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 not installed")
	}
	old := &statsStore{bin: bin, path: path}
	// A deliberately pre-platform schema: the columns this version expects are missing.
	if err := old.exec(ctx, `CREATE TABLE hourly (
  hour INTEGER PRIMARY KEY, plans INTEGER NOT NULL DEFAULT 0, cached INTEGER NOT NULL DEFAULT 0,
  bad_request INTEGER NOT NULL DEFAULT 0, unroutable INTEGER NOT NULL DEFAULT 0,
  upstream_fail INTEGER NOT NULL DEFAULT 0, mode_loop INTEGER NOT NULL DEFAULT 0,
  mode_out_and_back INTEGER NOT NULL DEFAULT 0, mode_point_to_point INTEGER NOT NULL DEFAULT 0,
  budget_distance INTEGER NOT NULL DEFAULT 0, budget_time INTEGER NOT NULL DEFAULT 0,
  charging INTEGER NOT NULL DEFAULT 0, custom_bike INTEGER NOT NULL DEFAULT 0,
  reserve INTEGER NOT NULL DEFAULT 0, gh_fail INTEGER NOT NULL DEFAULT 0,
  nrel_fail INTEGER NOT NULL DEFAULT 0, ocm_lookups INTEGER NOT NULL DEFAULT 0,
  ocm_cache_hits INTEGER NOT NULL DEFAULT 0, ocm_fail INTEGER NOT NULL DEFAULT 0,
  ocm_replans INTEGER NOT NULL DEFAULT 0, charge_feasible INTEGER NOT NULL DEFAULT 0,
  charge_infeasible INTEGER NOT NULL DEFAULT 0, charge_no_backup INTEGER NOT NULL DEFAULT 0,
  tiles INTEGER NOT NULL DEFAULT 0, tile_hits INTEGER NOT NULL DEFAULT 0, latency TEXT);`); err != nil {
		t.Fatal(err)
	}

	// Opening it the normal way must migrate rather than fail.
	store, err := newStatsStore(path)
	if err != nil {
		t.Fatalf("opening an existing pre-platform database failed: %v", err)
	}
	// And a second open must be harmless — every ALTER fails with "duplicate column" from now on.
	if _, err := newStatsStore(path); err != nil {
		t.Fatalf("re-opening a migrated database failed: %v", err)
	}

	m := newMetrics()
	if err := m.restore(ctx, store); err != nil {
		t.Fatal(err)
	}
	m.plan(planShape{Mode: "loop", Budget: "distance", Platform: "android"})
	if err := m.flush(ctx); err != nil {
		t.Fatalf("writing platform counts to a migrated database failed: %v", err)
	}
	back := newMetrics()
	if err := back.restore(ctx, store); err != nil {
		t.Fatal(err)
	}
	if got := back.snapshot("test", true, true, true, "", "").Today.Platform["android"]; got != 1 {
		t.Errorf("platform count did not survive the migration: got %d, want 1", got)
	}
}
