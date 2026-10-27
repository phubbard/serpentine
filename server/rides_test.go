package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// The catalogue ships embedded, so a mistake in it is a mistake in the binary. Everything the
// planner needs has to be checkable without the planner.
func TestShippedRideCatalogIsPlannable(t *testing.T) {
	cat, err := parseRideCatalog(rideCatalog)
	if err != nil {
		t.Fatalf("rides.json: %v", err)
	}
	if len(cat.Rides) < 3 {
		t.Errorf("a demo list of %d is not a demo", len(cat.Rides))
	}
	for _, r := range cat.Rides {
		// The graph is US-only. An entry we cannot plan is worse than no entry, because the app
		// has named the road and then failed on it.
		if r.Country != "US" {
			t.Errorf("%s: country %q has no graph yet", r.ID, r.Country)
		}
		// The blurb is the whole pitch on a crowded screen; a sentence, not an essay.
		if n := len([]rune(r.Blurb)); n > 110 {
			t.Errorf("%s: blurb is %d characters", r.ID, n)
		}
		if strings.HasSuffix(r.Name, ".") {
			t.Errorf("%s: name is a title, not a sentence", r.ID)
		}
	}
}

// Each of these is a way the catalogue could be wrong that the planner would report far from the
// cause, or not at all.
func TestRideCatalogRejectsWhatThePlannerCannotUse(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{"no rides", `{"rides":[]}`, "empty"},
		{"duplicate id", `{"rides":[
			{"id":"a","name":"A","road":"R","country":"US","blurb":"b","mode":"out_and_back","start":[-117,33],"turnaround":[-117.1,33.1]},
			{"id":"a","name":"B","road":"R","country":"US","blurb":"b","mode":"out_and_back","start":[-117,33],"turnaround":[-117.1,33.1]}]}`, "duplicate"},
		{"no country", `{"rides":[{"id":"a","name":"A","road":"R","blurb":"b","mode":"out_and_back","start":[-117,33],"turnaround":[-117.1,33.1]}]}`, "country"},
		// The classic: lat,lon instead of lon,lat. 33,-117 is in the Atlantic off Africa.
		{"swapped coordinates", `{"rides":[{"id":"a","name":"A","road":"R","country":"US","blurb":"b","mode":"out_and_back","start":[33,-117],"turnaround":[33.1,-117.1]}]}`, "not a lon,lat"},
		{"turnaround on the start", `{"rides":[{"id":"a","name":"A","road":"R","country":"US","blurb":"b","mode":"out_and_back","start":[-117,33],"turnaround":[-117,33]}]}`, "from the start"},
		{"turnaround in another state", `{"rides":[{"id":"a","name":"A","road":"R","country":"US","blurb":"b","mode":"out_and_back","start":[-117,33],"turnaround":[-83,35]}]}`, "from the start"},
		{"out-and-back with no turnaround", `{"rides":[{"id":"a","name":"A","road":"R","country":"US","blurb":"b","mode":"out_and_back","start":[-117,33]}]}`, "no turnaround"},
		{"loop too short to plan", `{"rides":[{"id":"a","name":"A","road":"R","country":"US","blurb":"b","mode":"loop","start":[-117,33],"distance_m":500}]}`, "outside what"},
		{"one-way with no end", `{"rides":[{"id":"a","name":"A","road":"R","country":"US","blurb":"b","mode":"point_to_point","start":[-117,33]}]}`, "no end"},
		{"a mode the app cannot plan", `{"rides":[{"id":"a","name":"A","road":"R","country":"US","blurb":"b","mode":"wander","start":[-117,33]}]}`, "not a mode"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseRideCatalog([]byte(c.body))
			if err == nil {
				t.Fatalf("accepted a catalogue that would fail at the planner")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error should say what is wrong: got %q, want something about %q", err, c.want)
			}
		})
	}
}

// The app decodes this, so the wire shape is part of the contract. Which geometry field appears
// depends on the mode, so the check is per-entry rather than on whichever one happens to be first.
func TestRideCatalogServesTheFieldsTheAppReads(t *testing.T) {
	var raw struct {
		Rides []map[string]any `json:"rides"`
	}
	if err := json.Unmarshal(rideCatalog, &raw); err != nil {
		t.Fatalf("rides.json: %v", err)
	}
	for _, r := range raw.Rides {
		id, _ := r["id"].(string)
		for _, k := range []string{"id", "name", "road", "region", "country", "blurb", "mode", "start"} {
			if _, ok := r[k]; !ok {
				t.Errorf("%s: wire shape is missing %q", id, k)
			}
		}
		geometry := map[string]string{
			"point_to_point": "end",
			"out_and_back":   "turnaround",
			"loop":           "distance_m",
		}[r["mode"].(string)]
		if _, ok := r[geometry]; !ok {
			t.Errorf("%s: mode %v needs %q", id, r["mode"], geometry)
		}
	}
}
