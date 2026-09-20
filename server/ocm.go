package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Open Charge Map, used only to answer "has anyone reported this charger broken lately?" (ADR-022).
// NREL says a station exists and what it has; it does not say whether it works. OCM carries an
// operational status, a last-verified date, and typed rider check-ins including the ones that
// matter here: equipment not operational, equipment problem, not compatible, and spot occupied.
//
// Per ADR-021 this is deliberately per-station, never per-network. The key lives on axiom and goes
// in the X-API-Key header; it is never logged. Only the coordinates of chargers we already planned
// to visit are sent, and only when the rider asked for charging.
const ocmDefaultBase = "https://api.openchargemap.io/v3"

// OCM reference data (verified 2026-09-19): statuses below 100 are usable, 100 and above are not.
const (
	ocmNotOperational = 100 // "Not Operational"
	ocmTempUnavail    = 30  // "Temporarily Unavailable" — flagged, not fatal
)

// Check-in outcomes that mean a rider turned up and failed to charge.
var ocmFailedCheckins = map[int]string{
	20:  "equipment not operational",
	22:  "equipment not fully installed",
	25:  "equipment problem",
	30:  "not compatible with their vehicle",
	40:  "needed an access card or fob",
	50:  "no charging equipment present",
	100: "spot occupied by another vehicle",
}

const (
	ocmMatchKM       = 0.25 // a site's own listing, not its neighbour's
	ocmCheckinWindow = 365 * 24 * time.Hour
	ocmStaleAfter    = 2 * 365 * 24 * time.Hour // "last confirmed" older than this is not reassurance
	ocmCacheTTL      = 24 * time.Hour           // OCM changes slowly; riders don't need it fresher
)

type ocmClient struct {
	base  string
	key   string
	http  *http.Client
	mu    sync.Mutex
	cache map[string]ocmCacheEntry
}

type ocmCacheEntry struct {
	at   time.Time
	info *reliability
}

func newOCMClient(base, key string) *ocmClient {
	return &ocmClient{
		base:  strings.TrimRight(base, "/"),
		key:   key,
		http:  &http.Client{Timeout: 20 * time.Second},
		cache: map[string]ocmCacheEntry{},
	}
}

// reliability is what riders get told about a charger beyond "it exists".
type reliability struct {
	Status        string `json:"status,omitempty"`         // OCM's own words, e.g. "Operational"
	Operational   bool   `json:"operational"`              // false: don't send anyone here
	LastConfirmed string `json:"last_confirmed,omitempty"` // date, or empty if never verified
	Stale         bool   `json:"stale,omitempty"`          // nobody has confirmed it in two years
	Failures      int    `json:"recent_failures,omitempty"`
	Note          string `json:"note,omitempty"` // one plain sentence for the app to show
}

type ocmPOI struct {
	AddressInfo struct {
		Title     string  `json:"Title"`
		Latitude  float64 `json:"Latitude"`
		Longitude float64 `json:"Longitude"`
	} `json:"AddressInfo"`
	StatusTypeID *int `json:"StatusTypeID"`
	StatusType   *struct {
		Title         string `json:"Title"`
		IsOperational *bool  `json:"IsOperational"`
	} `json:"StatusType"`
	DateLastVerified     string `json:"DateLastVerified"`
	DateLastStatusUpdate string `json:"DateLastStatusUpdate"`
	UserComments         []struct {
		CheckinStatusTypeID *int   `json:"CheckinStatusTypeID"`
		DateCreated         string `json:"DateCreated"`
	} `json:"UserComments"`
}

// lookup returns what OCM knows about the charger at lonlat, or nil if it has no listing there.
// Failures are never fatal: a plan without reliability data is the plan we shipped before.
func (c *ocmClient) lookup(ctx context.Context, lonlat [2]float64) *reliability {
	key := fmt.Sprintf("%.4f,%.4f", lonlat[0], lonlat[1])
	c.mu.Lock()
	if e, ok := c.cache[key]; ok && time.Since(e.at) < ocmCacheTTL {
		c.mu.Unlock()
		return e.info
	}
	c.mu.Unlock()

	info, err := c.fetch(ctx, lonlat)
	if err != nil {
		return nil // caller logs; the ride is still valid without this
	}
	c.mu.Lock()
	c.cache[key] = ocmCacheEntry{at: time.Now(), info: info}
	c.mu.Unlock()
	return info
}

func (c *ocmClient) fetch(ctx context.Context, lonlat [2]float64) (*reliability, error) {
	q := url.Values{}
	q.Set("output", "json")
	q.Set("latitude", strconv.FormatFloat(lonlat[1], 'f', 5, 64))
	q.Set("longitude", strconv.FormatFloat(lonlat[0], 'f', 5, 64))
	q.Set("distance", "1")
	q.Set("distanceunit", "km")
	q.Set("maxresults", "5")
	q.Set("includecomments", "true")
	q.Set("compact", "true")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/poi?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-API-Key", c.key)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ocm: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("ocm: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ocm %d: %.200s", resp.StatusCode, body)
	}
	var pois []ocmPOI
	if err := json.Unmarshal(body, &pois); err != nil {
		return nil, fmt.Errorf("ocm: %w", err)
	}
	return summarise(pois, lonlat), nil
}

// summarise picks the listing closest to the charger we planned and turns it into one honest
// sentence. Nil when OCM has nothing within ocmMatchKM — silence beats a guess.
func summarise(pois []ocmPOI, lonlat [2]float64) *reliability {
	var best *ocmPOI
	bestKM := ocmMatchKM
	for i := range pois {
		d := haversineKM(lonlat[:], []float64{pois[i].AddressInfo.Longitude, pois[i].AddressInfo.Latitude})
		if d <= bestKM {
			best, bestKM = &pois[i], d
		}
	}
	if best == nil {
		return nil
	}
	r := &reliability{Operational: true}
	status := 0
	if best.StatusTypeID != nil {
		status = *best.StatusTypeID
	}
	if best.StatusType != nil {
		r.Status = best.StatusType.Title
		if best.StatusType.IsOperational != nil {
			r.Operational = *best.StatusType.IsOperational
		}
	}
	if status >= ocmNotOperational {
		r.Operational = false
	}

	if t, err := time.Parse(time.RFC3339, best.DateLastVerified); err == nil {
		r.LastConfirmed = t.Format("2006-01-02")
		r.Stale = time.Since(t) > ocmStaleAfter
	}
	for _, c := range best.UserComments {
		if c.CheckinStatusTypeID == nil {
			continue
		}
		if _, bad := ocmFailedCheckins[*c.CheckinStatusTypeID]; !bad {
			continue
		}
		if t, err := time.Parse(time.RFC3339, c.DateCreated); err == nil && time.Since(t) > ocmCheckinWindow {
			continue
		}
		r.Failures++
	}

	switch {
	case !r.Operational:
		r.Note = "Reported out of service — plan to use the backup."
	case status == ocmTempUnavail:
		r.Note = "Reported temporarily unavailable."
	case r.Failures >= 2:
		r.Note = fmt.Sprintf("%d riders reported trouble charging here in the last year.", r.Failures)
	case r.Failures == 1:
		r.Note = "One rider reported trouble charging here in the last year."
	case r.Stale && r.LastConfirmed != "":
		r.Note = "Not confirmed working since " + r.LastConfirmed + "."
	}
	return r
}

// addReliability annotates the stops and their backups with what Open Charge Map knows, and returns
// the NREL ids of any *stop* reported out of service. Alternates are left alone: there can be dozens
// and nobody is being sent to them.
func (s *server) addReliability(ctx context.Context, res *planResult) []int {
	var (
		wg   sync.WaitGroup
		sem  = make(chan struct{}, 4)
		mu   sync.Mutex
		dead []int
	)
	for i := range res.Chargers {
		c := &res.Chargers[i]
		if c.Role != "stop" && c.Role != "backup" {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(c *charger) {
			defer wg.Done()
			defer func() { <-sem }()
			info := s.ocm.lookup(ctx, c.LonLat)
			if info == nil {
				return
			}
			c.Reliability = info
			if !info.Operational && c.Stop {
				if id, ok := nrelID(c.ID); ok {
					mu.Lock()
					dead = append(dead, id)
					mu.Unlock()
				}
			}
		}(c)
	}
	wg.Wait()
	return dead
}

// nrelID turns a charger id ("nrel:12345") back into the station id it was built from.
func nrelID(id string) (int, bool) {
	rest, ok := strings.CutPrefix(id, "nrel:")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	return n, err == nil
}

// withoutStations drops stations by id, so charge stops can be planned again without the ones
// riders report dead.
func withoutStations(stations []nrelStation, ids []int) []nrelStation {
	drop := make(map[int]bool, len(ids))
	for _, id := range ids {
		drop[id] = true
	}
	kept := make([]nrelStation, 0, len(stations))
	for _, st := range stations {
		if !drop[st.ID] {
			kept = append(kept, st)
		}
	}
	return kept
}
