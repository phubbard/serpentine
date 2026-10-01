package main

import (
	"context"
	"math"
	"sync"
)

// A→B with a detour budget. A rider going somewhere has a deadline, so "curvy" has a price cap:
// max_extra_s is how much longer than the quickest way they'll accept. We route the quick way first
// as the baseline, then try the requested twistiness and, if that overruns, progressively tamer
// models until one fits. Each route is one GraphHopper call (~100-300 ms), so the whole thing costs
// at most detourSteps+1 calls.
//
// Stepping down beats searching for a target time: twistiness maps to a priority multiplier, not to
// minutes, and the relationship is different on every pair of points.
var detourSteps = []float64{1.0, 0.66, 0.33}

// detourInfo tells the rider what the curves cost them.
type detourInfo struct {
	FastestS   float64 `json:"fastest_s"`   // the quick way, same avoid rules
	ExtraS     float64 `json:"extra_s"`     // what this route adds over it
	MaxExtraS  float64 `json:"max_extra_s"` // what they allowed
	Twistiness float64 `json:"twistiness"`  // what we could afford, <= the request's
	Fits       bool    `json:"fits"`        // false: even the quick way is all there is
}

// planAtoB routes point_to_point. Without max_extra_s it is a single route at the requested
// twistiness, exactly as before.
func (s *server) planAtoB(ctx context.Context, r *planRequest, cm *customModel) (*ghPath, *detourInfo, error) {
	points := r.points()
	if r.Style == "efficient" {
		// Lowest energy, not lowest time (ADR-034): the ride to a charger on a nearly flat battery.
		path, err := s.planEfficient(ctx, r, points)
		return path, nil, err
	}
	if r.Style == "direct" {
		// The errand ride (ADR-031): GraphHopper's own fastest route on the direct profile, which is
		// the only place freeways aren't punished. No detour budget — the whole point is not detouring.
		path, err := s.gh.route(ctx, ghRequest{Points: points, Profile: "direct"})
		return path, nil, err
	}
	if r.MaxExtraS == nil {
		path, err := s.gh.route(ctx, ghRequest{Points: points, CustomModel: cm})
		return path, nil, err
	}
	// Baseline: no curvature preference, but the same surfaces and ferries the rider excluded.
	fastest, err := s.gh.route(ctx, ghRequest{Points: points, CustomModel: customModelFor(0, r.Avoid)})
	if err != nil {
		return nil, nil, err
	}
	fastestS := float64(fastest.Time) / 1000
	budgetS := fastestS + *r.MaxExtraS

	best, bestTwist := fastest, 0.0
	for _, step := range detourSteps {
		twist := *r.Twistiness * step
		if twist <= 0 {
			continue
		}
		path, err := s.gh.route(ctx, ghRequest{Points: points, CustomModel: customModelFor(twist, r.Avoid)})
		if err != nil {
			return nil, nil, err
		}
		if float64(path.Time)/1000 <= budgetS {
			best, bestTwist = path, twist
			break // the first that fits is the twistiest that fits
		}
	}
	return best, &detourInfo{
		FastestS:   math.Round(fastestS),
		ExtraS:     math.Round(float64(best.Time)/1000 - fastestS),
		MaxExtraS:  *r.MaxExtraS,
		Twistiness: math.Round(bestTwist*100) / 100,
		Fits:       bestTwist > 0,
	}, nil
}

// The errand profile is the only base without freeway penalties baked in, so an efficient route
// starts there and tightens (ADR-009 allows only tightening under LM). Each model below is a guess
// at what saves energy; which one actually wins is decided by the energy model, not by this list.
//
// Measured San Diego → Ramona 2026-10-01: fastest is 57.0 km / 971 m climb (~6.1 kWh), while pushing
// off the freeway gives 60.2 km / 1048 m (~4.8 kWh). Longer, higher, and a fifth cheaper — because
// Wh/km at 50 km/h is far below Wh/km at 100. Distance is not the metric; energy is.
func fptr(x float64) *float64 { return &x }

// Built once, not per request: planEfficient and the reach list have to be able to point at the
// *same* model, or the percentage the list promises isn't the one the route is scored against.
// Treat as read-only.
var efficientCandidates = []*customModel{
	nil, // the plain fastest route: on a short flat hop it is often also the cheapest
	{
		DistanceInfluence: fptr(90),
		Priority: []cmRule{
			{If: "road_class == MOTORWAY", MultiplyBy: "0.4"},
			{ElseIf: "road_class == TRUNK", MultiplyBy: "0.7"},
			{If: "max_speed > 90", MultiplyBy: "0.6"},
			{ElseIf: "max_speed > 70", MultiplyBy: "0.85"},
			{If: "average_slope > 6", MultiplyBy: "0.5"},
			{ElseIf: "average_slope > 3", MultiplyBy: "0.75"},
		},
	},
	{
		DistanceInfluence: fptr(160),
		Priority: []cmRule{
			{If: "road_class == MOTORWAY", MultiplyBy: "0.15"},
			{ElseIf: "road_class == TRUNK", MultiplyBy: "0.4"},
			{If: "max_speed > 90", MultiplyBy: "0.35"},
			{ElseIf: "max_speed > 70", MultiplyBy: "0.7"},
			{If: "average_slope > 6", MultiplyBy: "0.25"},
			{ElseIf: "average_slope > 3", MultiplyBy: "0.6"},
		},
	},
}

func efficientModels() []*customModel { return efficientCandidates }

// reachModel is the single model used to price every charger in the list (ADR-034). It is one of
// the candidates planEfficient scores, so the route a rider gets after tapping is never worse than
// the percentage the list promised them.
func reachModel() *customModel { return efficientCandidates[1] }

// planEfficient routes the candidates in parallel and returns the one our energy model says costs
// least. Three GraphHopper calls, which is what makes this affordable per tapped charger but not
// per row of a list.
func (s *server) planEfficient(ctx context.Context, r *planRequest, points [][2]float64) (*ghPath, error) {
	models := efficientModels()
	paths := make([]*ghPath, len(models))
	errs := make([]error, len(models))

	var wg sync.WaitGroup
	for i, cm := range models {
		wg.Add(1)
		go func(i int, cm *customModel) {
			defer wg.Done()
			paths[i], errs[i] = s.gh.route(ctx, ghRequest{Points: points, Profile: "direct", CustomModel: cm})
		}(i, cm)
	}
	wg.Wait()

	best, bestKWh, firstErr := (*ghPath)(nil), math.Inf(1), error(nil)
	for i, p := range paths {
		if errs[i] != nil {
			if firstErr == nil {
				firstErr = errs[i]
			}
			continue
		}
		if kwh := routeKWh(r.Vehicle, p); kwh < bestKWh {
			best, bestKWh = p, kwh
		}
	}
	if best == nil {
		return nil, firstErr
	}
	return best, nil
}
