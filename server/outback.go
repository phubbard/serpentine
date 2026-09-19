package main

import (
	"context"
	"errors"
	"math"
)

// Out-and-back (ADR-005, ADR-015): ride out to a turnaround, come home a different way. The return
// leg is routed with a per-request custom model that penalises a corridor around the outbound
// polyline (GraphHopper "areas"; tighten-only, so valid under LM). 0.3 inside the corridor makes the
// return prefer a comparable different road without forcing a huge detour: Julian → Ramona came
// home on Old Julian Hwy at the same 35 km, where 0.1 sent it 105 km round via Alpine.

const (
	corridorHalfWidthKM = 0.3
	corridorSampleKM    = 0.5
	corridorEndClearKM  = 2.0 // the first and last km near start/turnaround are shared anyway
	corridorMultiplyBy  = "0.3"
	roadWindingFactor   = 1.3 // road km per straight-line km, for placing turnarounds
	sharedPenalty       = 3.0 // per fraction of the return that repeats the outbound
)

// errNoOutBack: every candidate turnaround failed to route.
var errNoOutBack = errors.New("no out-and-back found from this start; try another direction or a shorter distance")

type outBackInfo struct {
	TargetM    float64    `json:"target_m,omitempty"`
	Turnaround [2]float64 `json:"turnaround"`
	HeadingDeg float64    `json:"heading_deg"`
	OutKM      float64    `json:"out_km"`
	BackKM     float64    `json:"back_km"`
	SharedKM   float64    `json:"shared_km"` // return distance within 300 m of the outbound road
	Score      float64    `json:"score"`
	Candidates int        `json:"candidates"`
	Failed     int        `json:"failed"`
}

type featureCollection struct {
	Type     string    `json:"type"`
	Features []feature `json:"features"`
}

type feature struct {
	Type       string         `json:"type"`
	ID         string         `json:"id"`
	Properties map[string]any `json:"properties"`
	Geometry   struct {
		Type        string           `json:"type"`
		Coordinates [][][][2]float64 `json:"coordinates"`
	} `json:"geometry"`
}

// destination returns the point distKM from p on the given bearing (degrees).
func destination(p [2]float64, bearingDeg, distKM float64) [2]float64 {
	lat1, lon1 := p[1]*math.Pi/180, p[0]*math.Pi/180
	b, d := bearingDeg*math.Pi/180, distKM/earthRadiusKM
	lat2 := math.Asin(math.Sin(lat1)*math.Cos(d) + math.Cos(lat1)*math.Sin(d)*math.Cos(b))
	lon2 := lon1 + math.Atan2(math.Sin(b)*math.Sin(d)*math.Cos(lat1), math.Cos(d)-math.Sin(lat1)*math.Sin(lat2))
	return [2]float64{lon2 * 180 / math.Pi, lat2 * 180 / math.Pi}
}

// sampleIdx picks polyline indexes about stepKM apart, skipping clearKM at each end.
func sampleIdx(cum []float64, stepKM, clearKM float64) []int {
	total := cum[len(cum)-1]
	var out []int
	last := -1.0
	for i, c := range cum {
		if c < clearKM || c > total-clearKM {
			continue
		}
		if last < 0 || c-last >= stepKM {
			out = append(out, i)
			last = c
		}
	}
	return out
}

// corridorModel adds a penalty for a band ±corridorHalfWidthKM around the polyline (minus its ends)
// to base. Returns base unchanged if the outbound leg is too short to have a middle.
func corridorModel(base *customModel, coords [][]float64) *customModel {
	idx := sampleIdx(cumulativeKM(coords), corridorSampleKM, corridorEndClearKM)
	if len(idx) < 2 {
		return base
	}
	var polys [][][][2]float64
	for k := 1; k < len(idx); k++ {
		a, b := coords[idx[k-1]], coords[idx[k]]
		kmPerLon := 111.32 * math.Cos(a[1]*math.Pi/180)
		dx, dy := (b[0]-a[0])*kmPerLon, (b[1]-a[1])*110.57
		l := math.Hypot(dx, dy)
		if l == 0 {
			continue
		}
		nx, ny := -dy/l*corridorHalfWidthKM, dx/l*corridorHalfWidthKM
		off := func(p []float64, s float64) [2]float64 {
			return [2]float64{p[0] + s*nx/kmPerLon, p[1] + s*ny/110.57}
		}
		ring := [][2]float64{off(a, 1), off(b, 1), off(b, -1), off(a, -1), off(a, 1)}
		polys = append(polys, [][][2]float64{ring})
	}
	f := feature{Type: "Feature", ID: "outbound", Properties: map[string]any{}}
	f.Geometry.Type = "MultiPolygon"
	f.Geometry.Coordinates = polys
	cm := customModel{Areas: &featureCollection{Type: "FeatureCollection", Features: []feature{f}}}
	if base != nil {
		cm.Priority = append(cm.Priority, base.Priority...)
	}
	cm.Priority = append(cm.Priority, cmRule{If: "in_outbound", MultiplyBy: corridorMultiplyBy})
	return &cm
}

// sharedKM estimates how much of back runs within the corridor of out (sampled, ends excluded).
func sharedKM(out, back [][]float64) float64 {
	oc, bc := cumulativeKM(out), cumulativeKM(back)
	oi := sampleIdx(oc, 0.2, corridorEndClearKM)
	bi := sampleIdx(bc, 0.2, corridorEndClearKM)
	shared := 0.0
	for k, i := range bi {
		for _, j := range oi {
			if haversineKM(back[i], out[j]) < corridorHalfWidthKM {
				step := 0.2
				if k > 0 {
					step = bc[i] - bc[bi[k-1]]
				}
				shared += step
				break
			}
		}
	}
	return shared
}

// mergePaths joins out and back at the turnaround into one path; the out leg's "arrive"
// instruction becomes a via.
func mergePaths(out, back *ghPath) *ghPath {
	m := &ghPath{
		Distance: out.Distance + back.Distance, Time: out.Time + back.Time,
		Ascend: out.Ascend + back.Ascend, Descend: out.Descend + back.Descend,
		Details: map[string][]ghDetail{},
	}
	off := len(out.Points.Coordinates) - 1
	m.Points.Coordinates = append(append([][]float64{}, out.Points.Coordinates...), back.Points.Coordinates[1:]...)
	for _, ins := range out.Instructions {
		if ins.Sign == 4 { // finish
			ins.Sign, ins.Text = 5, "Turnaround: head back"
		}
		m.Instructions = append(m.Instructions, ins)
	}
	for _, ins := range back.Instructions {
		ins.Interval[0] += off
		ins.Interval[1] += off
		m.Instructions = append(m.Instructions, ins)
	}
	for k, ds := range out.Details {
		m.Details[k] = append(m.Details[k], ds...)
	}
	for k, ds := range back.Details {
		for _, d := range ds {
			d.From += off
			d.To += off
			m.Details[k] = append(m.Details[k], d)
		}
	}
	return m
}

type outBackTry struct {
	heading    float64
	radiusKM   float64
	turnaround [2]float64
	out, back  *ghPath
	shared     float64
	stats      routeStats
	score      float64
	err        error
}

func (s *server) tryOutBack(ctx context.Context, start [2]float64, t *outBackTry, cm *customModel, targetKM float64) {
	t.out, t.err = s.gh.route(ctx, ghRequest{Points: [][2]float64{start, t.turnaround}, CustomModel: cm})
	if t.err != nil {
		return
	}
	back := corridorModel(cm, t.out.Points.Coordinates)
	t.back, t.err = s.gh.route(ctx, ghRequest{Points: [][2]float64{t.turnaround, start}, CustomModel: back})
	if t.err != nil {
		return
	}
	m := mergePaths(t.out, t.back)
	t.stats = computeStats(m, cumulativeKM(m.Points.Coordinates))
	t.shared = sharedKM(t.out.Points.Coordinates, t.back.Points.Coordinates)
	if targetKM == 0 { // explicit turnaround: no distance target
		targetKM = t.stats.KM
	}
	t.score = loopScore(t.stats, targetKM)
	if backKM := t.back.Distance / 1000; backKM > 0 {
		t.score += sharedPenalty * t.shared / backKM
	}
}

// planOutBack returns the merged path and the index of the turnaround in its polyline.
func (s *server) planOutBack(ctx context.Context, r *planRequest, cm *customModel) (*ghPath, int, *outBackInfo, error) {
	var tries []*outBackTry
	targetKM := r.DistanceM / 1000
	if r.Turnaround != nil {
		tries = []*outBackTry{{turnaround: *r.Turnaround}}
	} else {
		var headings []float64
		if r.HeadingDeg != nil {
			h := *r.HeadingDeg
			headings = []float64{h, math.Mod(h+330, 360), math.Mod(h+30, 360)}
		} else {
			// The seed rotates the fan so "another one" gives new turnarounds.
			rot := math.Mod(float64(*r.Seed-1)*22.5, 45)
			for h := 0.0; h < 360; h += 45 {
				headings = append(headings, h+rot)
			}
		}
		radius := targetKM / 2 / roadWindingFactor
		for _, h := range headings {
			tries = append(tries, &outBackTry{heading: h, radiusKM: radius, turnaround: destination(*r.Start, h, radius)})
		}
	}
	s.runOutBack(ctx, *r.Start, tries, cm, targetKM)

	var best *outBackTry
	failed := 0
	for _, t := range tries {
		if t.err != nil {
			failed++
			continue
		}
		if best == nil || t.score < best.score {
			best = t
		}
	}
	if best == nil {
		for _, t := range tries {
			var ge *ghError
			if !errors.As(t.err, &ge) {
				return nil, 0, nil, t.err
			}
		}
		return nil, 0, nil, errNoOutBack
	}
	n := len(tries)
	if r.Turnaround == nil && math.Abs(best.stats.KM-targetKM)/targetKM > 0.10 {
		radius := best.radiusKM * targetKM / best.stats.KM
		retry := &outBackTry{heading: best.heading, radiusKM: radius, turnaround: destination(*r.Start, best.heading, radius)}
		s.runOutBack(ctx, *r.Start, []*outBackTry{retry}, cm, targetKM)
		n++
		if retry.err != nil {
			failed++
		} else if retry.score < best.score {
			best = retry
		}
	}
	m := mergePaths(best.out, best.back)
	info := &outBackInfo{
		Turnaround: lonLat(best.out.Points.Coordinates[len(best.out.Points.Coordinates)-1]),
		HeadingDeg: best.heading, OutKM: round1(best.out.Distance / 1000), BackKM: round1(best.back.Distance / 1000),
		SharedKM: round1(best.shared), Score: math.Round(best.score*1000) / 1000, Candidates: n, Failed: failed,
	}
	if r.Turnaround == nil {
		info.TargetM = r.DistanceM
	}
	return m, len(best.out.Points.Coordinates) - 1, info, nil
}

func (s *server) runOutBack(ctx context.Context, start [2]float64, tries []*outBackTry, cm *customModel, targetKM float64) {
	done := make(chan struct{})
	sem := make(chan struct{}, loopConcurrency)
	for _, t := range tries {
		go func(t *outBackTry) {
			sem <- struct{}{}
			s.tryOutBack(ctx, start, t, cm, targetKM)
			<-sem
			done <- struct{}{}
		}(t)
	}
	for range tries {
		<-done
	}
}
