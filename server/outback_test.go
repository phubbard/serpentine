package main

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDestination(t *testing.T) {
	start := [2]float64{-116.868, 33.042}
	for _, b := range []float64{0, 90, 225} {
		p := destination(start, b, 50)
		if d := haversineKM(start[:], p[:]); math.Abs(d-50) > 0.01 {
			t.Errorf("bearing %v: %.3f km, want 50", b, d)
		}
	}
	if p := destination(start, 0, 50); p[1] <= start[1] || math.Abs(p[0]-start[0]) > 1e-9 {
		t.Errorf("bearing 0 should go due north, got %v", p)
	}
}

func TestMergePaths(t *testing.T) {
	out := fixturePath(t, "gh_demo.json")
	back := fixturePath(t, "gh_palomar.json")
	m := mergePaths(out, back)
	no, nb := len(out.Points.Coordinates), len(back.Points.Coordinates)
	if len(m.Points.Coordinates) != no+nb-1 {
		t.Fatalf("merged %d points, want %d", len(m.Points.Coordinates), no+nb-1)
	}
	if m.Distance != out.Distance+back.Distance {
		t.Error("distance must add")
	}
	last := m.Instructions[len(m.Instructions)-1]
	if last.Interval[1] > len(m.Points.Coordinates)-1 {
		t.Errorf("back instructions not offset: %v", last.Interval)
	}
	turns := 0
	for _, ins := range m.Instructions {
		if ins.Sign == 4 {
			turns++
		}
	}
	if turns != 1 {
		t.Errorf("want exactly one finish instruction after merge, got %d", turns)
	}
	for k, ds := range m.Details {
		if end := ds[len(ds)-1].To; end != len(m.Points.Coordinates)-1 {
			t.Errorf("detail %s ends at %d, want %d", k, end, len(m.Points.Coordinates)-1)
		}
	}
}

func TestCorridorModelIsLMSafe(t *testing.T) {
	p := fixturePath(t, "gh_demo.json")
	cm := corridorModel(customModelFor(0.5, []string{"unpaved"}), p.Points.Coordinates)
	if cm.Areas == nil || len(cm.Areas.Features) != 1 || cm.Areas.Features[0].ID != "outbound" {
		t.Fatal("want one 'outbound' area feature")
	}
	polys := cm.Areas.Features[0].Geometry.Coordinates
	if len(polys) < 100 {
		t.Errorf("225 km route at 0.5 km sampling should give hundreds of rectangles, got %d", len(polys))
	}
	for _, poly := range polys {
		ring := poly[0]
		if len(ring) != 5 || ring[0] != ring[4] {
			t.Fatalf("ring must be closed with 5 points: %v", ring)
		}
	}
	found := false
	for _, r := range cm.Priority {
		v, err := strconv.ParseFloat(r.MultiplyBy, 64)
		if err != nil || v < 0 || v > 1 {
			t.Errorf("multiply_by %q breaks LM", r.MultiplyBy)
		}
		found = found || r.If == "in_outbound"
	}
	if !found {
		t.Error("missing in_outbound rule")
	}
	b, _ := json.Marshal(cm)
	if !strings.Contains(string(b), `"type":"MultiPolygon"`) {
		t.Error("areas must serialise as GeoJSON MultiPolygon")
	}
	// A leg shorter than the cleared ends has no corridor: base model passes through.
	short := [][]float64{{-116.8, 33}, {-116.79, 33}}
	if corridorModel(nil, short) != nil {
		t.Error("short leg should return the base model")
	}
}

func TestSharedKM(t *testing.T) {
	p := fixturePath(t, "gh_demo.json").Points.Coordinates
	if s := sharedKM(p, p); s < 200 {
		t.Errorf("a route shares ~all of itself, got %.1f km", s)
	}
	far := make([][]float64, len(p))
	for i, c := range p {
		far[i] = []float64{c[0] + 1.0, c[1]} // ~93 km east, clear of the whole loop
	}
	if s := sharedKM(p, far); s != 0 {
		t.Errorf("a copy 93 km away shares nothing, got %.1f", s)
	}
}

func TestOutAndBackEndpoint(t *testing.T) {
	var calls atomic.Int32
	gh := fakeGH(t, nil, &calls)
	defer gh.Close()
	api := testServer(gh)
	defer api.Close()
	code, res := post(t, api.URL+"/v1/plan", `{"mode":"out_and_back","start":[-116.868,33.042],"distance_m":120000}`)
	if code != 200 {
		t.Fatalf("got %d %v", code, res["error"])
	}
	ob := res["out_and_back"].(map[string]any)
	if ob["candidates"].(float64) < 8 {
		t.Errorf("want >= 8 candidates, got %v", ob["candidates"])
	}
	roads := res["handoff"].(map[string]any)["waypoint_roads"].([]any)
	found := false
	for _, r := range roads {
		found = found || r.(string) == "Turnaround"
	}
	if !found {
		t.Errorf("turnaround must be a handoff waypoint: %v", roads)
	}
	code, _ = post(t, api.URL+"/v1/plan", `{"mode":"out_and_back","start":[-116.868,33.042],"turnaround":[-116.6019,33.0786]}`)
	if code != 200 {
		t.Errorf("explicit turnaround: got %d", code)
	}
}
