package main

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// Handoff to Apple Maps (ADR-004, ADR-012, ADR-013).
//
// Waypoints go a short way into each significant road on our route, so Apple has to use that road
// to reach the stop. The source/destination sit on the first/last public road: a point in a private
// lot makes Apple say "Walking required".

const (
	maxHandoffWaypoints = 10  // Apple's cap for URLs is unmeasured beyond 7; the UI shows ~13.
	minRoadKM           = 3.0 // shorter roads are connectors Apple will pick anyway
	waypointIntoRoadKM  = 1.0 // distance past the turn onto the road
	endpointClearKM     = 2.0 // no waypoint this close to source/destination
)

type road struct {
	Name    string  `json:"name"`
	FromIdx int     `json:"-"`
	ToIdx   int     `json:"-"`
	KM      float64 `json:"km"`
}

func roadName(ins ghInstruction) string {
	switch {
	case ins.StreetName != "" && ins.StreetRef != "":
		return ins.StreetName + " (" + ins.StreetRef + ")"
	case ins.StreetRef != "":
		return ins.StreetRef
	default:
		return ins.StreetName
	}
}

// roadsOf merges consecutive instructions on the same named road.
func roadsOf(p *ghPath, cum []float64) []road {
	var roads []road
	for _, ins := range p.Instructions {
		if ins.Interval[1] <= ins.Interval[0] {
			continue // arrival / waypoint markers
		}
		name := roadName(ins)
		km := cum[ins.Interval[1]] - cum[ins.Interval[0]]
		if n := len(roads); n > 0 && roads[n-1].Name == name {
			roads[n-1].ToIdx = ins.Interval[1]
			roads[n-1].KM += km
			continue
		}
		roads = append(roads, road{Name: name, FromIdx: ins.Interval[0], ToIdx: ins.Interval[1], KM: km})
	}
	return roads
}

// indexAtKM returns the first coordinate index at or past the given along-route distance.
func indexAtKM(cum []float64, km float64) int {
	i := sort.SearchFloat64s(cum, km)
	if i >= len(cum) {
		i = len(cum) - 1
	}
	return i
}

type handoff struct {
	AppleMapsURL  string       `json:"apple_maps_url"`
	GoogleMapsURL string       `json:"google_maps_url"`
	Source        [2]float64   `json:"source"`
	Destination   [2]float64   `json:"destination"`
	Waypoints     [][2]float64 `json:"waypoints"`
	WaypointRoads []string     `json:"waypoint_roads"`
}

func lonLat(c []float64) [2]float64 { return [2]float64{c[0], c[1]} }

// publicEndpoints finds the first and last polyline indexes on a road Apple will drive to.
func publicEndpoints(p *ghPath, n int) (int, int) {
	first, last := 0, n-1
	private := func(v string) bool { return v == "service" || v == "track" }
	rc := p.Details["road_class"]
	for _, d := range rc {
		if !private(d.Str) {
			first = d.From
			break
		}
	}
	for i := len(rc) - 1; i >= 0; i-- {
		if !private(rc[i].Str) {
			last = rc[i].To
			break
		}
	}
	return first, last
}

func buildHandoff(p *ghPath, cum []float64, roads []road) handoff {
	coords := p.Points.Coordinates
	srcIdx, dstIdx := publicEndpoints(p, len(coords))
	src, dst := coords[srcIdx], coords[dstIdx]

	type cand struct {
		idx  int
		road string
		km   float64
	}
	var cands []cand
	for _, r := range roads {
		if r.Name == "" || r.KM < minRoadKM {
			continue
		}
		idx := indexAtKM(cum, cum[r.FromIdx]+waypointIntoRoadKM)
		c := coords[idx]
		if haversineKM(c, src) < endpointClearKM || haversineKM(c, dst) < endpointClearKM {
			continue
		}
		// A short differently-named stretch can split one road in two; one waypoint per road is enough.
		if n := len(cands); n > 0 && cands[n-1].road == r.Name {
			cands[n-1].km += r.KM
			continue
		}
		cands = append(cands, cand{idx, r.Name, r.KM})
	}
	if len(cands) > maxHandoffWaypoints {
		// Keep the longest roads, then restore route order.
		sort.SliceStable(cands, func(i, j int) bool { return cands[i].km > cands[j].km })
		cands = cands[:maxHandoffWaypoints]
		sort.Slice(cands, func(i, j int) bool { return cands[i].idx < cands[j].idx })
	}

	h := handoff{Source: lonLat(src), Destination: lonLat(dst), Waypoints: [][2]float64{}, WaypointRoads: []string{}}
	for _, c := range cands {
		h.Waypoints = append(h.Waypoints, lonLat(coords[c.idx]))
		h.WaypointRoads = append(h.WaypointRoads, c.road)
	}
	h.AppleMapsURL = appleMapsURL(h.Source, h.Waypoints, h.Destination)
	h.GoogleMapsURL = googleMapsURL(h.Source, h.Waypoints, h.Destination)
	return h
}

// latLon formats [lon, lat] as Apple/Google want it: "lat,lon".
func latLon(p [2]float64) string { return fmt.Sprintf("%.5f,%.5f", p[1], p[0]) }

// appleMapsURL builds the iOS 18.4+ unified directions URL (repeated waypoint=).
func appleMapsURL(src [2]float64, wps [][2]float64, dst [2]float64) string {
	var b strings.Builder
	b.WriteString("https://maps.apple.com/directions?source=" + latLon(src))
	for _, w := range wps {
		b.WriteString("&waypoint=" + latLon(w))
	}
	b.WriteString("&destination=" + latLon(dst) + "&mode=driving&avoid=tolls,highways")
	return b.String()
}

func googleMapsURL(src [2]float64, wps [][2]float64, dst [2]float64) string {
	parts := make([]string, len(wps))
	for i, w := range wps {
		parts[i] = latLon(w)
	}
	q := url.Values{}
	q.Set("api", "1")
	q.Set("origin", latLon(src))
	q.Set("destination", latLon(dst))
	q.Set("travelmode", "driving")
	if len(parts) > 0 {
		q.Set("waypoints", strings.Join(parts, "|"))
	}
	return "https://www.google.com/maps/dir/?" + q.Encode()
}
