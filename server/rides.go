package main

import (
	"encoding/json"
	"fmt"
	"math"
)

// Demo rides (ADR-043): a short catalogue of roads worth knowing, served at /v1/rides.
//
// It exists for the first screen. A rider who has just installed the app, before granting location
// or typing anything, is looking at an empty form and a disabled button — and the product page
// names US 129 and US 550 as if you could ask for them. One tap on a demo ride plans the real thing.
//
// An entry is a *road*, not a town. Picking "Deals Gap" and planning a loop nearby would be worse
// than having no feature, because the app would have promised that road by name and not ridden it.
// So each entry carries both ends and plans as an out-and-back, which is the mode that guarantees
// the road itself.
type demoRide struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Road   string `json:"road"`
	Region string `json:"region"`
	// Country is here from the first entry, while every entry is "US", because the graph is
	// US-only today and will not always be. An entry added before its graph exists cannot plan.
	Country string     `json:"country"`
	Blurb   string     `json:"blurb"`
	Mode    string     `json:"mode"`
	Start   [2]float64 `json:"start"`
	// End is the far end of a one-way road, Turnaround the far end of a there-and-back, and
	// DistanceM the size of a loop. Exactly one applies, decided by Mode.
	End        *[2]float64 `json:"end,omitempty"`
	Turnaround *[2]float64 `json:"turnaround,omitempty"`
	DistanceM  float64     `json:"distance_m,omitempty"`
}

type demoRideCatalog struct {
	Version string     `json:"version"`
	Note    string     `json:"note"`
	Rides   []demoRide `json:"rides"`
}

func parseRideCatalog(raw []byte) (demoRideCatalog, error) {
	var c demoRideCatalog
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, err
	}
	if len(c.Rides) == 0 {
		return c, fmt.Errorf("rides: catalogue is empty")
	}
	seen := map[string]bool{}
	for _, r := range c.Rides {
		switch {
		case r.ID == "":
			return c, fmt.Errorf("rides: %q has no id", r.Name)
		case seen[r.ID]:
			return c, fmt.Errorf("rides: duplicate id %q", r.ID)
		case r.Name == "" || r.Road == "" || r.Blurb == "":
			return c, fmt.Errorf("rides: %q is missing a name, road or blurb", r.ID)
		case r.Country == "":
			return c, fmt.Errorf("rides: %q has no country", r.ID)
		case !onEarth(r.Start):
			return c, fmt.Errorf("rides: %q start is not a lon,lat", r.ID)
		}
		seen[r.ID] = true

		switch r.Mode {
		case "point_to_point":
			// The mode most of these need. A famous road is often the *only* road — the Dragon,
			// Palomar South Grade — and out_and_back routes the return through a corridor penalty
			// that forbids retracing, so it refuses the very roads this catalogue exists for.
			if r.End == nil || !onEarth(*r.End) {
				return c, fmt.Errorf("rides: %q is point-to-point with no end", r.ID)
			}
			if km := haversineKM(r.Start[:], r.End[:]); km < 1 || km > 200 {
				return c, fmt.Errorf("rides: %q end is %.0f km from the start", r.ID, km)
			}
		case "out_and_back":
			if r.Turnaround == nil || !onEarth(*r.Turnaround) {
				return c, fmt.Errorf("rides: %q is an out-and-back with no turnaround", r.ID)
			}
			// A turnaround on top of the start is a ride to nowhere; one across the country is a
			// typo. Both have to fail here rather than at the planner.
			if km := haversineKM(r.Start[:], r.Turnaround[:]); km < 1 || km > 200 {
				return c, fmt.Errorf("rides: %q turnaround is %.0f km from the start", r.ID, km)
			}
		case "loop":
			if r.DistanceM < 20_000 || r.DistanceM > 500_000 {
				return c, fmt.Errorf("rides: %q loop distance %.0f m is outside what /v1/plan takes", r.ID, r.DistanceM)
			}
		default:
			return c, fmt.Errorf("rides: %q has mode %q, which is not a mode the app can plan", r.ID, r.Mode)
		}
	}
	return c, nil
}

// onEarth catches the classic: coordinates written lat,lon instead of lon,lat. Everything else in
// this server is [lon, lat] and a swapped pair usually still parses as a valid point somewhere
// useless, so the check is deliberately tighter than ±180/±90.
func onEarth(p [2]float64) bool {
	return math.Abs(p[0]) <= 180 && math.Abs(p[1]) <= 90 && !(p[0] == 0 && p[1] == 0)
}
