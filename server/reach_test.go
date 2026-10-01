package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A station the reach search will accept: one Level 2 unit with J1772 ports.
func testStation(id int, name string, lon, lat float64, ports int, kw float64) nrelStation {
	s := nrelStation{ID: id, Name: name, Lon: lon, Lat: lat, Street: "1 Test St", City: "Testville",
		Network: "TestNet"}
	unit := struct {
		ChargingLevel string `json:"charging_level"`
		Connectors    map[string]struct {
			PowerKW   *float64 `json:"power_kw"`
			PortCount int      `json:"port_count"`
		} `json:"connectors"`
	}{ChargingLevel: "2"}
	unit.Connectors = map[string]struct {
		PowerKW   *float64 `json:"power_kw"`
		PortCount int      `json:"port_count"`
	}{"J1772": {PowerKW: &kw, PortCount: ports}}
	s.EVChargingUnits = append(s.EVChargingUnits, unit)
	return s
}

// terrain is what the fake GraphHopper should pretend the ride to a destination looks like: the
// road's posted speed and how much climbing it involves. Everything else follows from the geometry.
type terrain struct {
	speedKMH float64
	climbM   float64
}

// fakeGHTerrain answers every route request with a straight two-point path to the destination,
// dressed in the speed and climb this test wants for it. That is enough for the energy model, which
// reads exactly those two things plus distance.
func fakeGHTerrain(t *testing.T, byDest map[[2]float64]terrain) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/route" {
			http.NotFound(w, r)
			return
		}
		var req ghRequest
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("bad request to GraphHopper: %v", err)
		}
		if len(req.Points) != 2 {
			t.Errorf("reach should route point to point, got %d points", len(req.Points))
		}
		if req.Profile != "direct" {
			t.Errorf("efficient routing must start from the direct profile, got %q", req.Profile)
		}
		dest := req.Points[1]
		ter, ok := byDest[dest]
		if !ok {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"message":"nowhere to go"}`))
			return
		}
		src := req.Points[0]
		km := haversineKM([]float64{src[0], src[1]}, []float64{dest[0], dest[1]})
		resp := map[string]any{"paths": []map[string]any{{
			"distance": km * 1000,
			"time":     int64(km / ter.speedKMH * 3600 * 1000),
			"ascend":   ter.climbM,
			"points": map[string]any{"coordinates": [][]float64{
				{src[0], src[1], 0}, {dest[0], dest[1], ter.climbM},
			}},
			"details": map[string]any{"max_speed": [][]any{{0, 1, ter.speedKMH}}},
		}}}
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func reachTestServer(t *testing.T, gh *httptest.Server, stations []nrelStation) *server {
	t.Helper()
	store := newStationStore(t.TempDir() + "/stations.json")
	store.replace(stations, time.Now())
	return &server{
		gh: newGHClient(gh.URL), stations: store, nrel: newNRELClient("http://unused", "k"),
		cache: newPlanCache(10, time.Hour), reach: newReachCache(10, time.Minute),
		stats: newMetrics(), log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// The feature's whole reason to exist: the charger that costs least to reach is not the nearest one.
// Near-but-uphill-on-a-fast-road loses to far-but-flat-and-slow, because Wh/km climbs with speed and
// a climb is paid for in kWh.
func TestReachSortsByEnergyNotDistance(t *testing.T) {
	start := [2]float64{-117.0, 33.0}
	near := [2]float64{-117.0, 33.05} // ~5.6 km away, over a ridge, on a fast road
	far := [2]float64{-117.0, 33.09}  // ~10 km away, flat, slow road
	gh := fakeGHTerrain(t, map[[2]float64]terrain{
		near: {speedKMH: 110, climbM: 400},
		far:  {speedKMH: 45, climbM: 0},
	})
	defer gh.Close()
	s := reachTestServer(t, gh, []nrelStation{
		testStation(1, "Ridge Top", near[0], near[1], 2, 6.6),
		testStation(2, "Flat Valley", far[0], far[1], 8, 12),
	})

	soc := 0.5
	req := &reachRequest{Start: &start, Soc: &soc}
	if err := req.normalize(); err != nil {
		t.Fatal(err)
	}
	res, err := s.reachOptions(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Options) != 2 {
		t.Fatalf("expected both chargers priced, got %d", len(res.Options))
	}
	if res.Options[0].Name != "Flat Valley" {
		t.Errorf("sorted by distance, not energy: first is %q needing %.0f%%, second %q needing %.0f%%",
			res.Options[0].Name, res.Options[0].SocNeeded*100,
			res.Options[1].Name, res.Options[1].SocNeeded*100)
	}
	if res.Options[0].StraightKM <= res.Options[1].StraightKM {
		t.Error("this test is meant to have the cheaper charger be the further one")
	}
	for _, o := range res.Options {
		if o.SocNeeded <= 0 {
			t.Errorf("%s: a ride that costs nothing is a broken energy model", o.Name)
		}
	}
}

// A rider reads "needs 8%" and decides on it. Arrival has to be what's left, and anything over
// what's in the pack has to be marked rather than quietly listed.
func TestReachMarksWhatIsOutOfRange(t *testing.T) {
	start := [2]float64{-117.0, 33.0}
	close := [2]float64{-117.0, 33.02}
	stretch := [2]float64{-117.0, 33.30}
	gh := fakeGHTerrain(t, map[[2]float64]terrain{
		close:   {speedKMH: 50, climbM: 0},
		stretch: {speedKMH: 50, climbM: 0},
	})
	defer gh.Close()
	s := reachTestServer(t, gh, []nrelStation{
		testStation(1, "Corner Shop", close[0], close[1], 2, 6.6),
		testStation(2, "Far Town", stretch[0], stretch[1], 4, 6.6),
	})

	soc := 0.08 // ~1.2 kWh of a 15.1 kWh pack
	req := &reachRequest{Start: &start, Soc: &soc}
	if err := req.normalize(); err != nil {
		t.Fatal(err)
	}
	res, err := s.reachOptions(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Options) == 0 {
		t.Fatal("expected at least the near charger")
	}
	first := res.Options[0]
	if !first.Reachable {
		t.Errorf("%s needs %.0f%% of %.0f%% left and should be reachable", first.Name, first.SocNeeded*100, soc*100)
	}
	if got := soc - first.SocNeeded; math.Abs(first.SocArrival-got) > 0.02 {
		t.Errorf("arrival charge %.2f doesn't follow from %.2f - %.2f", first.SocArrival, soc, first.SocNeeded)
	}
	for _, o := range res.Options {
		if o.SocNeeded > soc && o.Reachable {
			t.Errorf("%s needs %.0f%% but only %.0f%% is left, and it is not marked", o.Name, o.SocNeeded*100, soc*100)
		}
	}
}

// When nothing is within what's left, an empty screen is the least useful answer. The search widens
// once so the rider learns where the nearest charger is — somewhere to be towed to — and everything
// it finds is marked honestly as out of range.
func TestReachWidensWhenNothingIsInRange(t *testing.T) {
	start := [2]float64{-117.0, 33.0}
	far := [2]float64{-117.0, 33.45} // ~50 km north; 5 % of the pack is about 13 km of best case
	gh := fakeGHTerrain(t, map[[2]float64]terrain{far: {speedKMH: 60, climbM: 100}})
	defer gh.Close()
	s := reachTestServer(t, gh, []nrelStation{testStation(1, "Another County", far[0], far[1], 4, 6.6)})

	soc := 0.05
	req := &reachRequest{Start: &start, Soc: &soc}
	if err := req.normalize(); err != nil {
		t.Fatal(err)
	}
	res, err := s.reachOptions(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Options) != 1 {
		t.Fatalf("a stranded rider should still be told where the nearest charger is, got %d options", len(res.Options))
	}
	if res.Options[0].Reachable {
		t.Error("a charger 50 km away on 5 % of the pack must not be offered as reachable")
	}
	if res.Warning == "" {
		t.Error("the list is useless without saying none of it is in range")
	}
}

// Beyond the widened radius there is nothing to say but the truth.
func TestReachSaysSoWhenThereIsNothingAtAll(t *testing.T) {
	start := [2]float64{-117.0, 33.0}
	gh := fakeGHTerrain(t, map[[2]float64]terrain{})
	defer gh.Close()
	s := reachTestServer(t, gh, nil)

	soc := 0.05
	req := &reachRequest{Start: &start, Soc: &soc}
	if err := req.normalize(); err != nil {
		t.Fatal(err)
	}
	res, err := s.reachOptions(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Options) != 0 || res.Warning == "" {
		t.Errorf("expected an empty list with a plain warning, got %d options and %q", len(res.Options), res.Warning)
	}
}

// The percentage in the list is a promise. The tapped route re-runs the candidates, so the model
// used to price the list has to be one of them — otherwise the route could come back needing more
// than the list said, which is the one direction that strands someone.
func TestListPriceIsOneOfTheRouteCandidates(t *testing.T) {
	want := reachModel()
	for _, cm := range efficientModels() {
		if cm == want {
			return
		}
	}
	t.Error("the model that prices the list is not among the routes the tap can choose, so a tapped route may cost more than advertised")
}

func TestEfficientModelsOnlyTighten(t *testing.T) {
	// ADR-009: under LM a per-request model may only multiply by <= 1. A rule above 1 is rejected by
	// GraphHopper at request time, which would break the feature in production and not in tests.
	for i, cm := range efficientModels() {
		if cm == nil {
			continue
		}
		for _, r := range cm.Priority {
			var v float64
			if _, err := jsonNumber(r.MultiplyBy, &v); err != nil {
				t.Errorf("model %d: multiply_by %q is not a number", i, r.MultiplyBy)
				continue
			}
			if v > 1 {
				t.Errorf("model %d: multiply_by %v is above 1; LM will reject it", i, v)
			}
		}
	}
}

func jsonNumber(s string, out *float64) (float64, error) {
	err := json.Unmarshal([]byte(s), out)
	return *out, err
}

func TestReachRequestValidation(t *testing.T) {
	start := [2]float64{-117.0, 33.0}
	soc := 0.5
	cases := []struct {
		name string
		req  reachRequest
		want string
	}{
		{"no start", reachRequest{Soc: &soc}, "start is required"},
		{"no soc", reachRequest{Start: &start}, "soc is required"},
		{"soc over one", reachRequest{Start: &start, Soc: ptrF(1.5)}, "soc must be between"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.req.normalize()
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("want error containing %q, got %v", c.want, err)
			}
		})
	}
	ok := reachRequest{Start: &start, Soc: &soc}
	if err := ok.normalize(); err != nil {
		t.Fatalf("a valid request was rejected: %v", err)
	}
	if ok.Vehicle == nil || ok.Vehicle.Name != "Zero SR/S" {
		t.Error("no vehicle should default to the SR/S, as plans do")
	}
}

func ptrF(f float64) *float64 { return &f }

// A wandering GPS fix must not re-route ten chargers every second.
func TestReachCacheKeyIgnoresJitter(t *testing.T) {
	a := [2]float64{-117.16110, 32.71570}
	b := [2]float64{-117.16115, 32.71572} // ~5 m away
	s1, s2 := 0.21, 0.22
	r1 := reachRequest{Start: &a, Soc: &s1}
	r2 := reachRequest{Start: &b, Soc: &s2}
	if err := r1.normalize(); err != nil {
		t.Fatal(err)
	}
	if err := r2.normalize(); err != nil {
		t.Fatal(err)
	}
	if r1.cacheKey() != r2.cacheKey() {
		t.Error("a few metres and one percent should hit the same cache entry")
	}
	far := [2]float64{-117.30, 32.71}
	r3 := reachRequest{Start: &far, Soc: &s1}
	if err := r3.normalize(); err != nil {
		t.Fatal(err)
	}
	if r1.cacheKey() == r3.cacheKey() {
		t.Error("a different town must not reuse the answer")
	}
}

// Downtown has hundreds of usable sites inside a kilometre. Ten of them in three blocks is not a
// choice, so the list takes the best site per patch instead of the ten literally nearest.
func TestReachSpreadsCandidatesAcrossPatches(t *testing.T) {
	var sites []charger
	// Six pedestals in one block, then two real alternatives further out.
	for i := 0; i < 6; i++ {
		sites = append(sites, charger{
			ID: string(rune('a' + i)), Name: "Garage", Ports: 2 + i, PowerKW: 6.6,
			LonLat: [2]float64{-117.0 + float64(i)*0.0005, 33.0}, OffRouteKM: float64(i) * 0.05,
		})
	}
	sites = append(sites,
		charger{ID: "x", Name: "Supermarket", Ports: 8, PowerKW: 12, LonLat: [2]float64{-117.03, 33.0}, OffRouteKM: 2.8},
		charger{ID: "y", Name: "Mall", Ports: 4, PowerKW: 7, LonLat: [2]float64{-117.06, 33.0}, OffRouteKM: 5.6})

	out := spreadSites(sites, reachSpreadKM, 10)
	if len(out) != 3 {
		t.Fatalf("expected one per patch (3), got %d: %v", len(out), names(out))
	}
	// Within the block it should keep the one a stranded rider would rather have: most ports.
	if out[0].Ports != 7 {
		t.Errorf("kept the nearest pedestal (%d ports) over the best one in the same block (7 ports)", out[0].Ports)
	}
	if out[1].Name != "Supermarket" || out[2].Name != "Mall" {
		t.Errorf("lost the genuinely different options: %v", names(out))
	}
}

func names(cs []charger) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Name
	}
	return out
}

// A two-kilometre hop costs a fraction of a percent. Rounding every short ride to "0 %" would throw
// away the ordering this endpoint exists to produce.
func TestShortHopsKeepTheirOrdering(t *testing.T) {
	v := srs()
	near := &charger{ID: "n", Name: "Near", LonLat: [2]float64{-117.0, 33.0}}
	far := &charger{ID: "f", Name: "Far", LonLat: [2]float64{-117.0, 33.01}}
	mk := func(km, climb float64) *ghPath {
		p := &ghPath{Distance: km * 1000, Ascend: climb}
		p.Points.Coordinates = [][]float64{{-117.0, 33.0, 0}, {-117.0, 33.0 + km/111.0, climb}}
		p.Details = map[string][]ghDetail{"max_speed": {{From: 0, To: 1, Num: 50, IsNum: true}}}
		return p
	}
	a := optionFor(&v, near, mk(0.6, 10), 0.18)
	b := optionFor(&v, far, mk(2.0, 10), 0.18)
	if a.SocNeeded == 0 || b.SocNeeded == 0 {
		t.Fatalf("short rides rounded away to zero: %v and %v", a.SocNeeded, b.SocNeeded)
	}
	if !(a.SocNeeded < b.SocNeeded) {
		t.Errorf("a 0.6 km ride (%v) should cost less than a 2 km one (%v)", a.SocNeeded, b.SocNeeded)
	}
}
