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
	Mode       string        `json:"mode"`
	Start      *[2]float64   `json:"start"`
	End        *[2]float64   `json:"end,omitempty"`
	Turnaround *[2]float64   `json:"turnaround,omitempty"`
	DistanceM  float64       `json:"distance_m,omitempty"`
	DurationS  float64       `json:"duration_s,omitempty"`  // loop, out_and_back: ride time instead of distance
	MaxExtraS  *float64      `json:"max_extra_s,omitempty"` // point_to_point: seconds of detour allowed over the quickest route
	HeadingDeg *float64      `json:"heading_deg,omitempty"`
	Seed       *int64        `json:"seed,omitempty"`
	Twistiness *float64      `json:"twistiness,omitempty"`
	Avoid      []string      `json:"avoid,omitempty"`
	Charging   *chargingOpts `json:"charging,omitempty"`
	Vehicle    *vehicle      `json:"vehicle,omitempty"` // omitted = the Zero SR/S (ADR-025)
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
	if err := r.normalizeDuration(); err != nil {
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
		r.DistanceM, r.HeadingDeg, r.Seed, r.Turnaround = 0, nil, nil, nil
		if r.MaxExtraS != nil && (*r.MaxExtraS < 0 || *r.MaxExtraS > 7200) {
			return badf("max_extra_s must be between 0 and 7200")
		}
	case "out_and_back":
		r.End = nil
		if r.Turnaround != nil {
			if err := checkLonLat("turnaround", *r.Turnaround); err != nil {
				return err
			}
			r.DistanceM, r.HeadingDeg, r.Seed = 0, nil, nil
			break
		}
		if r.DistanceM < 20_000 || r.DistanceM > 500_000 {
			return badf("out_and_back needs a turnaround or distance_m between 20000 and 500000")
		}
		r.normalizeHeadingSeed()
	case "loop":
		if r.DistanceM < 20_000 || r.DistanceM > 500_000 {
			return badf("distance_m must be between 20000 and 500000 for a loop")
		}
		r.End, r.Turnaround = nil, nil
		r.normalizeHeadingSeed()
	default:
		return badf(`mode must be "loop", "out_and_back" or "point_to_point"`)
	}
	if r.Mode != "point_to_point" {
		r.MaxExtraS = nil
	}
	if err := r.normalizeCharging(); err != nil {
		return err
	}
	if r.Charging == nil {
		r.Vehicle = nil // nothing to model, and it would only split the cache
	} else {
		if r.Vehicle == nil {
			v := srs()
			r.Vehicle = &v
		}
		if err := r.Vehicle.normalize(); err != nil {
			return err
		}
	}
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

// budgetSpeedKMH turns a time budget into a first distance guess. Deliberately low: serpentine's
// roads are slow, and a ride that comes in under the budget is fine while one that runs over is a
// broken promise. The plan is corrected against GraphHopper's own time afterwards (planForBudget).
const budgetSpeedKMH = 55

// normalizeDuration converts a time budget into the distance the rest of the pipeline works in.
func (r *planRequest) normalizeDuration() error {
	if r.DurationS == 0 {
		return nil
	}
	if r.Mode == "point_to_point" {
		return badf("duration_s applies to loop and out_and_back only")
	}
	if r.DistanceM != 0 {
		return badf("give distance_m or duration_s, not both")
	}
	if r.DurationS < 1800 || r.DurationS > 28800 {
		return badf("duration_s must be between 1800 and 28800")
	}
	r.DurationS = math.Round(r.DurationS)
	r.DistanceM = math.Round(clamp(r.DurationS/3600*budgetSpeedKMH*1000, 20_000, 500_000))
	return nil
}

func clamp(v, lo, hi float64) float64 { return math.Min(math.Max(v, lo), hi) }

func (r *planRequest) normalizeHeadingSeed() {
	if r.HeadingDeg != nil {
		h := math.Mod(math.Mod(*r.HeadingDeg, 360)+360, 360)
		r.HeadingDeg = &h
	}
	if r.Seed == nil {
		one := int64(1)
		r.Seed = &one
	}
}

func (r *planRequest) normalizeCharging() error {
	c := r.Charging
	if c == nil || !c.Enabled {
		r.Charging = nil
		return nil
	}
	def := func(p **float64, v float64) {
		if *p == nil {
			*p = &v
		}
	}
	def(&c.SocStart, 1.0)
	def(&c.SocMinArrival, 0.15)
	def(&c.ChargeTo, 0.9)
	for _, v := range []float64{*c.SocStart, *c.SocMinArrival, *c.ChargeTo} {
		if v < 0 || v > 1 {
			return badf("charging soc values must be between 0 and 1")
		}
	}
	if *c.SocMinArrival >= *c.SocStart || *c.SocMinArrival >= *c.ChargeTo {
		return badf("soc_min_arrival must be below soc_start and charge_to")
	}
	return nil
}

func checkLonLat(field string, p [2]float64) error {
	if p[0] < -180 || p[0] > 180 || p[1] < -90 || p[1] > 90 {
		return badf("%s must be [lon, lat]", field)
	}
	return nil
}

// shape is what a request tells the operational counters: what kind of ride, never where (ADR-026).
func (r *planRequest) shape(cached bool, seconds float64) planShape {
	budget := "distance"
	if r.DurationS > 0 {
		budget = "time"
	}
	p := planShape{Mode: r.Mode, Budget: budget, Cached: cached, Seconds: seconds}
	if r.Charging != nil {
		p.Charging = true
		p.Reserve = r.Charging.ReserveForBackup == nil || *r.Charging.ReserveForBackup
		if r.Vehicle != nil {
			def := srs()
			p.Custom = r.Vehicle.Name != def.Name || r.Vehicle.UsableKWh != def.UsableKWh
		}
	}
	return p
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

// budgetInfo answers "does this fit in the time I have?" for a duration_s request. TotalS includes
// charging when it was planned.
type budgetInfo struct {
	TargetS float64 `json:"target_s"`
	TotalS  float64 `json:"total_s"`
	Fits    bool    `json:"fits"`
	// Note explains a ride that came back far shorter than asked for — nearly always a bike that
	// needs a charge stop the budget can't hold (ADR-018 amendment).
	Note string `json:"note,omitempty"`
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
	Detour       *detourInfo      `json:"detour,omitempty"` // point_to_point with max_extra_s
	Budget       *budgetInfo      `json:"budget,omitempty"` // duration_s requests only
	OutAndBack   *outBackInfo     `json:"out_and_back,omitempty"`
	Energy       *energySummary   `json:"energy,omitempty"`   // charging requests only
	Chargers     []charger        `json:"chargers,omitempty"` // charging requests only
	Handoff      handoff          `json:"handoff"`
	GPXURL       string           `json:"gpx_url"`
}

// buildResult assembles the response. stations is nil unless charging was requested; turnIdx is
// the out-and-back turnaround's polyline index, or -1.
func buildResult(id, mode string, p *ghPath, loop *loopInfo, ob *outBackInfo, turnIdx int, stations []nrelStation, co *chargingOpts, v *vehicle) *planResult {
	coords := p.Points.Coordinates
	cum := cumulativeKM(coords)
	roads := roadsOf(p, cum)
	var (
		energy *energySummary
		sites  []charger
		forced []forcedWaypoint
	)
	if turnIdx >= 0 {
		forced = append(forced, forcedWaypoint{idx: turnIdx, pt: lonLat(coords[turnIdx]), label: "Turnaround"})
	}
	if co != nil {
		sites = sitesFromStations(stations, acConnectors(v))
		placeOnRoute(sites, coords, cum)
		sum := planCharging(v, sites, energyProfile(v, p, cum), float64(p.Time)/1000, *co)
		sum.ChargersNearby = len(sites)
		energy = &sum
		for _, c := range sites {
			if c.Stop {
				forced = append(forced, forcedWaypoint{idx: c.idx, pt: c.LonLat, label: "Charge: " + c.Name})
			}
		}
		sites = selectChargers(sites)
	}
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
		OutAndBack:   ob,
		Energy:       energy,
		Chargers:     sites,
		Handoff:      buildHandoff(p, cum, roads, forced),
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
				// A loop shouldn't ride any road twice: spurs cost as much as dirt.
				t.score = loopScore(t.stats, targetM/1000) + 5*t.stats.RepeatedKM/t.stats.KM
			}
		}(t)
	}
	wg.Wait()
}
