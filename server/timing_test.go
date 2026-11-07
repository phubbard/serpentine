package main

import (
	"math"
	"testing"
)

// straightPath builds a path of n+1 vertices running due east at latitude 33, so the segments have
// real length and cumulativeKM works, with one road_class/urban_density pair covering the whole
// thing. timeS is the free-flow time GraphHopper would have reported.
func straightPath(n int, roadClass, density string, timeS float64) *ghPath {
	p := &ghPath{Time: int64(timeS * 1000), Details: map[string][]ghDetail{}}
	for i := 0; i <= n; i++ {
		p.Points.Coordinates = append(p.Points.Coordinates, []float64{-117.0 + float64(i)*0.01, 33.0, 100})
	}
	p.Details["road_class"] = []ghDetail{{From: 0, To: n, Str: roadClass}}
	p.Details["urban_density"] = []ghDetail{{From: 0, To: n, Str: density}}
	return p
}

func pathKM(p *ghPath) float64 {
	cum := cumulativeKM(p.Points.Coordinates)
	return cum[len(cum)-1]
}

// The allowance has to be exactly rate × distance, so a measured ride can be used to solve for the
// rate rather than only to say the answer looked wrong.
func TestRideDelayRates(t *testing.T) {
	cases := []struct {
		class, density string
		wantSPerKM     float64
	}{
		{"primary", "city", 36},
		{"primary", "residential", 24},
		{"primary", "rural", 5},
		{"secondary", "city", 36},
		{"trunk", "city", 36}, // at-grade expressway in the US, not a freeway
		{"tertiary", "city", 36},
		{"residential", "city", 10},
		{"service", "residential", 8},
		{"track", "rural", 3},
		{"motorway", "city", 0}, // grade-separated: nothing at grade to wait for
		{"motorway_link", "city", 0},
		{"primary", "", 5}, // missing density falls back to rural, never to town
	}
	for _, c := range cases {
		p := straightPath(20, c.class, c.density, 600)
		cum := cumulativeKM(p.Points.Coordinates)
		km := cum[len(cum)-1]
		got, want := rideDelayS(p, cum), km*c.wantSPerKM
		if math.Abs(got-want) > 0.5 {
			t.Errorf("%s/%s over %.1f km: delay %.1f s, want %.1f (%.0f s/km)",
				c.class, c.density, km, got, want, c.wantSPerKM)
		}
	}
}

// A curvy arterial pays both allowances. This is the ADR-018 term: the posted limit isn't
// achievable through a tight sequence, on top of whatever the junctions cost.
func TestRideDelayCurvyAddsToInterruptions(t *testing.T) {
	plain := straightPath(20, "secondary", "rural", 600)
	curvy := straightPath(20, "secondary", "rural", 600)
	curvy.Details["curvature"] = []ghDetail{{From: 0, To: 20, Num: 0.80, IsNum: true}}
	km := pathKM(plain)

	base, with := rideDelayS(plain, cumulativeKM(plain.Points.Coordinates)), rideDelayS(curvy, cumulativeKM(curvy.Points.Coordinates))
	if got, want := with-base, km*delayCurvySPerKM; math.Abs(got-want) > 0.5 {
		t.Errorf("curvy surcharge %.1f s over %.1f km, want %.1f", got, want, km*delayCurvySPerKM)
	}

	// Straight enough not to count must cost nothing extra, at the same threshold computeStats uses.
	straightish := straightPath(20, "secondary", "rural", 600)
	straightish.Details["curvature"] = []ghDetail{{From: 0, To: 20, Num: curvyBelow, IsNum: true}}
	if got := rideDelayS(straightish, cumulativeKM(straightish.Points.Coordinates)); math.Abs(got-base) > 0.5 {
		t.Errorf("curvature exactly at the %.2f threshold cost %.1f s, want the plain %.1f", curvyBelow, got, base)
	}

	// ...and a curvy freeway is still a freeway.
	fwy := straightPath(20, "motorway", "city", 600)
	fwy.Details["curvature"] = []ghDetail{{From: 0, To: 20, Num: 0.80, IsNum: true}}
	if got := rideDelayS(fwy, cumulativeKM(fwy.Points.Coordinates)); got != 0 {
		t.Errorf("grade-separated road charged %.1f s of delay, want 0", got)
	}
}

// The whole point: a signalised urban arterial must stop reading like an open road. Before ADR-045
// a loop like this came back at the posted limit.
func TestUrbanArterialIsNotFreeFlow(t *testing.T) {
	p := straightPath(40, "primary", "city", 0)
	km := pathKM(p)
	p.Time = int64(km / 72 * 3600 * 1000) // GraphHopper's free-flow: 72 km/h (45 mph) through town

	cum := cumulativeKM(p.Points.Coordinates)
	freeMPH := km / 1.60934 / (float64(p.Time) / 1000 / 3600)
	gotMPH := km / 1.60934 / (rideTimeS(p, cum) / 3600)
	if freeMPH < 43 || freeMPH > 46 {
		t.Fatalf("fixture setup: free-flow %.1f mph, wanted ~45", freeMPH)
	}
	// Beeline quoted 26.1 mph for this corner of San Diego (2026-11-06). Anything still in the
	// thirties means the correction isn't reaching the roads it was built for.
	if gotMPH > 30 {
		t.Errorf("city arterial corrected to %.1f mph (from %.1f free-flow); wanted something near the measured 26", gotMPH, freeMPH)
	}
}

// Real GraphHopper output, with every detail populated the way axiom returns it.
func TestRideTimeOnFixtures(t *testing.T) {
	for _, name := range []string{"gh_palomar.json", "gh_demo.json", "gh_loop_track.json"} {
		p := fixturePath(t, name)
		cum := cumulativeKM(p.Points.Coordinates)
		free := float64(p.Time) / 1000
		got := rideTimeS(p, cum)
		if got <= free {
			t.Errorf("%s: corrected %.0f s is not slower than free-flow %.0f s", name, got, free)
		}
		// These are mostly-rural rides. A correction outside this band means a rate went in wrong,
		// not that the route changed: under 5 % and it isn't doing its job, over 40 % and it has
		// stopped being an allowance.
		if frac := (got - free) / free; frac < 0.05 || frac > 0.40 {
			t.Errorf("%s: allowance is %.1f %% of free-flow, outside the 5–40 %% band", name, 100*frac)
		}
	}
}

// buildResult has to hand the rider a turn list that sums to the total it quoted them, or the
// screen contradicts itself.
func TestInstructionTimesSumToCorrectedTotal(t *testing.T) {
	p := fixturePath(t, "gh_palomar.json")
	res := buildResult("t", "loop", "", p, nil, nil, -1, nil, nil, nil)

	sum := 0.0
	for _, i := range res.Instructions {
		sum += i.TimeS
	}
	if res.FreeFlowS >= res.TimeS {
		t.Errorf("free-flow %.0f s should be under the corrected %.0f s", res.FreeFlowS, res.TimeS)
	}
	if math.Abs(res.TimeS-(res.FreeFlowS+res.DelayS)) > 1 {
		t.Errorf("time_s %.0f != free_flow %.0f + delay %.0f", res.TimeS, res.FreeFlowS, res.DelayS)
	}
	if rel := math.Abs(sum-res.TimeS) / res.TimeS; rel > 0.02 {
		t.Errorf("turn list sums to %.0f s against a quoted %.0f s (%.1f %% apart)", sum, res.TimeS, 100*rel)
	}
}

// A degenerate path must not panic or invent time.
func TestRideDelayDegenerate(t *testing.T) {
	for _, p := range []*ghPath{
		{},
		{Points: struct {
			Coordinates [][]float64 `json:"coordinates"`
		}{Coordinates: [][]float64{{-117, 33, 0}}}},
	} {
		cum := cumulativeKM(p.Points.Coordinates)
		if got := rideDelayS(p, cum); got != 0 {
			t.Errorf("delay on a %d-vertex path was %.1f s, want 0", len(p.Points.Coordinates), got)
		}
	}
}

// Charging has to be spent against real minutes, not free-flow ones: a budget that counts a
// 90-minute charge stop honestly and then under-counts the riding is still a broken promise.
// Uses the NREL fixture, so it needs no key — charging is refused outright without one, which is
// why this is a unit test and not a live call.
func TestChargingSpendsCorrectedRideTime(t *testing.T) {
	p := fixturePath(t, "gh_palomar.json")
	stations, err := parseNREL(loadFixture(t, "nrel_palomar.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, startSoc := range []float64{1.0, 0.5} {
		o := opts(startSoc, 0.15, 0.9)
		res := buildResult("t", "loop", "", p, nil, nil, -1, stations, &o, &bike)
		if res.Energy == nil {
			t.Fatalf("soc %.1f: no energy summary", startSoc)
		}
		got := res.Energy.TotalTimeS
		if got < res.TimeS-1 {
			t.Errorf("soc %.1f: total %.0f s is under the ride's own %.0f s", startSoc, got, res.TimeS)
		}
		// The decisive one: with nothing to charge, the total *is* the ride, so if free-flow had
		// been handed to planCharging this would come back short by the whole allowance.
		if res.Energy.Stops == 0 && math.Abs(got-res.TimeS) > 1 {
			t.Errorf("soc %.1f: no stops, so total %.0f s should be the corrected ride time %.0f s (free-flow was %.0f)",
				startSoc, got, res.TimeS, res.FreeFlowS)
		}
		if got <= res.FreeFlowS {
			t.Errorf("soc %.1f: total %.0f s is no more than free-flow %.0f s", startSoc, got, res.FreeFlowS)
		}
	}
}
