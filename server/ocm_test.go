package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// poi builds an OCM listing at a point, with a status and a list of check-in outcomes.
func poi(lon, lat float64, statusID int, title string, operational bool, verified time.Time, checkins ...int) ocmPOI {
	var p ocmPOI
	p.AddressInfo.Longitude, p.AddressInfo.Latitude = lon, lat
	p.StatusTypeID = &statusID
	p.StatusType = &struct {
		Title         string `json:"Title"`
		IsOperational *bool  `json:"IsOperational"`
	}{Title: title, IsOperational: &operational}
	if !verified.IsZero() {
		p.DateLastVerified = verified.Format(time.RFC3339)
	}
	for _, c := range checkins {
		id := c
		p.UserComments = append(p.UserComments, struct {
			CheckinStatusTypeID *int   `json:"CheckinStatusTypeID"`
			DateCreated         string `json:"DateCreated"`
		}{CheckinStatusTypeID: &id, DateCreated: time.Now().AddDate(0, -1, 0).Format(time.RFC3339)})
	}
	return p
}

func TestSummariseReliability(t *testing.T) {
	here := [2]float64{-116.8681, 33.0417}
	recent := time.Now().AddDate(0, -2, 0)

	t.Run("nothing listed nearby", func(t *testing.T) {
		// A listing 3 km away is a different charger, so we say nothing at all.
		far := poi(-116.90, 33.07, 50, "Operational", true, recent)
		if r := summarise([]ocmPOI{far}, here); r != nil {
			t.Errorf("matched a charger 3 km away: %+v", r)
		}
	})

	t.Run("operational and recently confirmed", func(t *testing.T) {
		r := summarise([]ocmPOI{poi(-116.8681, 33.0417, 50, "Operational", true, recent)}, here)
		if r == nil || !r.Operational || r.Note != "" {
			t.Fatalf("a working charger needs no warning: %+v", r)
		}
		if r.LastConfirmed == "" || r.Stale {
			t.Errorf("last confirmed %q, stale %v", r.LastConfirmed, r.Stale)
		}
	})

	t.Run("not operational", func(t *testing.T) {
		r := summarise([]ocmPOI{poi(-116.8681, 33.0417, ocmNotOperational, "Not Operational", false, recent)}, here)
		if r == nil || r.Operational {
			t.Fatalf("status 100 must not be operational: %+v", r)
		}
		if r.Note == "" {
			t.Error("a dead charger needs a note")
		}
	})

	t.Run("failed check-ins are counted, successes are not", func(t *testing.T) {
		// 20 = failed, 25 = failed, 10 = charged successfully.
		r := summarise([]ocmPOI{poi(-116.8681, 33.0417, 50, "Operational", true, recent, 20, 25, 10)}, here)
		if r == nil || r.Failures != 2 {
			t.Fatalf("failures = %v, want 2", r)
		}
		if r.Note == "" {
			t.Error("riders reporting trouble should produce a note")
		}
	})

	t.Run("old check-ins are ignored", func(t *testing.T) {
		p := poi(-116.8681, 33.0417, 50, "Operational", true, recent)
		id := 20
		p.UserComments = append(p.UserComments, struct {
			CheckinStatusTypeID *int   `json:"CheckinStatusTypeID"`
			DateCreated         string `json:"DateCreated"`
		}{CheckinStatusTypeID: &id, DateCreated: time.Now().AddDate(-3, 0, 0).Format(time.RFC3339)})
		if r := summarise([]ocmPOI{p}, here); r == nil || r.Failures != 0 {
			t.Errorf("a three-year-old failure shouldn't count: %+v", r)
		}
	})

	t.Run("stale confirmation is flagged, not treated as a failure", func(t *testing.T) {
		old := time.Now().AddDate(-5, 0, 0)
		r := summarise([]ocmPOI{poi(-116.8681, 33.0417, 50, "Operational", true, old)}, here)
		if r == nil || !r.Stale || r.Failures != 0 || !r.Operational {
			t.Fatalf("stale but working: %+v", r)
		}
		if r.Note == "" {
			t.Error("a charger unconfirmed for five years deserves a note")
		}
	})

	t.Run("closest listing wins", func(t *testing.T) {
		near := poi(-116.8682, 33.0418, ocmNotOperational, "Not Operational", false, recent)
		alsoNear := poi(-116.8695, 33.0430, 50, "Operational", true, recent)
		r := summarise([]ocmPOI{alsoNear, near}, here)
		if r == nil || r.Operational {
			t.Errorf("should have matched the nearer, dead listing: %+v", r)
		}
	})
}

func TestWithoutStations(t *testing.T) {
	in := []nrelStation{{ID: 1}, {ID: 2}, {ID: 3}}
	got := withoutStations(in, []int{2})
	if len(got) != 2 || got[0].ID != 1 || got[1].ID != 3 {
		t.Fatalf("got %v", got)
	}
	if len(in) != 3 {
		t.Error("withoutStations mutated its input")
	}
}

func TestNRELID(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int
		ok   bool
	}{
		{"nrel:42", 42, true},
		{"ocm:42", 0, false},
		{"nrel:abc", 0, false},
		{"", 0, false},
	} {
		got, ok := nrelID(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("nrelID(%q) = %d, %v", tc.in, got, ok)
		}
	}
}

// A charge stop that Open Charge Map says is out of service must be replaced, not merely labelled —
// and the replacement plan must still be a valid one (ADR-022).
func TestDeadChargerIsReplanned(t *testing.T) {
	var calls atomic.Int32
	gh := fakeGH(t, nil, &calls)
	defer gh.Close()
	nrel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(loadFixture(t, "nrel_palomar.json"))
	}))
	defer nrel.Close()

	// First, plan with no OCM at all to learn which site gets chosen.
	plain := testServerWithNREL(gh, newNRELClient(nrel.URL, "k"))
	defer plain.Close()
	body := `{"mode":"point_to_point","start":[-117.2116,32.867],"end":[-116.4185,32.87],"charging":{"enabled":true,"soc_start":0.6}}`
	code, res := post(t, plain.URL+"/v1/plan", body)
	if code != 200 {
		t.Fatalf("baseline plan: %d", code)
	}
	firstStop := stopNames(res)
	if len(firstStop) == 0 {
		t.Skip("fixture produced no charge stop, nothing to condemn")
	}
	condemned := firstStop[0]

	// Now an OCM that calls that one dead and everything else fine.
	var lookups atomic.Int32
	ocmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lookups.Add(1)
		if r.Header.Get("X-API-Key") != "ocm-key" {
			t.Errorf("OCM key must travel in the X-API-Key header")
		}
		lat, _ := strconv.ParseFloat(r.URL.Query().Get("latitude"), 64)
		lon, _ := strconv.ParseFloat(r.URL.Query().Get("longitude"), 64)
		status, title, live := 50, "Operational", true
		if nameAt(res, lon, lat) == condemned {
			status, title, live = ocmNotOperational, "Not Operational", false
		}
		writeJSONList(w, []ocmPOI{poi(lon, lat, status, title, live, time.Now().AddDate(0, -1, 0))})
	}))
	defer ocmSrv.Close()

	api := testServerWith(gh, newNRELClient(nrel.URL, "k"), newOCMClient(ocmSrv.URL, "ocm-key"))
	defer api.Close()
	code, res2 := post(t, api.URL+"/v1/plan", body)
	if code != 200 {
		t.Fatalf("plan with OCM: %d", code)
	}
	if lookups.Load() == 0 {
		t.Fatal("OCM was never consulted")
	}
	for _, name := range stopNames(res2) {
		if name == condemned {
			t.Errorf("%q is reported out of service but is still a planned stop", condemned)
		}
	}
	// Whatever it settled on, the rider must still be told what's known about it.
	for _, c := range chargersOf(res2) {
		if c["role"] == "stop" && c["reliability"] == nil {
			t.Errorf("stop %v carries no reliability data", c["name"])
		}
	}
}

func chargersOf(res map[string]any) []map[string]any {
	var out []map[string]any
	list, _ := res["chargers"].([]any)
	for _, c := range list {
		if m, ok := c.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func stopNames(res map[string]any) []string {
	var names []string
	for _, c := range chargersOf(res) {
		if stop, _ := c["stop"].(bool); stop {
			names = append(names, fmt.Sprint(c["name"]))
		}
	}
	return names
}

// nameAt finds the charger a lookup is about, by the coordinates the client sent.
func nameAt(res map[string]any, lon, lat float64) string {
	for _, c := range chargersOf(res) {
		ll, _ := c["lonlat"].([]any)
		if len(ll) != 2 {
			continue
		}
		clon, _ := ll[0].(float64)
		clat, _ := ll[1].(float64)
		if math.Abs(clon-lon) < 1e-4 && math.Abs(clat-lat) < 1e-4 {
			return fmt.Sprint(c["name"])
		}
	}
	return ""
}

func writeJSONList(w http.ResponseWriter, pois []ocmPOI) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(pois)
}
