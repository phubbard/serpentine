package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// The guard that matters more than the numbers: a plan leaves counts behind and nothing else
// (ADR-026). If someone later adds a field holding a coordinate, this fails.
func TestStatsHoldNothingAboutARide(t *testing.T) {
	var calls atomic.Int32
	gh := fakeGH(t, nil, &calls)
	defer gh.Close()
	api := testServer(gh)
	defer api.Close()

	// Distinctive coordinates: if any of these reach the stats, the test can spot them.
	body := `{"mode":"loop","start":[-116.868,33.042],"distance_m":150000,"seed":7,"twistiness":0.6}`
	if code, _ := post(t, api.URL+"/v1/plan", body); code != 200 {
		t.Fatalf("plan: %d", code)
	}

	resp, err := http.Get(api.URL + "/stats.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	text := string(raw)

	for _, forbidden := range []string{"116.868", "33.042", "150000", "polyline", "latitude", "longitude", "\"lat\"", "\"lon\"", "start"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("stats leaked %q:\n%s", forbidden, text)
		}
	}

	var s snapshot
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	if s.Today.Plans != 1 || s.Today.Mode["loop"] != 1 {
		t.Errorf("the plan wasn't counted: %+v", s.Today)
	}
	if s.Today.Budget["distance"] != 1 {
		t.Errorf("budget not counted: %+v", s.Today.Budget)
	}
	if s.Today.Charging != 0 || s.Today.CustomBike != 0 {
		t.Errorf("a plain plan shouldn't count as charging or a custom bike: %+v", s.Today)
	}
	// The finest time anything is recorded at is the hour.
	for _, h := range s.Hours {
		if h.Hour%3600 != 0 {
			t.Errorf("hour bucket %d isn't hour-truncated", h.Hour)
		}
	}
}

func TestStatsCountFailuresAndCacheHits(t *testing.T) {
	var calls atomic.Int32
	gh := fakeGH(t, nil, &calls)
	defer gh.Close()
	api := testServer(gh)
	defer api.Close()

	body := `{"mode":"loop","start":[-116.868,33.042],"distance_m":150000,"seed":7}`
	post(t, api.URL+"/v1/plan", body)
	post(t, api.URL+"/v1/plan", body)                                                        // identical: served from the cache
	post(t, api.URL+"/v1/plan", `{"mode":"loop","start":[-116.868,33.042],"distance_m":10}`) // too short

	resp, err := http.Get(api.URL + "/stats.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var s snapshot
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		t.Fatal(err)
	}
	if s.Today.Plans != 2 || s.Today.Cached != 1 {
		t.Errorf("plans %d, cached %d; want 2 and 1", s.Today.Plans, s.Today.Cached)
	}
	if s.Today.Bad != 1 {
		t.Errorf("bad requests %d, want 1", s.Today.Bad)
	}
}

func TestPercentileReadsTheHistogram(t *testing.T) {
	hist := make([]int, len(latencyEdges)+1)
	hist[0] = 90 // 90 fast requests
	hist[4] = 10 // 10 slow ones (<= 2 s)
	if p50 := percentile(hist, 0.5); p50 != latencyEdges[0] {
		t.Errorf("p50 = %v, want %v", p50, latencyEdges[0])
	}
	if p95 := percentile(hist, 0.95); p95 != latencyEdges[4] {
		t.Errorf("p95 = %v, want %v", p95, latencyEdges[4])
	}
	if percentile(make([]int, len(latencyEdges)+1), 0.5) != 0 {
		t.Error("no requests should report zero, not a made-up number")
	}
}

// An empty server must still answer with something the dashboard can iterate: a nil slice marshals
// as null, which threw in the browser and left the page half-drawn.
func TestEmptyStatsAreStillUsable(t *testing.T) {
	m := newMetrics()
	raw, err := json.Marshal(m.snapshot("test", true, false, false, "", ""))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"hours":null`) {
		t.Error("hours must be an empty array, not null")
	}
	var s snapshot
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	if s.Today.Mode == nil || s.Today.Latency == nil {
		t.Error("the empty buckets still need their maps and histogram")
	}
}
