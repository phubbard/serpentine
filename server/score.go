package main

import "math"

// Route statistics and loop scoring (ADR-010). The weights are a first guess; tune them against
// rides Paul would choose, the same way the per-request custom model is tuned.

const earthRadiusKM = 6371.0

func haversineKM(a, b []float64) float64 {
	lat1, lat2 := a[1]*math.Pi/180, b[1]*math.Pi/180
	dLat, dLon := lat2-lat1, (b[0]-a[0])*math.Pi/180
	h := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1)*math.Cos(lat2)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earthRadiusKM * math.Asin(math.Sqrt(h))
}

// cumulativeKM[i] is the along-route distance from the start to coordinate i.
func cumulativeKM(coords [][]float64) []float64 {
	cum := make([]float64, len(coords))
	for i := 1; i < len(coords); i++ {
		cum[i] = cum[i-1] + haversineKM(coords[i-1], coords[i])
	}
	return cum
}

type routeStats struct {
	KM        float64            `json:"km"`
	RoadClass map[string]float64 `json:"road_class_km"`
	Urban     map[string]float64 `json:"urban_density_km"`
	Surface   map[string]float64 `json:"surface_km"`
	// CurvyKM is distance on edges with curvature < 0.94 (beeline/length; 1.0 = straight).
	CurvyKM float64 `json:"curvy_km"`
}

func computeStats(p *ghPath, cum []float64) routeStats {
	byValue := func(key string) map[string]float64 {
		m := map[string]float64{}
		for _, d := range p.Details[key] {
			v := d.Str
			if v == "" {
				v = "missing"
			}
			m[v] += cum[d.To] - cum[d.From]
		}
		return m
	}
	s := routeStats{
		KM:        cum[len(cum)-1],
		RoadClass: byValue("road_class"),
		Urban:     byValue("urban_density"),
		Surface:   byValue("surface"),
	}
	for _, d := range p.Details["curvature"] {
		if d.IsNum && d.Num < 0.94 {
			s.CurvyKM += cum[d.To] - cum[d.From]
		}
	}
	return s
}

var unpavedSurfaces = []string{"gravel", "fine_gravel", "compacted", "unpaved", "dirt", "ground", "grass", "sand"}

// loopScore: lower is better. Terms are fractions of route length so loops of different size compare.
func loopScore(s routeStats, targetKM float64) float64 {
	if s.KM <= 0 {
		return math.Inf(1)
	}
	frac := func(km float64) float64 { return km / s.KM }
	bad := s.RoadClass["track"] + s.RoadClass["service"]
	for _, surf := range unpavedSurfaces {
		bad += s.Surface[surf]
	}
	fast := s.RoadClass["motorway"] + s.RoadClass["trunk"]
	return 5*frac(bad) +
		2*frac(s.Urban["city"]) +
		0.5*frac(s.Urban["residential"]) +
		2*frac(fast) -
		1*frac(s.CurvyKM) +
		2*math.Abs(s.KM-targetKM)/targetKM
}
