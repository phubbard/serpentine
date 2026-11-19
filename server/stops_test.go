package main

import (
	"strings"
	"testing"
)

// The errand ride's whole premise is that Apple Maps is told to stop at the pharmacy. Measured
// against production on 2026-11-18, it was not: a route through two errands handed Apple
// ["Balboa Avenue", "Genesee Avenue"] and would have driven straight past both. This is that bug.
func TestStopsBecomeHandoffWaypoints(t *testing.T) {
	p := fixturePath(t, "gh_palomar.json")
	coords := p.Points.Coordinates

	// A little off the polyline, as a real shop is: the handoff must carry the place the rider
	// asked for, not the nearest vertex we happened to route over.
	pharmacy := [2]float64{coords[500][0] + 0.002, coords[500][1] + 0.002}
	grocery := [2]float64{coords[1500][0] - 0.002, coords[1500][1] - 0.002}

	res := buildResult("t", "point_to_point", "curvy", p, nil, nil, -1,
		[][2]float64{pharmacy, grocery}, nil, nil, nil)

	for want, label := range map[[2]float64]string{pharmacy: "Stop: 1", grocery: "Stop: 2"} {
		found := false
		for i, w := range res.Handoff.Waypoints {
			if w == want {
				found = true
				if got := res.Handoff.WaypointRoads[i]; got != label {
					t.Errorf("stop at %v labelled %q, wanted %q", want, got, label)
				}
			}
		}
		if !found {
			t.Errorf("stop %v is not a handoff waypoint; Apple would drive past it. roads=%v",
				want, res.Handoff.WaypointRoads)
		}
	}

	// And in route order, so the rider is sent to the first errand first.
	iPharm, iGroc := -1, -1
	for i, w := range res.Handoff.Waypoints {
		switch w {
		case pharmacy:
			iPharm = i
		case grocery:
			iGroc = i
		}
	}
	if iPharm > iGroc {
		t.Errorf("stops out of route order: pharmacy at %d, grocery at %d", iPharm, iGroc)
	}
}

// Every stop spends one of Apple's ten slots. The rest hold the route to the roads we picked, so
// the total still has to fit or Apple silently drops the tail.
func TestStopsStayWithinAppleWaypointCap(t *testing.T) {
	p := fixturePath(t, "gh_palomar.json")
	coords := p.Points.Coordinates

	stops := make([][2]float64, 0, maxStops)
	for i := 1; i <= maxStops; i++ {
		c := coords[i*len(coords)/(maxStops+1)]
		stops = append(stops, [2]float64{c[0], c[1]})
	}
	res := buildResult("t", "point_to_point", "curvy", p, nil, nil, -1, stops, nil, nil, nil)

	if n := len(res.Handoff.Waypoints); n > maxHandoffWaypoints {
		t.Errorf("%d waypoints with %d stops, over the cap of %d", n, maxStops, maxHandoffWaypoints)
	}
	got := 0
	for _, l := range res.Handoff.WaypointRoads {
		if strings.HasPrefix(l, stopPrefix) {
			got++
		}
	}
	if got != maxStops {
		t.Errorf("%d of %d stops survived the trim; stops are forced and must never be dropped", got, maxStops)
	}
}

// A GPX on a Beeline counts the waypoints down, which is what makes the errand list useful on the
// bike (ADR-039 did this for charge stops).
func TestStopsAreGPXWaypoints(t *testing.T) {
	p := fixturePath(t, "gh_palomar.json")
	c := p.Points.Coordinates[800]
	res := buildResult("t", "point_to_point", "curvy", p, nil, nil, -1,
		[][2]float64{{c[0], c[1]}}, nil, nil, nil)

	for _, w := range rideStops(res) {
		if w.name == "Stop: 1" {
			return
		}
	}
	t.Error("the errand stop is missing from the GPX waypoints")
}

// Via and stops both insert points mid-route, for opposite reasons: via pins a resumed ride to
// roads it was already on, stops are errands to announce. A resumed ride samples 16 via points
// (ADR-035) and must not turn into 16 announced stops.
func TestViaIsNotAStop(t *testing.T) {
	p := fixturePath(t, "gh_palomar.json")
	res := buildResult("t", "point_to_point", "curvy", p, nil, nil, -1, nil, nil, nil, nil)
	for _, l := range res.Handoff.WaypointRoads {
		if strings.HasPrefix(l, stopPrefix) {
			t.Errorf("a plan with no stops produced %q", l)
		}
	}
}

func TestStopsValidation(t *testing.T) {
	start := [2]float64{-117.19, 32.82}
	end := [2]float64{-117.16, 32.79}
	stop := [2]float64{-117.135, 32.835}

	req := func(mut func(*planRequest)) *planRequest {
		r := &planRequest{Mode: "point_to_point", Start: &start, End: &end}
		mut(r)
		return r
	}

	if err := req(func(r *planRequest) {
		r.Stops = [][2]float64{stop}
		r.Via = [][2]float64{stop}
	}).normalize(); err == nil {
		t.Error("stops combined with via was accepted; the point order is ambiguous")
	}

	many := make([][2]float64, maxStops+1)
	for i := range many {
		many[i] = stop
	}
	if err := req(func(r *planRequest) { r.Stops = many }).normalize(); err == nil {
		t.Errorf("%d stops accepted, over the cap of %d", len(many), maxStops)
	}

	if err := req(func(r *planRequest) {
		r.Stops = [][2]float64{{-200, 95}}
	}).normalize(); err == nil {
		t.Error("a stop off the planet was accepted")
	}

	ok := req(func(r *planRequest) { r.Stops = [][2]float64{stop} })
	if err := ok.normalize(); err != nil {
		t.Fatalf("a plain two-errand request was rejected: %v", err)
	}
	if n := len(ok.points()); n != 3 {
		t.Errorf("start + 1 stop + end routed as %d points, wanted 3", n)
	}

	// A loop has no endpoints to put errands between; the field must not leak into other modes.
	loop := &planRequest{Mode: "loop", Start: &start, DistanceM: 40000, Stops: [][2]float64{stop}}
	if err := loop.normalize(); err != nil {
		t.Fatalf("loop rejected: %v", err)
	}
	if loop.Stops != nil {
		t.Error("stops survived normalize on a loop")
	}
}
