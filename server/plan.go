package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"sync"
)

// profileVersion goes into the cache key: bump it whenever serpentine.json (the base profile on
// axiom) or the per-request model below changes meaning.
const profileVersion = "base-v0.2/req-v1"

type planRequest struct {
	Mode       string      `json:"mode"`
	Start      *[2]float64 `json:"start"`
	End        *[2]float64 `json:"end,omitempty"`
	DistanceM  float64     `json:"distance_m,omitempty"`
	HeadingDeg *float64    `json:"heading_deg,omitempty"`
	Seed       *int64      `json:"seed,omitempty"`
	Twistiness *float64    `json:"twistiness,omitempty"`
	Avoid      []string    `json:"avoid,omitempty"`
	Charging   *struct {
		Enabled bool `json:"enabled"`
	} `json:"charging,omitempty"`
}

type badRequest struct{ msg string }

func (e *badRequest) Error() string { return e.msg }

func badf(format string, a ...any) error { return &badRequest{fmt.Sprintf(format, a...)} }

// normalize validates and fills defaults so equal requests hash equally.
func (r *planRequest) normalize() error {
	if r.Start == nil {
		return badf("start is required")
	}
	if err := checkLonLat("start", *r.Start); err != nil {
		return err
	}
	switch r.Mode {
	case "point_to_point":
		if r.End == nil {
			return badf("end is required for point_to_point")
		}
		if err := checkLonLat("end", *r.End); err != nil {
			return err
		}
		r.DistanceM, r.HeadingDeg, r.Seed = 0, nil, nil
	case "loop":
		if r.DistanceM < 20_000 || r.DistanceM > 500_000 {
			return badf("distance_m must be between 20000 and 500000 for a loop")
		}
		r.End = nil
		if r.HeadingDeg != nil {
			h := math.Mod(math.Mod(*r.HeadingDeg, 360)+360, 360)
			r.HeadingDeg = &h
		}
		if r.Seed == nil {
			one := int64(1)
			r.Seed = &one
		}
	case "out_and_back":
		return badf("out_and_back is not implemented yet (ADR-005)")
	default:
		return badf(`mode must be "loop" or "point_to_point"`)
	}
	if r.Charging != nil && r.Charging.Enabled {
		return badf("charging is not implemented yet")
	}
	r.Charging = nil
	if r.Twistiness == nil {
		t := 0.5
		r.Twistiness = &t
	}
	if *r.Twistiness < 0 || *r.Twistiness > 1 {
		return badf("twistiness must be between 0 and 1")
	}
	if r.Avoid == nil {
		r.Avoid = []string{"unpaved", "ferries"}
	}
	for _, a := range r.Avoid {
		if a != "unpaved" && a != "ferries" {
			return badf("avoid may contain only \"unpaved\" and \"ferries\"")
		}
	}
	return nil
}

func checkLonLat(field string, p [2]float64) error {
	if p[0] < -180 || p[0] > 180 || p[1] < -90 || p[1] > 90 {
		return badf("%s must be [lon, lat]", field)
	}
	return nil
}

func (r *planRequest) cacheKey() string {
	b, _ := json.Marshal(r)
	sum := sha256.Sum256(append([]byte(profileVersion+"\n"), b...))
	return hex.EncodeToString(sum[:8])
}

// customModelFor layers per-request tightening on the base profile (ADR-009). LM preparation only
// allows multiply_by <= 1, which every rule here is.
func customModelFor(twistiness float64, avoid []string) *customModel {
	f := func(x float64) string { return strconv.FormatFloat(x, 'f', 3, 64) }
	var rules []cmRule
	if twistiness > 0 {
		rules = append(rules,
			cmRule{If: "curvature >= 0.99", MultiplyBy: f(1 - 0.7*twistiness)},
			cmRule{ElseIf: "curvature >= 0.97", MultiplyBy: f(1 - 0.5*twistiness)},
			cmRule{ElseIf: "curvature >= 0.94", MultiplyBy: f(1 - 0.25*twistiness)},
		)
	}
	for _, a := range avoid {
		switch a {
		case "unpaved":
			rules = append(rules, cmRule{
				If:         "road_class == TRACK || surface == GRAVEL || surface == FINE_GRAVEL || surface == COMPACTED || surface == UNPAVED || surface == DIRT || surface == GROUND || surface == GRASS || surface == SAND",
				MultiplyBy: "0.01",
			})
		case "ferries":
			rules = append(rules, cmRule{If: "road_environment == FERRY", MultiplyBy: "0"})
		}
	}
	if len(rules) == 0 {
		return nil
	}
	return &customModel{Priority: rules}
}

type instructionOut struct {
	Text      string  `json:"text"`
	DistanceM float64 `json:"distance_m"`
	TimeS     float64 `json:"time_s"`
	Sign      int     `json:"sign"`
	Index     int     `json:"i"` // polyline index where the manoeuvre happens
}

type loopInfo struct {
	TargetM    float64 `json:"target_m"`
	HeadingDeg float64 `json:"heading_deg"`
	Seed       int64   `json:"seed"`
	Score      float64 `json:"score"`
	Candidates int     `json:"candidates"`
	Failed     int     `json:"failed"`
	RequestedM float64 `json:"requested_m"` // round_trip.distance actually sent for the winner
}

type planResult struct {
	ID           string           `json:"id"`
	Mode         string           `json:"mode"`
	DistanceM    float64          `json:"distance_m"`
	TimeS        float64          `json:"time_s"`
	AscendM      float64          `json:"ascend_m"`
	Polyline     [][]float64      `json:"polyline"`
	Roads        []road           `json:"roads"`
	Instructions []instructionOut `json:"instructions"`
	Stats        routeStats       `json:"stats"`
	Loop         *loopInfo        `json:"loop,omitempty"`
	Handoff      handoff          `json:"handoff"`
	GPXURL       string           `json:"gpx_url"`
}

func buildResult(id, mode string, p *ghPath, loop *loopInfo) *planResult {
	coords := p.Points.Coordinates
	cum := cumulativeKM(coords)
	roads := roadsOf(p, cum)
	ins := make([]instructionOut, 0, len(p.Instructions))
	for _, i := range p.Instructions {
		ins = append(ins, instructionOut{Text: i.Text, DistanceM: math.Round(i.Distance), TimeS: math.Round(float64(i.Time) / 1000), Sign: i.Sign, Index: i.Interval[0]})
	}
	return &planResult{
		ID:           id,
		Mode:         mode,
		DistanceM:    math.Round(p.Distance),
		TimeS:        math.Round(float64(p.Time) / 1000),
		AscendM:      math.Round(p.Ascend),
		Polyline:     coords,
		Roads:        roads,
		Instructions: ins,
		Stats:        computeStats(p, cum),
		Loop:         loop,
		Handoff:      buildHandoff(p, cum, roads),
		GPXURL:       "/v1/plan/" + id + ".gpx",
	}
}

// errNoLoop means every candidate failed: the start is near the edge of the map or in roadless country.
var errNoLoop = errors.New("no loop found from this start; try another heading or a start further from the coast or border")

type loopTry struct {
	heading float64
	seed    int64
	distM   float64
	path    *ghPath
	stats   routeStats
	score   float64
	err     error
}

// planLoop fans out seed x heading round trips, scores them and returns the best (ADR-010).
func (s *server) planLoop(ctx context.Context, r *planRequest, cm *customModel) (*ghPath, *loopInfo, error) {
	var headings []float64
	if r.HeadingDeg != nil {
		h := *r.HeadingDeg
		headings = []float64{h, math.Mod(h+330, 360), math.Mod(h+30, 360)}
	} else {
		headings = []float64{0, 45, 90, 135, 180, 225, 270, 315}
	}
	seeds := []int64{*r.Seed, *r.Seed + 1}
	// round_trip overshoots its distance by 10-130 %; ask for less, then correct the winner.
	firstDist := r.DistanceM * 0.75

	var tries []*loopTry
	for _, h := range headings {
		for _, sd := range seeds {
			tries = append(tries, &loopTry{heading: h, seed: sd, distM: firstDist})
		}
	}
	s.runTries(ctx, *r.Start, tries, cm, r.DistanceM)

	var best *loopTry
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
				return nil, nil, t.err // engine trouble, not geography
			}
		}
		return nil, nil, errNoLoop
	}
	n := len(tries)

	targetKM := r.DistanceM / 1000
	if math.Abs(best.stats.KM-targetKM)/targetKM > 0.10 {
		retry := &loopTry{heading: best.heading, seed: best.seed, distM: best.distM * targetKM / best.stats.KM}
		s.runTries(ctx, *r.Start, []*loopTry{retry}, cm, r.DistanceM)
		n++
		if retry.err != nil {
			failed++
		} else if retry.score < best.score {
			best = retry
		}
	}
	return best.path, &loopInfo{
		TargetM: r.DistanceM, HeadingDeg: best.heading, Seed: best.seed, Score: math.Round(best.score*1000) / 1000,
		Candidates: n, Failed: failed, RequestedM: math.Round(best.distM),
	}, nil
}

const loopConcurrency = 4

func (s *server) runTries(ctx context.Context, start [2]float64, tries []*loopTry, cm *customModel, targetM float64) {
	sem := make(chan struct{}, loopConcurrency)
	var wg sync.WaitGroup
	for _, t := range tries {
		wg.Add(1)
		sem <- struct{}{}
		go func(t *loopTry) {
			defer wg.Done()
			defer func() { <-sem }()
			req := ghRequest{
				Points:      [][2]float64{start},
				Algorithm:   "round_trip",
				RTDistance:  math.Round(t.distM),
				RTSeed:      t.seed,
				Headings:    []float64{t.heading},
				CustomModel: cm,
			}
			t.path, t.err = s.gh.route(ctx, req)
			if t.err == nil {
				t.stats = computeStats(t.path, cumulativeKM(t.path.Points.Coordinates))
				t.score = loopScore(t.stats, targetM/1000)
			}
		}(t)
	}
	wg.Wait()
}
