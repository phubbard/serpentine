package main

import (
	"context"
	"math"
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
	points := [][2]float64{*r.Start, *r.End}
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
