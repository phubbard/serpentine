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
