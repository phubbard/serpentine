package main

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Fixtures are real GraphHopper 11 responses from axiom (2026-09-18, base profile v0.2):
//   gh_demo.json        UTC → Ramona → Julian → Mount Laguna → UTC (demo ride, 225 km)
//   gh_loop_track.json  150 km round_trip from downtown SD, seed 1 heading 45 (runs Marron Valley Rd)
//   gh_error.json       round_trip that fails ("Could not find a valid point")

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func fixturePath(t *testing.T, name string) *ghPath {
	t.Helper()
	p, err := parseGHResponse(200, loadFixture(t, name))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseGHResponse(t *testing.T) {
	p := fixturePath(t, "gh_demo.json")
	if p.Distance < 220_000 || p.Distance > 230_000 {
		t.Errorf("demo distance %.0f m, want ~225 km", p.Distance)
	}
	for _, k := range ghDetails {
		if len(p.Details[k]) == 0 {
			t.Errorf("detail %q missing", k)
		}
	}
	if d := p.Details["road_class"][0]; d.Str == "" || d.IsNum {
		t.Errorf("road_class detail should be a string, got %+v", d)
	}
	if d := p.Details["curvature"][0]; !d.IsNum {
		t.Errorf("curvature detail should be numeric, got %+v", d)
	}

	_, err := parseGHResponse(400, loadFixture(t, "gh_error.json"))
	var ge *ghError
	if !errors.As(err, &ge) || !strings.Contains(ge.Message, "valid point") {
		t.Errorf("want ghError with GraphHopper's message, got %v", err)
	}
	if _, err := parseGHResponse(502, []byte("<html>bad gateway</html>")); err == nil || errors.As(err, &ge) {
		t.Errorf("a 502 must be an engine error, not a routing (4xx) error: %v", err)
	}
}

func TestStats(t *testing.T) {
	p := fixturePath(t, "gh_demo.json")
	s := computeStats(p, cumulativeKM(p.Points.Coordinates))
	if math.Abs(s.KM-p.Distance/1000)/(p.Distance/1000) > 0.01 {
		t.Errorf("haversine length %.1f km vs GraphHopper %.1f km", s.KM, p.Distance/1000)
	}
	var sum float64
	for _, km := range s.RoadClass {
		sum += km
	}
	if math.Abs(sum-s.KM) > 0.5 {
		t.Errorf("road classes sum to %.1f km, route is %.1f km", sum, s.KM)
	}
	if s.Urban["rural"] < 100 {
		t.Errorf("demo ride should be mostly rural, got %.0f km", s.Urban["rural"])
	}

	track := fixturePath(t, "gh_loop_track.json")
	ts := computeStats(track, cumulativeKM(track.Points.Coordinates))
	if ts.RoadClass["track"] < 10 {
		t.Errorf("Marron Valley loop should show >10 km of track, got %.1f", ts.RoadClass["track"])
	}
}

func TestLoopScorePenalisesTrackAndDistanceError(t *testing.T) {
	good := routeStats{KM: 150, RoadClass: map[string]float64{"secondary": 150}, Urban: map[string]float64{"rural": 150}, CurvyKM: 60}
	withTrack := good
	withTrack.RoadClass = map[string]float64{"secondary": 130, "track": 20}
	long := good
	long.KM = 230
	if loopScore(withTrack, 150) <= loopScore(good, 150) {
		t.Error("track should raise the score")
	}
	if loopScore(long, 150) <= loopScore(good, 150) {
		t.Error("overshooting the target should raise the score")
	}
	if !math.IsInf(loopScore(routeStats{}, 150), 1) {
		t.Error("empty route should score +Inf")
	}
}

func TestHandoffDemo(t *testing.T) {
	p := fixturePath(t, "gh_demo.json")
	cum := cumulativeKM(p.Points.Coordinates)
	h := buildHandoff(p, cum, roadsOf(p, cum), nil)

	if n := len(h.Waypoints); n == 0 || n > maxHandoffWaypoints {
		t.Fatalf("%d waypoints, want 1..%d", n, maxHandoffWaypoints)
	}
	if !strings.Contains(strings.Join(h.WaypointRoads, "|"), "Sunrise Highway") {
		t.Errorf("Sunrise Highway must get a waypoint; roads: %v", h.WaypointRoads)
	}
	if strings.Count(h.AppleMapsURL, "&waypoint=") != len(h.Waypoints) {
		t.Errorf("waypoint count mismatch in %s", h.AppleMapsURL)
	}
	if !strings.HasPrefix(h.AppleMapsURL, "https://maps.apple.com/directions?source=32.") ||
		!strings.HasSuffix(h.AppleMapsURL, "&mode=driving&avoid=tolls,highways") {
		t.Errorf("unexpected Apple URL shape (lat,lon order?): %s", h.AppleMapsURL)
	}
	// Every waypoint lies on our polyline and away from the endpoints.
	for i, w := range h.Waypoints {
		if haversineKM(w[:], h.Source[:]) < endpointClearKM {
			t.Errorf("waypoint %d too close to source", i)
		}
		found := false
		for _, c := range p.Points.Coordinates {
			if c[0] == w[0] && c[1] == w[1] {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("waypoint %d %v is not a polyline vertex", i, w)
		}
	}
}

func TestHandoffOneWaypointPerRoad(t *testing.T) {
	// 0-50 km on A, a 0.2 km bridge named B, then A again: A gets one waypoint, not two.
	var coords [][]float64
	for i := 0; i <= 200; i++ {
		coords = append(coords, []float64{-117 + float64(i)*0.005, 33}) // ~0.47 km steps
	}
	p := &ghPath{Instructions: []ghInstruction{
		{StreetName: "Start Rd", Interval: [2]int{0, 20}},
		{StreetName: "A", Interval: [2]int{20, 100}},
		{StreetName: "B", Interval: [2]int{100, 101}},
		{StreetName: "A", Interval: [2]int{101, 180}},
		{StreetName: "End Rd", Interval: [2]int{180, 200}},
	}}
	p.Points.Coordinates = coords
	cum := cumulativeKM(coords)
	h := buildHandoff(p, cum, roadsOf(p, cum), nil)
	// Start Rd's 1 km point is inside endpointClearKM of the source, so it moves to mid-road.
	if got := strings.Join(h.WaypointRoads, ","); got != "Start Rd,A,End Rd" {
		t.Errorf("waypoint roads %q, want Start Rd,A,End Rd", got)
	}
}

func TestPublicEndpointsSkipServiceRoads(t *testing.T) {
	p := &ghPath{Details: map[string][]ghDetail{"road_class": {
		{From: 0, To: 3, Str: "service"}, {From: 3, To: 8, Str: "secondary"}, {From: 8, To: 10, Str: "service"},
	}}}
	first, last := publicEndpoints(p, 11)
	if first != 3 || last != 8 {
		t.Errorf("got %d..%d, want 3..8", first, last)
	}
}

func TestNormalize(t *testing.T) {
	cases := []struct {
		body string
		ok   bool
	}{
		{`{"mode":"loop","start":[-116.87,33.04],"distance_m":150000}`, true},
		{`{"mode":"loop","start":[-116.87,33.04],"distance_m":5000}`, false},
		{`{"mode":"loop","distance_m":150000}`, false},
		{`{"mode":"point_to_point","start":[-116.87,33.04]}`, false},
		{`{"mode":"point_to_point","start":[-116.87,33.04],"end":[-116.60,33.08]}`, true},
		{`{"mode":"point_to_point","start":[33.04,-116.87],"end":[-116.60,33.08]}`, false}, // lat,lon swapped
		{`{"mode":"out_and_back","start":[-116.87,33.04],"distance_m":150000}`, true},
		{`{"mode":"out_and_back","start":[-116.87,33.04],"turnaround":[-116.60,33.08]}`, true},
		{`{"mode":"out_and_back","start":[-116.87,33.04]}`, false},
		{`{"mode":"loop","start":[-116.87,33.04],"distance_m":150000,"twistiness":1.5}`, false},
		{`{"mode":"loop","start":[-116.87,33.04],"distance_m":150000,"avoid":["tolls"]}`, false},
		{`{"mode":"loop","start":[-116.87,33.04],"distance_m":150000,"charging":{"enabled":true}}`, true},
		{`{"mode":"loop","start":[-116.87,33.04],"distance_m":150000,"charging":{"enabled":true,"soc_start":1.2}}`, false},
		{`{"mode":"loop","start":[-116.87,33.04],"distance_m":150000,"charging":{"enabled":true,"soc_start":0.3,"soc_min_arrival":0.4}}`, false},
		{`{"mode":"loop","start":[-116.87,33.04],"distance_m":150000,"charging":{"enabled":false,"soc_start":7}}`, true}, // ignored when off
	}
	for _, c := range cases {
		var r planRequest
		if err := json.Unmarshal([]byte(c.body), &r); err != nil {
			t.Fatal(err)
		}
		if err := r.normalize(); (err == nil) != c.ok {
			t.Errorf("%s: err=%v, want ok=%v", c.body, err, c.ok)
		}
	}
}

func TestCacheKeyStableAcrossDefaults(t *testing.T) {
	key := func(body string) string {
		var r planRequest
		_ = json.Unmarshal([]byte(body), &r)
		if err := r.normalize(); err != nil {
			t.Fatal(err)
		}
		return r.cacheKey()
	}
	a := key(`{"mode":"loop","start":[-116.87,33.04],"distance_m":150000}`)
	b := key(`{"mode":"loop","start":[-116.87,33.04],"distance_m":150000,"seed":1,"twistiness":0.5,"avoid":["unpaved","ferries"]}`)
	c := key(`{"mode":"loop","start":[-116.87,33.04],"distance_m":150000,"seed":2}`)
	if a != b {
		t.Error("explicit defaults should hash like omitted ones")
	}
	if a == c {
		t.Error("different seed must hash differently")
	}
}

func TestCustomModelOnlyTightens(t *testing.T) {
	for _, tw := range []float64{0, 0.3, 0.5, 1} {
		cm := customModelFor(tw, []string{"unpaved", "ferries"})
		for _, r := range cm.Priority {
			v, err := strconv.ParseFloat(r.MultiplyBy, 64)
			if err != nil || v < 0 || v > 1 {
				t.Errorf("twistiness %.1f: multiply_by %q breaks LM (must be 0..1)", tw, r.MultiplyBy)
			}
		}
	}
	if customModelFor(0, nil) != nil {
		t.Error("no twistiness and no avoids should send no custom model")
	}
}

// fakeGH serves fixtures: round trips with a heading in failHeadings get GraphHopper's 400.
func fakeGH(t *testing.T, failHeadings map[float64]bool, calls *atomic.Int32) *httptest.Server {
	demo, loop, fail := loadFixture(t, "gh_demo.json"), loadFixture(t, "gh_loop_track.json"), loadFixture(t, "gh_error.json")
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/info":
			_, _ = w.Write([]byte(`{"version":"11.0","data_date":"2026-09-17T20:21:05Z","import_date":"2026-09-18T23:29:37Z"}`))
		case "/route":
			calls.Add(1)
			var req ghRequest
			body, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(body, &req); err != nil {
				t.Errorf("bad request to GraphHopper: %v", err)
			}
			if !strings.Contains(string(body), `"headings"`) && req.Algorithm == "round_trip" {
				t.Error(`round trip sent without "headings"`)
			}
			switch {
			case req.Algorithm != "round_trip":
				_, _ = w.Write(demo)
			case failHeadings[req.Headings[0]]:
				w.WriteHeader(400)
				_, _ = w.Write(fail)
			default:
				_, _ = w.Write(loop)
			}
		default:
			http.NotFound(w, r)
		}
	}))
}

func testServer(gh *httptest.Server) *httptest.Server { return testServerWithNREL(gh, nil) }

func testServerWithNREL(gh *httptest.Server, nrel *nrelClient) *httptest.Server {
	s := &server{gh: newGHClient(gh.URL), nrel: nrel, cache: newPlanCache(10, time.Hour), log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	return httptest.NewServer(s.routes())
}

func post(t *testing.T, url, body string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestPlanEndpoints(t *testing.T) {
	var calls atomic.Int32
	gh := fakeGH(t, map[float64]bool{0: true, 45: true}, &calls)
	defer gh.Close()
	api := testServer(gh)
	defer api.Close()

	code, res := post(t, api.URL+"/v1/plan", `{"mode":"point_to_point","start":[-117.2116,32.867],"end":[-116.4185,32.87]}`)
	if code != 200 || res["handoff"] == nil || res["gpx_url"] == nil {
		t.Fatalf("point_to_point: %d %v", code, res["error"])
	}

	code, res = post(t, api.URL+"/v1/plan", `{"mode":"loop","start":[-117.16,32.72],"distance_m":150000}`)
	if code != 200 {
		t.Fatalf("loop: %d %v", code, res["error"])
	}
	loop := res["loop"].(map[string]any)
	if loop["failed"].(float64) != 4 { // headings 0 and 45 x 2 seeds
		t.Errorf("want 4 failed candidates, got %v", loop["failed"])
	}
	if h := loop["heading_deg"].(float64); h == 0 || h == 45 {
		t.Errorf("winner came from a failing heading: %v", h)
	}

	// Same request again: served from cache, no GraphHopper calls.
	before := calls.Load()
	if code, _ = post(t, api.URL+"/v1/plan", `{"mode":"loop","start":[-117.16,32.72],"distance_m":150000}`); code != 200 || calls.Load() != before {
		t.Errorf("repeat request should be cached (code %d, %d new GH calls)", code, calls.Load()-before)
	}

	resp, err := http.Get(api.URL + res["gpx_url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	gpx, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(gpx), "<trkpt") {
		t.Errorf("gpx: %d", resp.StatusCode)
	}

	if code, _ = post(t, api.URL+"/v1/plan", `{"mode":"loop","start":[-117.16,32.72],"distance_m":150000,"heading_deg":0,"seed":9}`); code != 200 {
		t.Errorf("heading 0 fans out to 330/30 too, so it should still find a loop; got %d", code)
	}
	if code, _ = post(t, api.URL+"/v1/plan", `{"mode":"loop","start":[-117.16,32.72],"distance_m":150000,"heading_deg":0,"seed":9,"twistiness":0.2}`); code != 200 {
		t.Errorf("got %d", code)
	}
	if code, _ = post(t, api.URL+"/v1/plan", `{"mode":"nope","start":[0,0]}`); code != 400 {
		t.Errorf("bad mode: got %d, want 400", code)
	}
}

func TestLoopAllFail(t *testing.T) {
	var calls atomic.Int32
	all := map[float64]bool{}
	for h := 0.0; h < 360; h += 45 {
		all[h] = true
	}
	gh := fakeGH(t, all, &calls)
	defer gh.Close()
	api := testServer(gh)
	defer api.Close()
	code, res := post(t, api.URL+"/v1/plan", `{"mode":"loop","start":[-117.16,32.72],"distance_m":150000}`)
	if code != 422 || !strings.Contains(res["error"].(string), "no loop") {
		t.Errorf("got %d %v, want 422 no loop", code, res["error"])
	}
}

func TestHealth(t *testing.T) {
	var calls atomic.Int32
	gh := fakeGH(t, nil, &calls)
	api := testServer(gh)
	defer api.Close()
	resp, _ := http.Get(api.URL + "/v1/health")
	if resp.StatusCode != 200 {
		t.Errorf("health up: %d", resp.StatusCode)
	}
	gh.Close()
	resp, _ = http.Get(api.URL + "/v1/health")
	if resp.StatusCode != 503 {
		t.Errorf("health with GraphHopper down: %d, want 503", resp.StatusCode)
	}
}

func TestTestPage(t *testing.T) {
	var calls atomic.Int32
	gh := fakeGH(t, nil, &calls)
	defer gh.Close()
	api := testServer(gh)
	defer api.Close()
	resp, err := http.Get(api.URL + "/v1/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "Plan loop") {
		t.Fatalf("test page: %d", resp.StatusCode)
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "connect-src 'self'") {
		t.Errorf("CSP must forbid third-party loads, got %q", csp)
	}
	// The page must not reference any other host.
	for _, bad := range []string{"http://", "https://", "//cdn", "googleapis"} {
		if strings.Contains(string(body), bad) {
			t.Errorf("test page references %q", bad)
		}
	}
}

func TestRepeatedKMFindsSpur(t *testing.T) {
	// The Palomar fixture rides E Valley Pkwy / Valley Center Rd out and back for 4.2 km around a
	// round_trip turning point (km 29.8-34.0 and 34.7-38.9): ~8.4 km counting both passes. The
	// detector sees ~70 % of it (samples near the spur's tip don't match), plenty for a penalty.
	p := fixturePath(t, "gh_palomar.json")
	s := computeStats(p, cumulativeKM(p.Points.Coordinates))
	if s.RepeatedKM < 5 || s.RepeatedKM > 10 {
		t.Errorf("Palomar spur: repeated %.1f km, want 5-10", s.RepeatedKM)
	}
	// The demo ride reuses part of Pomerado Road out and back.
	d := fixturePath(t, "gh_demo.json")
	if r := computeStats(d, cumulativeKM(d.Points.Coordinates)).RepeatedKM; r < 10 {
		t.Errorf("demo ride repeats Pomerado: want > 10 km, got %.1f", r)
	}
	// A straight line never repeats.
	var line [][]float64
	for i := 0; i < 500; i++ {
		line = append(line, []float64{-117 + float64(i)*0.001, 33})
	}
	if r := repeatedKM(line, cumulativeKM(line)); r != 0 {
		t.Errorf("straight line repeated %.1f km", r)
	}
}

func TestAboutPage(t *testing.T) {
	var calls atomic.Int32
	gh := fakeGH(t, nil, &calls)
	defer gh.Close()
	api := testServer(gh)
	defer api.Close()
	for _, path := range []string{"/", "/v1/about"} {
		resp, err := http.Get(api.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || !strings.Contains(string(body), "/v1/img/ipad-loop.jpg") {
			t.Fatalf("%s: %d", path, resp.StatusCode)
		}
		// Every image the page references must be embedded.
		for _, m := range regexp.MustCompile(`/v1/img/[a-z0-9-]+\.jpg`).FindAllString(string(body), -1) {
			r, err := http.Get(api.URL + m)
			if err != nil {
				t.Fatal(err)
			}
			r.Body.Close()
			if r.StatusCode != 200 || r.Header.Get("Content-Type") != "image/jpeg" {
				t.Errorf("%s: %d %s", m, r.StatusCode, r.Header.Get("Content-Type"))
			}
		}
	}
}

func TestPrivacyPage(t *testing.T) {
	var calls atomic.Int32
	gh := fakeGH(t, nil, &calls)
	defer gh.Close()
	api := testServer(gh)
	defer api.Close()
	resp, err := http.Get(api.URL + "/v1/privacy")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "Serpentine — Privacy") {
		t.Fatalf("privacy page: %d", resp.StatusCode)
	}
	// Claims the page makes that the code must keep true.
	for _, claim := range []string{"24 hours", "developer.nlr.gov", "30 days", "pfh@phfactor.net"} {
		if !strings.Contains(string(body), claim) {
			t.Errorf("privacy page lost %q", claim)
		}
	}
	resp, err = http.Get(api.URL + "/v1/support")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "pfh@phfactor.net") {
		t.Errorf("support page must be served and carry a contact: %d", resp.StatusCode)
	}
	if planCacheTTL != 24*time.Hour {
		t.Errorf("page says plans are kept 24 h; planCacheTTL is %v", planCacheTTL)
	}
	if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "default-src 'none'") {
		t.Error("privacy page must carry the no-third-party CSP")
	}
}

func TestDurationRejectsBadRequests(t *testing.T) {
	var calls atomic.Int32
	gh := fakeGH(t, nil, &calls)
	defer gh.Close()
	api := testServer(gh)
	defer api.Close()
	for _, tc := range []struct{ name, body string }{
		{"point to point", `{"mode":"point_to_point","start":[-117.2,32.9],"end":[-116.8,33.0],"duration_s":7200}`},
		{"both budgets", `{"mode":"loop","start":[-116.868,33.042],"distance_m":100000,"duration_s":7200}`},
		{"too short", `{"mode":"loop","start":[-116.868,33.042],"duration_s":600}`},
		{"too long", `{"mode":"loop","start":[-116.868,33.042],"duration_s":40000}`},
	} {
		if code, _ := post(t, api.URL+"/v1/plan", tc.body); code != 400 {
			t.Errorf("%s: got %d, want 400", tc.name, code)
		}
	}
}

func TestDurationBudget(t *testing.T) {
	var calls atomic.Int32
	gh := fakeGH(t, nil, &calls)
	defer gh.Close()
	api := testServer(gh)
	defer api.Close()
	// The fake returns the same 341-minute loop whatever distance we ask for, so the correction can
	// never converge: the point is that it gives up rather than looping, and reports the overrun.
	code, res := post(t, api.URL+"/v1/plan", `{"mode":"loop","start":[-116.868,33.042],"duration_s":7200}`)
	if code != 200 {
		t.Fatalf("plan: %d", code)
	}
	b, ok := res["budget"].(map[string]any)
	if !ok {
		t.Fatal("no budget block on a duration_s plan")
	}
	if b["target_s"].(float64) != 7200 {
		t.Errorf("target_s = %v", b["target_s"])
	}
	if b["fits"].(bool) {
		t.Errorf("a 341-minute ride can't fit a 2-hour budget: %v", b)
	}
	if b["total_s"].(float64) != res["time_s"].(float64) {
		t.Errorf("total_s %v should be the ride time %v when there's no charging", b["total_s"], res["time_s"])
	}
	// Three plans at most (candidate fan-out is 16 + up to one rescale each).
	if n := calls.Load(); n > 3*17 {
		t.Errorf("%d routing calls: the budget correction isn't bounded", n)
	}
}

func TestDistancePlanHasNoBudget(t *testing.T) {
	var calls atomic.Int32
	gh := fakeGH(t, nil, &calls)
	defer gh.Close()
	api := testServer(gh)
	defer api.Close()
	code, res := post(t, api.URL+"/v1/plan", `{"mode":"loop","start":[-116.868,33.042],"distance_m":150000}`)
	if code != 200 {
		t.Fatalf("plan: %d", code)
	}
	if _, ok := res["budget"]; ok {
		t.Error("distance plans shouldn't carry a budget block")
	}
}

func TestDetourBudget(t *testing.T) {
	var calls atomic.Int32
	gh := fakeGH(t, nil, &calls)
	defer gh.Close()
	api := testServer(gh)
	defer api.Close()

	// Without max_extra_s: one route, no detour block, as before.
	code, res := post(t, api.URL+"/v1/plan", `{"mode":"point_to_point","start":[-117.2,32.9],"end":[-116.8,33.0]}`)
	if code != 200 {
		t.Fatalf("plain point_to_point: %d", code)
	}
	if _, ok := res["detour"]; ok {
		t.Error("no budget asked for, so no detour block")
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("%d routing calls for a plain A→B, want 1", n)
	}

	// With a budget: a baseline route plus the twistiness attempt, and the cost reported.
	calls.Store(0)
	code, res = post(t, api.URL+"/v1/plan",
		`{"mode":"point_to_point","start":[-117.2,32.9],"end":[-116.8,33.0],"twistiness":0.9,"max_extra_s":900}`)
	if code != 200 {
		t.Fatalf("budgeted point_to_point: %d", code)
	}
	d, ok := res["detour"].(map[string]any)
	if !ok {
		t.Fatal("no detour block")
	}
	if d["max_extra_s"].(float64) != 900 {
		t.Errorf("max_extra_s = %v", d["max_extra_s"])
	}
	// The fake returns the same path every time, so the full twistiness fits and costs nothing.
	if d["extra_s"].(float64) != 0 || d["twistiness"].(float64) != 0.9 || !d["fits"].(bool) {
		t.Errorf("detour = %v", d)
	}
	if n := int(calls.Load()); n > len(detourSteps)+1 {
		t.Errorf("%d routing calls: the step-down isn't bounded", n)
	}

	// A detour budget is meaningless without a destination, and out of range is a 400.
	if code, res := post(t, api.URL+"/v1/plan",
		`{"mode":"loop","start":[-116.868,33.042],"distance_m":100000,"max_extra_s":600}`); code != 200 {
		t.Errorf("loop with max_extra_s: %d", code)
	} else if _, ok := res["detour"]; ok {
		t.Error("max_extra_s should be dropped for loops, not honoured")
	}
	if code, _ := post(t, api.URL+"/v1/plan",
		`{"mode":"point_to_point","start":[-117.2,32.9],"end":[-116.8,33.0],"max_extra_s":9999}`); code != 400 {
		t.Errorf("max_extra_s 9999: %d, want 400", code)
	}
}

// The catalog is hand-written data the app depends on, so check its shape and that nothing claims
// more certainty than it has (ADR-020).
func TestVehicleCatalog(t *testing.T) {
	var calls atomic.Int32
	gh := fakeGH(t, nil, &calls)
	defer gh.Close()
	api := testServer(gh)
	defer api.Close()
	resp, err := http.Get(api.URL + "/v1/vehicles")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("catalog: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	var cat struct {
		Version  string `json:"version"`
		Vehicles []struct {
			ID         string   `json:"id"`
			Kind       string   `json:"kind"`
			Source     string   `json:"source"`
			Confidence string   `json:"confidence"`
			Connectors []string `json:"connectors"`
			ACkW       *float64 `json:"ac_kw"`
			DCkW       *float64 `json:"dc_kw"`
			TankL      *float64 `json:"tank_l"`
		} `json:"vehicles"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&cat); err != nil {
		t.Fatalf("catalog isn't valid JSON: %v", err)
	}
	if cat.Version == "" || len(cat.Vehicles) < 10 {
		t.Fatalf("catalog looks empty: version %q, %d vehicles", cat.Version, len(cat.Vehicles))
	}
	seen := map[string]bool{}
	ok := map[string]bool{"verified": true, "derived": true, "press": true, "unverified": true}
	// NREL takes these and rejects anything else, NACS included (ADR-020).
	valid := map[string]bool{"J1772": true, "J1772COMBO": true, "CHADEMO": true, "TESLA": true}
	for _, v := range cat.Vehicles {
		if v.ID == "" || seen[v.ID] {
			t.Errorf("missing or duplicate id %q", v.ID)
		}
		seen[v.ID] = true
		if v.Source == "" || !ok[v.Confidence] {
			t.Errorf("%s: source %q, confidence %q", v.ID, v.Source, v.Confidence)
		}
		switch v.Kind {
		case "electric":
			if len(v.Connectors) == 0 || v.DCkW == nil || v.ACkW == nil {
				t.Errorf("%s: an electric bike needs connectors, an AC rating and a DC rating (0 for none)", v.ID)
			}
			// A bike that can't take DC has to have somewhere to charge (ADR-020: the LiveWire ONE
			// is the reverse case — DC only, because its AC rate is a rounding error).
			if v.DCkW != nil && *v.DCkW == 0 && v.ACkW != nil && *v.ACkW == 0 {
				t.Errorf("%s: no AC and no DC charging at all", v.ID)
			}
			for _, c := range v.Connectors {
				if !valid[c] {
					t.Errorf("%s: connector %q isn't an NREL code", v.ID, c)
				}
			}
		case "combustion":
			if v.TankL == nil {
				t.Errorf("%s: a petrol bike needs a tank size", v.ID)
			}
		default:
			t.Errorf("%s: kind %q", v.ID, v.Kind)
		}
	}
}
