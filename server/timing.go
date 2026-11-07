package main

import "math"

// Ride-time correction (ADR-045).
//
// GraphHopper hands back free-flow time. Our base profile sets speed to
// min(car_average_speed, max_speed * 0.9) and the graph carries no turn costs, so a signalised
// arterial is modelled as an uninterrupted through-road: measured 2026-11-06, a University City
// loop claimed 42.1 mph along 1.75 mi of Clairemont Mesa Blvd and 44.9 mph along 2.0 mi of Sunset
// Cliffs Blvd. On a loop that was 78 % primary/secondary and 74 % built-up the whole plan came back
// 36.6 mph against Beeline's 26.1 mph for the same corner of San Diego — twenty minutes on a
// forty-minute ride.
//
// That matters most for a duration_s request, which is the one feature whose entire promise is the
// number that was wrong: ADR-018 plans to 97 % of the budget measured in GraphHopper seconds, so
// "I have 45 minutes" was buying a 65-minute ride. The correction therefore has to land *before*
// the budget loop, not on the way to the screen.
//
// The allowance is structural rather than a single fudge factor, so each term names a cause that
// can be checked against a ride log on its own. Two causes:
//
//  1. Interruptions — signals, stop signs, cross traffic, pedestrians, cars pulling out. The rate
//     is keyed on road class *and* urban density together, because the error lives where the
//     modelled speed is high and the interruptions are frequent: a city arterial signed 50 mph
//     that GraphHopper rides at 45. A residential street is already modelled near its real speed,
//     car_average_speed being low there, so charging it the arterial rate would count the same
//     slowness twice. Measured on the three fixtures, keying on density alone lands within 2–5
//     minutes of this over a two-hour ride — so the split is not what makes the totals right, it
//     is what keeps them right on a route the two models disagree about, such as a loop that
//     spends its urban miles on back streets rather than on boulevards. Grade-separated roads are
//     exempt: a freeway has none of this. (Freeway *congestion* is real and deliberately
//     unmodelled — we hold no traffic data, by design, and principle 6 says we aren't buying any.)
//  2. Corners — the posted limit isn't achievable through a tight sequence. ADR-018 already said
//     GraphHopper's speeds are optimistic on exactly the roads this app goes looking for.
//
// Both push the same way, and the direction is deliberate: a ride that comes in early is fine, one
// that runs over is a broken promise.
//
// THE NUMBERS BELOW ARE PROVISIONAL. They are desk estimates, not fitted values:
//
//   - A San Diego city arterial carries roughly 2 signals/km (Clairemont Mesa Blvd runs about one
//     every 0.25–0.4 mi). Mean control delay per signal, averaged over the cars that sail through
//     on green as well as the ones that stop, is around 18 s. 2 × 18 = 36 s/km.
//   - The same class of road in merely residential density gets about 1.3 signals/km → 24 s/km.
//   - Rural: the occasional junction or small town → 5 s/km.
//   - Local streets get far less, not because they're quick but because GraphHopper already rides
//     them slowly; the allowance covers stop signs it still doesn't know about.
//
// None of this has been fitted to a logged ride. docs/calibration.md says what to measure.
var delaySPerKM = map[string]map[string]float64{
	// Roads that carry signals: the arterials and collectors a plan spends its urban miles on.
	"arterial": {"city": 36, "residential": 24, "rural": 5},
	// Local streets: stop signs, parked cars, but a modelled speed that's already low.
	"local": {"city": 10, "residential": 8, "rural": 3},
}

const (
	// On top of the interruption allowance, over the curvy fraction only. Same threshold
	// routeStats uses, so the two numbers always describe the same metres of road.
	delayCurvySPerKM = 12.0

	// Below this curvature (beeline/length) an edge counts as curvy. Mirrors computeStats.
	curvyBelow = 0.94
)

// delayGroup buckets a road_class by whether it is grade-separated (no allowance at all), signal-
// carrying, or a local street. Trunk counts as signal-carrying: in the US it covers at-grade
// expressways with signals as often as it covers freeways.
func delayGroup(roadClass string) string {
	switch roadClass {
	case "motorway", "motorway_link":
		return "" // grade-separated: nothing at grade to wait for
	case "trunk", "trunk_link", "primary", "primary_link", "secondary", "secondary_link",
		"tertiary", "tertiary_link":
		return "arterial"
	default:
		return "local"
	}
}

// interruptionSPerKM is the allowance for one segment. An unknown or missing urban_density is
// treated as rural: it is always encoded in our graph (config.yml lists it), so a gap means no
// coverage at all rather than a quiet road, and guessing "town" there would inflate every rural
// ride on the strength of a missing field.
func interruptionSPerKM(roadClass, density string) float64 {
	g := delayGroup(roadClass)
	if g == "" {
		return 0
	}
	byDensity := delaySPerKM[g]
	if r, ok := byDensity[density]; ok {
		return r
	}
	return byDensity["rural"]
}

// vertexStrings spreads a string path detail over one slice entry per polyline vertex, the way
// energyProfile does for max_speed. Entry i describes the segment leaving vertex i.
func vertexStrings(p *ghPath, key string, n int) []string {
	out := make([]string, n)
	for _, d := range p.Details[key] {
		if d.IsNum {
			continue
		}
		for i := d.From; i < d.To && i < n; i++ {
			if i >= 0 {
				out[i] = d.Str
			}
		}
	}
	return out
}

// vertexCurvy marks the segments GraphHopper reports as curvy.
func vertexCurvy(p *ghPath, n int) []bool {
	out := make([]bool, n)
	for _, d := range p.Details["curvature"] {
		if !d.IsNum || d.Num >= curvyBelow {
			continue
		}
		for i := d.From; i < d.To && i < n; i++ {
			if i >= 0 {
				out[i] = true
			}
		}
	}
	return out
}

// rideDelayS is the seconds to add to GraphHopper's free-flow figure for this path. Walks the
// polyline segment by segment rather than multiplying the routeStats totals, because the rate
// depends on road class and density *together* and those marginal totals have already lost the
// pairing.
func rideDelayS(p *ghPath, cum []float64) float64 {
	n := len(cum)
	if n < 2 {
		return 0
	}
	density := vertexStrings(p, "urban_density", n)
	class := vertexStrings(p, "road_class", n)
	curvy := vertexCurvy(p, n)
	total := 0.0
	for i := 0; i < n-1; i++ {
		km := cum[i+1] - cum[i]
		if km <= 0 {
			continue
		}
		rate := interruptionSPerKM(class[i], density[i])
		// A curvy freeway is still a freeway, so this rides along with the interruption rate
		// rather than applying to grade-separated road.
		if curvy[i] && delayGroup(class[i]) != "" {
			rate += delayCurvySPerKM
		}
		total += km * rate
	}
	return total
}

// rideTimeS is how long this path really takes: GraphHopper's free-flow seconds plus the allowance.
// Everything that quotes a duration to a rider, or spends one against a budget, goes through here.
func rideTimeS(p *ghPath, cum []float64) float64 {
	return float64(p.Time)/1000 + rideDelayS(p, cum)
}

// rideTimeOf is rideTimeS when the caller has no cumulative-distance array to hand.
func rideTimeOf(p *ghPath) float64 {
	return rideTimeS(p, cumulativeKM(p.Points.Coordinates))
}

// scaleInstructionS spreads the same correction across the turn list, so the steps still sum to the
// total the rider was shown. Per-manoeuvre delay would need junction detail we don't fetch; a
// proportional share is honest about being an allowance and keeps the arithmetic consistent.
func scaleInstructionS(freeFlowS, correctedS float64) float64 {
	if freeFlowS <= 0 {
		return 1
	}
	return math.Max(1, correctedS/freeFlowS)
}
