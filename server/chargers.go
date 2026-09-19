package main

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Charge-stop planning (ADR-014): place NREL sites along the route, walk the energy profile, and
// when the pack would drop below soc_min_arrival stop at the furthest charger still reachable.
// Detours are counted as 2 x off-route distance at the flat rate; the polyline itself is not
// re-routed through the charger — Apple routes to it because it becomes a handoff waypoint.

const (
	chargerCorridorMiles = 2.0
	siteMergeKM          = 0.15 // NREL lists each pedestal as a station; merge the ones at one site
	maxChargeStops       = 3

	// What goes back to the client (ADR-014 amendment): urban corridors have hundreds of sites.
	alternateBinKM   = 20.0 // best alternates per this much route
	alternatesPerBin = 2
	backupWindowKM   = 10.0 // backups are this close (along the route) to a stop
	backupsPerStop   = 2
)

type chargingOpts struct {
	Enabled       bool     `json:"enabled"`
	SocStart      *float64 `json:"soc_start,omitempty"`
	SocMinArrival *float64 `json:"soc_min_arrival,omitempty"`
	ChargeTo      *float64 `json:"charge_to,omitempty"`
}

type charger struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	LonLat      [2]float64 `json:"lonlat"`
	Address     string     `json:"address"`
	Network     string     `json:"network"`
	Ports       int        `json:"ports"`
	PowerKW     float64    `json:"power_kw"` // advertised; 0 = not published
	Connectors  []string   `json:"connectors"`
	Hours       string     `json:"hours,omitempty"`
	Pricing     string     `json:"pricing,omitempty"`
	KMFromStart float64    `json:"km_from_start"`
	OffRouteKM  float64    `json:"off_route_km"`
	SocArrival  float64    `json:"soc_arrival_est"`
	Stop        bool       `json:"stop"`
	Role        string     `json:"role"`                // "stop", "backup" (near a stop) or "alternate"
	DwellMin    float64    `json:"dwell_min,omitempty"` // stops only

	idx int
}

type energySummary struct {
	UsableKWh      float64 `json:"usable_kwh"`
	KWhEst         float64 `json:"kwh_est"`
	SocStart       float64 `json:"soc_start"`
	SocMinArrival  float64 `json:"soc_min_arrival"`
	ChargeTo       float64 `json:"charge_to"`
	SocEndEst      float64 `json:"soc_end_est"`
	Feasible       bool    `json:"feasible"`
	Stops          int     `json:"stops"`
	ChargeMin      float64 `json:"charge_min"`
	ChargersNearby int     `json:"chargers_nearby"` // all usable sites in the corridor; `chargers` is a selection
	TotalTimeS     float64 `json:"total_time_s"`    // riding + charging + detours
	Warning        string  `json:"warning,omitempty"`
}

// sitesFromStations merges co-located pedestals and drops restricted or non-usable ones.
func sitesFromStations(stations []nrelStation) []charger {
	var sites []charger
	for _, s := range stations {
		if s.Restricted {
			continue
		}
		ports, kw, conns := s.usablePorts()
		if ports == 0 {
			continue
		}
		pt := []float64{s.Lon, s.Lat}
		merged := false
		for i := range sites {
			if haversineKM(sites[i].LonLat[:], pt) < siteMergeKM {
				sites[i].Ports += ports
				sites[i].PowerKW = math.Max(sites[i].PowerKW, kw)
				for _, c := range conns {
					if !contains(sites[i].Connectors, c) {
						sites[i].Connectors = append(sites[i].Connectors, c)
					}
				}
				merged = true
				break
			}
		}
		if !merged {
			sites = append(sites, charger{
				ID: fmt.Sprintf("nrel:%d", s.ID), Name: s.Name, LonLat: [2]float64{s.Lon, s.Lat},
				Address: s.Street + ", " + s.City, Network: s.Network, Ports: ports, PowerKW: kw,
				Connectors: conns, Hours: s.Hours, Pricing: s.Pricing, OffRouteKM: s.OffRouteKM,
			})
		}
	}
	return sites
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// placeOnRoute sets each site's nearest polyline vertex, along-route km and off-route km.
func placeOnRoute(sites []charger, coords [][]float64, cum []float64) {
	for i := range sites {
		best, bestKM := 0, math.Inf(1)
		for j, c := range coords {
			if d := haversineKM(c, sites[i].LonLat[:]); d < bestKM {
				best, bestKM = j, d
			}
		}
		sites[i].idx = best
		sites[i].KMFromStart = round1(cum[best])
		sites[i].OffRouteKM = round1(bestKM)
	}
	sort.SliceStable(sites, func(a, b int) bool { return sites[a].idx < sites[b].idx })
}

// siteQuality ranks sites for backups and alternates: more ports, full power, open 24 h, close to
// the route; Tesla-only sites need the adapter so rank lower.
func siteQuality(c *charger) float64 {
	q := math.Min(float64(c.Ports), 8) * 0.5
	if c.PowerKW == 0 || c.PowerKW >= bikeChargeKW {
		q++ // unpublished power is usually a standard 6-7 kW pedestal
	}
	if strings.Contains(strings.ToLower(c.Hours), "24 hours") {
		q += 1.5
	}
	if !contains(c.Connectors, "J1772") {
		q--
	}
	return q - c.OffRouteKM
}

// selectChargers keeps every stop, the best backups around each stop, and the best alternates per
// stretch of route, in route order. sites must already be placed and planned.
func selectChargers(sites []charger) []charger {
	keep := map[int]string{}
	for i := range sites {
		if sites[i].Stop {
			keep[i] = "stop"
		}
	}
	best := func(cands []int, n int) []int {
		sort.SliceStable(cands, func(a, b int) bool { return siteQuality(&sites[cands[a]]) > siteQuality(&sites[cands[b]]) })
		if len(cands) > n {
			cands = cands[:n]
		}
		return cands
	}
	for i := range sites {
		if !sites[i].Stop {
			continue
		}
		var near []int
		for j := range sites {
			if _, taken := keep[j]; !taken && math.Abs(sites[j].KMFromStart-sites[i].KMFromStart) <= backupWindowKM {
				near = append(near, j)
			}
		}
		for _, j := range best(near, backupsPerStop) {
			keep[j] = "backup"
		}
	}
	bins := map[int][]int{}
	for j := range sites {
		if _, taken := keep[j]; !taken {
			b := int(sites[j].KMFromStart / alternateBinKM)
			bins[b] = append(bins[b], j)
		}
	}
	for _, cands := range bins {
		for _, j := range best(cands, alternatesPerBin) {
			keep[j] = "alternate"
		}
	}
	out := []charger{}
	for i := range sites {
		if role, ok := keep[i]; ok {
			c := sites[i]
			c.Role = role
			out = append(out, c)
		}
	}
	return out
}

func round1(x float64) float64 { return math.Round(x*10) / 10 }
func round2(x float64) float64 { return math.Round(x*100) / 100 }

// planCharging walks the route choosing stops. energy is cumulative kWh per vertex.
func planCharging(sites []charger, energy []float64, rideTimeS float64, o chargingOpts) energySummary {
	socStart, socMin, chargeTo := *o.SocStart, *o.SocMinArrival, *o.ChargeTo
	detourKWh := func(c *charger) float64 { return 2 * c.OffRouteKM * cityKWhPerKM * consumptionMargin }
	detourS := func(c *charger) float64 { return 2 * c.OffRouteKM / 40 * 3600 } // ~40 km/h in town

	sum := energySummary{UsableKWh: usableKWh, SocStart: socStart, SocMinArrival: socMin, ChargeTo: chargeTo}
	total := energy[len(energy)-1]
	sum.KWhEst = round2(total)

	// soc at vertex i given the last charge ended at vertex from with level socFrom.
	socAt := func(i, from int, socFrom float64) float64 { return socFrom - (energy[i]-energy[from])/usableKWh }

	from, socFrom, extraS := 0, socStart, 0.0
	for {
		// Annotate every site ahead of the current leg with its arrival estimate on this leg
		// (strictly ahead: a stop keeps the arrival estimate it was chosen on).
		for k := range sites {
			if sites[k].idx > from || (from == 0 && sites[k].idx == 0) {
				sites[k].SocArrival = round2(socAt(sites[k].idx, from, socFrom) - detourKWh(&sites[k])/2/usableKWh)
			}
		}
		end := socAt(len(energy)-1, from, socFrom)
		if end >= socMin {
			sum.SocEndEst, sum.Feasible = round2(end), true
			break
		}
		if sum.Stops == maxChargeStops {
			sum.SocEndEst = round2(end)
			sum.Warning = fmt.Sprintf("needs more than %d charge stops; shorten the ride", maxChargeStops)
			break
		}
		// Furthest reachable site ahead; among near-equals prefer more ports.
		var pick *charger
		for k := range sites {
			c := &sites[k]
			if c.idx <= from || c.Stop || c.SocArrival < socMin {
				continue
			}
			if pick == nil || c.idx > pick.idx || (c.idx == pick.idx && c.Ports > pick.Ports) {
				pick = c
			}
		}
		if pick == nil {
			sum.SocEndEst = round2(end)
			sum.Warning = "no reachable charger before the pack drops below the minimum; shorten the ride or start fuller"
			break
		}
		pick.Stop = true
		pick.DwellMin = math.Round(chargeMinutes(pick.SocArrival, chargeTo, pick.PowerKW))
		sum.Stops++
		sum.ChargeMin += pick.DwellMin
		extraS += pick.DwellMin*60 + detourS(pick)
		// Leaving the charger at chargeTo, minus the drive back to the route.
		from, socFrom = pick.idx, chargeTo-detourKWh(pick)/2/usableKWh
	}
	sum.TotalTimeS = math.Round(rideTimeS + extraS)
	return sum
}
