package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"sync"
	"time"
)

// "Get me to a charger" (ADR-034). The rider is low, and the question is not which charger is
// nearest — it is which one they can still reach. Those are different orderings: a charger eight
// miles away over a ridge can cost more of the pack than one twelve miles away on the flat, and at
// 100 km/h a motorcycle spends roughly half again the Wh/km it spends at 50.
//
// So every candidate is routed for real, with the energy model that plans charge stops, and the
// list is sorted by the fraction of the pack it would take to get there.
const (
	reachCandidates = 10 // chargers routed per request; each is a GraphHopper call
	reachParallel   = 4  // concurrent routes within one request, so one caller can't swamp the graph
	reachMinKM      = 8  // search at least this far, even on a nearly flat battery
	reachMaxKM      = 120
	reachTTL        = 3 * time.Minute
	reachReliable   = 5   // how many of the best get an Open Charge Map lookup
	reachSpreadKM   = 1.0 // only the best site per patch this wide; see spreadSites
)

type reachRequest struct {
	Start   *[2]float64 `json:"start"`
	Soc     *float64    `json:"soc"`
	Vehicle *vehicle    `json:"vehicle,omitempty"`
	Limit   int         `json:"limit,omitempty"`
}

func (r *reachRequest) normalize() error {
	if r.Start == nil {
		return badf("start is required")
	}
	if err := checkLonLat("start", *r.Start); err != nil {
		return err
	}
	if r.Soc == nil {
		return badf("soc is required: this answers what you can reach on what's left")
	}
	if *r.Soc < 0 || *r.Soc > 1 {
		return badf("soc must be between 0 and 1")
	}
	if r.Vehicle == nil {
		v := srs()
		r.Vehicle = &v
	}
	if err := r.Vehicle.normalize(); err != nil {
		return err
	}
	if r.Limit <= 0 || r.Limit > reachCandidates {
		r.Limit = reachCandidates
	}
	return nil
}

// reachOption is one charger, priced. Identity fields match `charger` so the app reads them the
// same way; the rest is what this ride would cost.
type reachOption struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	LonLat     [2]float64 `json:"lonlat"`
	Address    string     `json:"address"`
	Network    string     `json:"network"`
	Ports      int        `json:"ports"`
	PowerKW    float64    `json:"power_kw"`
	Connectors []string   `json:"connectors"`
	Hours      string     `json:"hours,omitempty"`
	Pricing    string     `json:"pricing,omitempty"`

	StraightKM float64 `json:"straight_km"` // as the crow flies, for sanity-checking the route
	DistanceM  float64 `json:"distance_m"`  // the minimum-energy route we would ride
	TimeS      float64 `json:"time_s"`
	AscendM    float64 `json:"ascend_m"`
	KWhEst     float64 `json:"kwh_est"`
	SocNeeded  float64 `json:"soc_needed"`      // fraction of the whole pack, the sort key
	SocArrival float64 `json:"soc_arrival_est"` // what would be left on arrival
	Reachable  bool    `json:"reachable"`
	ChargeMin  float64 `json:"charge_min,omitempty"` // to 80 %, at this site's power

	Reliability *reliability `json:"reliability,omitempty"`
}

type reachResult struct {
	SocStart   float64       `json:"soc_start"`
	UsableKWh  float64       `json:"usable_kwh"`
	RangeKMEst float64       `json:"range_km_est"` // best case: all of it at city consumption, flat
	Nearby     int           `json:"nearby"`       // usable sites inside that range
	Options    []reachOption `json:"options"`
	Warning    string        `json:"warning,omitempty"`
}

// handleReach answers "where can I still get to". Shares the plan limiter: this costs several
// routes, so it must not be the cheap way around the rate limit (ADR-030).
func (s *server) handleReach(w http.ResponseWriter, r *http.Request) {
	var req reachRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if err := req.normalize(); err != nil {
		s.stats.failure("bad")
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.nrel == nil {
		writeError(w, http.StatusServiceUnavailable, "charger data unavailable: server has no NREL key")
		return
	}
	key := req.cacheKey()
	if res, ok := s.reach.get(key); ok {
		s.stats.reach(true, len(res.Options))
		writeJSON(w, http.StatusOK, res)
		return
	}

	ok, age := s.stations.canAnswer()
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "charger data unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()

	res, err := s.reachOptions(ctx, &req)
	if err != nil {
		s.log.Error("reach", "err", err)
		writeError(w, http.StatusBadGateway, "could not work out what you can reach")
		return
	}
	if age > stationsMaxAge {
		res.Warning = "Charger data is more than a week old; check before you depend on it."
	}
	s.reach.put(key, res)
	s.stats.reach(false, len(res.Options))
	s.log.Info("reach", "soc", *req.Soc, "nearby", res.Nearby, "options", len(res.Options))
	writeJSON(w, http.StatusOK, res)
}

func (s *server) reachOptions(ctx context.Context, req *reachRequest) (*reachResult, error) {
	v := req.Vehicle
	remainingKWh := v.UsableKWh * *req.Soc
	// Best case: every kilometre at city consumption on the flat. Road distance is never shorter
	// than the straight line, so nothing beyond this radius is reachable — it is a hard bound, not
	// a guess, which is what makes it safe to search only this far.
	rangeKM := remainingKWh / (v.CityWhPerKM / 1000) / consumptionMargin
	searchKM := math.Min(reachMaxKM, math.Max(reachMinKM, rangeKM))

	start := []float64{req.Start[0], req.Start[1]}
	sites := s.sitesWithin(start, searchKM, v)
	// Nothing at all within what's left is the worst moment this feature has to handle, and an empty
	// screen is the least useful thing to show then. Widen once and list the nearest anyway: a rider
	// waiting for a trailer still needs to know where it should take them.
	strandedBeyond := 0.0
	if len(sites) == 0 && searchKM < reachMaxKM {
		sites = s.sitesWithin(start, reachMaxKM, v)
		strandedBeyond = searchKM
	}
	nearby := len(sites)
	sites = spreadSites(sites, reachSpreadKM, req.Limit)

	opts := make([]reachOption, len(sites))
	routed := make([]bool, len(sites))
	sem := make(chan struct{}, reachParallel)
	var wg sync.WaitGroup
	for i := range sites {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			// One model, not the full candidate set: ten chargers times three routes is a load test,
			// not a feature. The tapped route re-runs the candidates and can only do better.
			path, err := s.gh.route(ctx, ghRequest{
				Points:      [][2]float64{*req.Start, sites[i].LonLat},
				Profile:     "direct",
				CustomModel: reachModel(),
			})
			if err != nil {
				// One unroutable charger (an island, a private drive) must not lose the other nine.
				s.log.Debug("reach: unroutable", "site", sites[i].Name, "err", err)
				return
			}
			opts[i] = optionFor(v, &sites[i], path, *req.Soc)
			routed[i] = true
		}(i)
	}
	wg.Wait()

	out := make([]reachOption, 0, len(opts))
	for i, o := range opts {
		if routed[i] {
			out = append(out, o)
		}
	}
	// The whole point of the feature: cheapest to reach first, not nearest.
	sort.Slice(out, func(i, j int) bool { return out[i].SocNeeded < out[j].SocNeeded })

	// Reliability costs a call each, so only the ones a rider is likely to pick (ADR-022).
	if s.ocm != nil {
		for i := range out {
			if i >= reachReliable {
				break
			}
			out[i].Reliability = s.ocm.lookup(ctx, out[i].LonLat)
		}
	}

	res := &reachResult{
		SocStart: round2(*req.Soc), UsableKWh: v.UsableKWh,
		RangeKMEst: round1(rangeKM), Nearby: nearby, Options: out,
	}
	switch {
	case len(out) == 0:
		res.Warning = "No public charger we can route to from here. This is a tow."
	case strandedBeyond > 0 || !out[0].Reachable:
		res.Warning = "Nothing is within what's left in the pack. These are the nearest anyway — " +
			"somewhere to be towed to, or to aim for if you can find a few more percent."
	case out[0].SocArrival < 0.05:
		res.Warning = "Even the best of these arrives nearly empty. Ride gently and take the first one that works."
	}
	return res, nil
}

// sitesWithin finds the usable public sites inside a radius, nearest first. The station store's
// corridor search over a single point is a radius search.
func (s *server) sitesWithin(start []float64, km float64, v *vehicle) []charger {
	stations := s.stations.nearbyRoute([][]float64{start}, km/1.609344)
	sites := sitesFromStations(stations, acConnectors(v))
	for i := range sites {
		sites[i].OffRouteKM = haversineKM(start, sites[i].LonLat[:])
	}
	sort.Slice(sites, func(i, j int) bool { return sites[i].OffRouteKM < sites[j].OffRouteKM })
	return sites
}

// spreadSites picks candidates a rider would actually choose between: the best site in each patch
// of about a kilometre, nearest patches first. Downtown San Diego has 397 usable sites within range
// of a near-flat battery, and without this the answer is ten parking garages inside three blocks,
// every one of them needing the same nothing. Out in the country, where this feature actually
// matters, sites are tens of kilometres apart and this changes nothing.
//
// `sites` must already be sorted by distance from the rider.
func spreadSites(sites []charger, spreadKM float64, limit int) []charger {
	var out []charger
	for i := range sites {
		patch := -1
		for j := range out {
			if haversineKM(out[j].LonLat[:], sites[i].LonLat[:]) < spreadKM {
				patch = j
				break
			}
		}
		if patch >= 0 {
			// Same patch as one we already have: keep whichever is the better place to be stuck.
			if siteQuality(&sites[i]) > siteQuality(&out[patch]) {
				out[patch] = sites[i]
			}
			continue
		}
		if len(out) >= limit {
			continue // far enough along that any new patch is further than everything we have
		}
		out = append(out, sites[i])
	}
	return out
}

// optionFor prices one routed charger.
func optionFor(v *vehicle, site *charger, p *ghPath, soc float64) reachOption {
	kwh := routeKWh(v, p)
	needed := kwh / v.UsableKWh
	arrival := soc - needed
	if arrival < 0 {
		arrival = 0
	}
	return reachOption{
		ID: site.ID, Name: site.Name, LonLat: site.LonLat, Address: site.Address,
		Network: site.Network, Ports: site.Ports, PowerKW: site.PowerKW,
		Connectors: site.Connectors, Hours: site.Hours, Pricing: site.Pricing,
		StraightKM: round1(site.OffRouteKM),
		DistanceM:  math.Round(p.Distance),
		TimeS:      math.Round(float64(p.Time) / 1000),
		AscendM:    math.Round(p.Ascend),
		KWhEst:     round2(kwh),
		// Three places, not two: a two-kilometre hop costs well under half a percent of the pack,
		// and rounding that to "0 %" throws away the ordering this whole endpoint exists to provide.
		SocNeeded:  round3(needed),
		SocArrival: round3(arrival),
		Reachable:  needed <= soc,
		ChargeMin:  math.Round(v.chargeMinutes(arrival, 0.8, site.PowerKW)),
	}
}

// cacheKey rounds the start to ~100 m and the charge to 5 %, so standing still with a wandering GPS
// fix doesn't re-route ten chargers for every jittered fix.
func (r *reachRequest) cacheKey() string {
	rounded := *r
	start := [2]float64{round3(r.Start[0]), round3(r.Start[1])}
	soc := math.Round(*r.Soc*20) / 20
	rounded.Start, rounded.Soc = &start, &soc
	b, _ := json.Marshal(rounded)
	sum := sha256.Sum256(append([]byte("reach-v1/"+profileVersion+"\n"), b...))
	return hex.EncodeToString(sum[:8])
}

func round3(x float64) float64 { return math.Round(x*1000) / 1000 }

// A cache of its own: reach answers are small, short-lived and keyed differently from plans, and
// mixing them into the plan cache would evict rides a rider is still looking at.
type reachCache struct {
	mu    sync.Mutex
	max   int
	ttl   time.Duration
	items map[string]reachItem
}

type reachItem struct {
	res *reachResult
	at  time.Time
}

func newReachCache(max int, ttl time.Duration) *reachCache {
	return &reachCache{max: max, ttl: ttl, items: map[string]reachItem{}}
}

func (c *reachCache) get(id string) (*reachResult, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	it, ok := c.items[id]
	if !ok || time.Since(it.at) > c.ttl {
		return nil, false
	}
	return it.res, true
}

func (c *reachCache) put(id string, res *reachResult) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.items) >= c.max {
		for k, it := range c.items { // drop anything expired, else one arbitrary entry
			if time.Since(it.at) > c.ttl {
				delete(c.items, k)
			}
		}
		if len(c.items) >= c.max {
			for k := range c.items {
				delete(c.items, k)
				break
			}
		}
	}
	c.items[id] = reachItem{res: res, at: time.Now()}
}
