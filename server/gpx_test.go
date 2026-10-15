package main

import (
	"encoding/xml"
	"io"
	"strings"
	"testing"
)

// gpxDoc decodes far enough to assert shape and order. Element order matters twice over: GPX 1.1
// fixes it in the schema, and Beeline reads a file's contents to decide which import mode to offer.
type gpxDoc struct {
	XMLName  xml.Name `xml:"gpx"`
	Version  string   `xml:"version,attr"`
	Creator  string   `xml:"creator,attr"`
	Metadata struct {
		Name string `xml:"name"`
	} `xml:"metadata"`
	Wpt []struct {
		Lat  float64 `xml:"lat,attr"`
		Lon  float64 `xml:"lon,attr"`
		Name string  `xml:"name"`
		Cmt  string  `xml:"cmt"`
		Desc string  `xml:"desc"`
	} `xml:"wpt"`
	Rte []struct {
		Name  string `xml:"name"`
		Rtept []struct {
			Lat float64 `xml:"lat,attr"`
			Lon float64 `xml:"lon,attr"`
		} `xml:"rtept"`
	} `xml:"rte"`
	Trk []struct {
		Name   string `xml:"name"`
		Trkseg []struct {
			Trkpt []struct {
				Lat float64 `xml:"lat,attr"`
				Lon float64 `xml:"lon,attr"`
			} `xml:"trkpt"`
		} `xml:"trkseg"`
	} `xml:"trk"`
}

func renderGPX(t *testing.T, res *planResult, kind gpxKind) (string, gpxDoc) {
	t.Helper()
	var b strings.Builder
	if err := writePlanGPX(&b, res, kind); err != nil {
		t.Fatalf("%s: %v", kind, err)
	}
	var doc gpxDoc
	if err := xml.Unmarshal([]byte(b.String()), &doc); err != nil {
		t.Fatalf("%s is not well-formed GPX: %v\n%s", kind, err, b.String())
	}
	return b.String(), doc
}

// A plan with two charge stops, a backup, and a polyline dense enough to need decimating.
func gpxFixture(t *testing.T) *planResult {
	t.Helper()
	res := &planResult{Mode: "loop"}
	for i := 0; i < 400; i++ {
		// ~11 m apart, so a 30 m decimation has real work to do.
		res.Polyline = append(res.Polyline, []float64{-116.8 + float64(i)*0.0001, 33.0, float64(i)})
	}
	res.Handoff = handoff{
		Source:      [2]float64{-116.8, 33.0},
		Destination: [2]float64{-116.76, 33.0},
		Waypoints:   [][2]float64{{-116.79, 33.0}, {-116.78, 33.0}},
	}
	res.Energy = &energySummary{ChargeTo: 0.8}
	res.Chargers = []charger{
		{Name: "Ramona Shopping Sunvalley", LonLat: [2]float64{-116.79, 33.0}, Address: "1 Main St, Ramona, CA",
			Ports: 6, PowerKW: 6.5, SocArrival: 0.27, DwellMin: 95, Stop: true, Role: "stop", idx: 100},
		{Name: "El Capitan", LonLat: [2]float64{-116.78, 33.0}, Address: "2 Lake Rd, Lakeside, CA",
			Ports: 8, PowerKW: 12, SocArrival: 0.31, DwellMin: 60, Stop: true, Role: "stop", idx: 250},
		{Name: "Ev Villat Lake Lakeshore01", LonLat: [2]float64{-116.775, 33.0}, Address: "3 Shore Dr, Lakeside, CA",
			Ports: 2, PowerKW: 6.5, SocArrival: 0.2, Stop: false, Role: "backup", idx: 260},
		{Name: "Somewhere else entirely", LonLat: [2]float64{-116.70, 33.1}, Address: "4 Far Rd",
			Ports: 4, PowerKW: 7, Stop: false, Role: "alternate", idx: 390},
	}
	return res
}

// The rule the whole design rests on: Beeline picks its mode from what the file holds, in the order
// track → route → waypoints. One intent per file, or a <trk> silently wins and the rider gets a
// breadcrumb with no turn prompts.
func TestEachExportHoldsOneIntent(t *testing.T) {
	res := gpxFixture(t)

	_, wpts := renderGPX(t, res, gpxWaypoints)
	if len(wpts.Wpt) == 0 {
		t.Error("the waypoints file has no waypoints, so Beeline will not offer waypoints-only mode")
	}
	if len(wpts.Rte) != 0 || len(wpts.Trk) != 0 {
		t.Errorf("waypoints file also holds %d rte and %d trk; Beeline would pick the other mode",
			len(wpts.Rte), len(wpts.Trk))
	}

	_, route := renderGPX(t, res, gpxRoute)
	if len(route.Rte) != 1 || len(route.Trk) != 0 {
		t.Errorf("route file holds %d rte and %d trk; a trk would win outright", len(route.Rte), len(route.Trk))
	}

	_, track := renderGPX(t, res, gpxTrack)
	if len(track.Trk) != 1 || len(track.Wpt) != 0 || len(track.Rte) != 0 {
		t.Errorf("track file holds %d trk, %d wpt, %d rte; it should hold only the track",
			len(track.Trk), len(track.Wpt), len(track.Rte))
	}
}

// In waypoints-only mode every <wpt> becomes a routing via-point, so anything that isn't a stop
// bends the route and clutters the device's editor.
func TestWaypointsFileHoldsStopsAndNothingElse(t *testing.T) {
	res := gpxFixture(t)
	_, doc := renderGPX(t, res, gpxWaypoints)

	if len(doc.Wpt) != 2 {
		t.Fatalf("want the 2 charge stops, got %d: %v", len(doc.Wpt), doc.Wpt)
	}
	for _, w := range doc.Wpt {
		if strings.Contains(w.Name, "backup") || strings.Contains(w.Name, "Somewhere else") {
			t.Errorf("a non-stop reached the waypoints file and would become a via-point: %q", w.Name)
		}
	}
}

// Backups belong in the route file, where they are markers rather than via-points: the device
// cannot search for a charger, and a waypoint can be skipped but never added.
func TestRouteFileCarriesBackupsAsMarkers(t *testing.T) {
	res := gpxFixture(t)
	_, doc := renderGPX(t, res, gpxRoute)

	var names []string
	for _, w := range doc.Wpt {
		names = append(names, w.Name)
	}
	joined := strings.Join(names, " | ")
	if !strings.Contains(joined, "backup") {
		t.Errorf("the route file should carry the backup charger: %s", joined)
	}
	if strings.Contains(joined, "Somewhere else") {
		t.Errorf("an alternate is not a backup and should not be exported: %s", joined)
	}
	// The route itself is a corridor, not a path: sparse points that pin our road choices.
	if n := len(doc.Rte[0].Rtept); n != 4 {
		t.Errorf("want source + 2 handoff waypoints + destination = 4 rtept, got %d", n)
	}
}

// The name is the one channel onto the device's own screen, so the numbers must survive even when
// the site name cannot.
func TestWaypointNameLeadsWithTheDecisionAndKeepsItsNumbers(t *testing.T) {
	res := gpxFixture(t)
	_, doc := renderGPX(t, res, gpxWaypoints)

	first := doc.Wpt[0].Name
	for _, want := range []string{"6x 6.5kW", "95 min", "arrive 27%"} {
		if !strings.Contains(first, want) {
			t.Errorf("name %q lost %q", first, want)
		}
	}
	if !strings.HasPrefix(first, "Ramona") {
		t.Errorf("name should lead with where it is, got %q", first)
	}

	// A name long enough to need truncating gives way at the name, never at the facts.
	long := charger{Name: strings.Repeat("Extremely Long Charger Site Name ", 3),
		Ports: 2, PowerKW: 50, SocArrival: 0.4, DwellMin: 25, Stop: true}
	got := wptName(long)
	if len([]rune(got)) > wptNameBudget+2 { // +2 for the ellipsis and rounding slack
		t.Errorf("name is %d runes, over the ~%d budget: %q", len([]rune(got)), wptNameBudget, got)
	}
	for _, want := range []string{"2x 50kW", "25 min", "arrive 40%"} {
		if !strings.Contains(got, want) {
			t.Errorf("truncation ate a number: %q lost %q", got, want)
		}
	}
}

// Beeline reads addresses into its editor and keeps them on re-export, so a stop we have an address
// for must never ship without one.
func TestEveryWaypointCarriesItsAddress(t *testing.T) {
	res := gpxFixture(t)
	_, doc := renderGPX(t, res, gpxWaypoints)
	for _, w := range doc.Wpt {
		if w.Desc == "" {
			t.Errorf("%q has no address", w.Name)
		}
		if w.Cmt == "" {
			t.Errorf("%q has no comment, so arrival and departure charge are nowhere", w.Name)
		}
	}
	if !strings.Contains(doc.Wpt[0].Cmt, "depart 80%") {
		t.Errorf("comment should state what the rider leaves with: %q", doc.Wpt[0].Cmt)
	}
	if !strings.Contains(doc.Wpt[0].Cmt, "Next leg") {
		t.Errorf("comment should state what the next leg costs: %q", doc.Wpt[0].Cmt)
	}
}

// A trackpoint every few metres is wasted bytes on a 53 mm screen, but the ride must still start
// and finish where it did.
func TestTrackIsDecimatedButKeepsItsEnds(t *testing.T) {
	res := gpxFixture(t)
	_, doc := renderGPX(t, res, gpxTrack)

	pts := doc.Trk[0].Trkseg[0].Trkpt
	if len(pts) >= len(res.Polyline) {
		t.Errorf("track was not decimated: %d points from %d", len(pts), len(res.Polyline))
	}
	if len(pts) < 2 {
		t.Fatalf("track collapsed to %d points", len(pts))
	}
	if pts[0].Lon != res.Polyline[0][0] {
		t.Error("the track no longer starts where the ride does")
	}
	if pts[len(pts)-1].Lon != res.Polyline[len(res.Polyline)-1][0] {
		t.Error("the track no longer ends where the ride does")
	}
	if len(doc.Trk[0].Trkseg) != 1 {
		t.Errorf("want a single trkseg, got %d", len(doc.Trk[0].Trkseg))
	}
}

// Five decimal places is about a metre. More is noise in a file with a hard size limit.
func TestCoordinatesAreFiveDecimals(t *testing.T) {
	res := gpxFixture(t)
	for _, kind := range []gpxKind{gpxWaypoints, gpxRoute, gpxTrack} {
		raw, _ := renderGPX(t, res, kind)
		for _, line := range strings.Split(raw, "\n") {
			if !strings.Contains(line, `lat="`) {
				continue
			}
			frac := strings.SplitN(strings.SplitN(line, `lat="`, 2)[1], `"`, 2)[0]
			if i := strings.Index(frac, "."); i >= 0 && len(frac)-i-1 != 5 {
				t.Errorf("%s: %q is not 5 decimal places", kind, frac)
			}
		}
	}
}

// The checklist item: the longest ride we will ever plan has to fit, in every variant. Measured
// density on a real 107.8 km loop was 1,714 polyline points — about 16 per km — so the 500 km
// ceiling in planRequest is roughly 8,000 points. That lands around half a megabyte, which says the
// 4 MB limit is nowhere near binding for a real ride.
func TestLongestRealisticRideFitsInEveryVariant(t *testing.T) {
	res := gpxFixture(t)
	res.Polyline = nil
	for i := 0; i < 8000; i++ { // 500 km at the observed ~62 m between points
		res.Polyline = append(res.Polyline, []float64{-116.8 + float64(i)*0.00066, 33.0, float64(i % 500)})
	}
	for _, kind := range []gpxKind{gpxWaypoints, gpxRoute, gpxTrack} {
		var b strings.Builder
		if err := writePlanGPX(&b, res, kind); err != nil {
			t.Fatalf("%s: the longest realistic ride should fit: %v", kind, err)
		}
		if b.Len() > gpxMaxBytes/2 {
			t.Errorf("%s is %.2f MB, uncomfortably close to the %d MB limit",
				kind, float64(b.Len())/(1<<20), gpxMaxBytes>>20)
		}
	}
}

// Four megabytes is hard: over it the import fails outright, so the writer refuses rather than
// handing over a file that cannot be opened.
func TestOversizeExportIsRefusedNotTruncated(t *testing.T) {
	res := &planResult{Mode: "loop"}
	// Far beyond anything plannable: 600k points at ~4.7 m, which survives decimation as ~86k.
	for i := 0; i < 600_000; i++ {
		res.Polyline = append(res.Polyline, []float64{-116.8 + float64(i)*0.00005, 33.0, 100})
	}
	err := writePlanGPX(io.Discard, res, gpxTrack)
	if err == nil {
		t.Fatal("an oversize export should be refused, not silently shipped")
	}
	if !strings.Contains(err.Error(), "limit") {
		t.Errorf("the error should say what the limit is: %v", err)
	}
}

func TestGPXKindParsing(t *testing.T) {
	for in, want := range map[string]gpxKind{
		"": gpxTrack, "track": gpxTrack, "waypoints": gpxWaypoints, "route": gpxRoute,
		"  Waypoints  ": gpxWaypoints,
	} {
		if got, ok := gpxKindOf(in); !ok || got != want {
			t.Errorf("%q: got %q ok=%v, want %q", in, got, ok, want)
		}
	}
	if _, ok := gpxKindOf("breadcrumb"); ok {
		t.Error("an unknown mode should be refused, not guessed at")
	}
	// The bare .gpx has always been a track; changing that would alter every existing Share GPX.
	if got, _ := gpxKindOf(""); got != gpxTrack {
		t.Errorf("default must stay track, got %q", got)
	}
}

func TestFileNamesSayWhichModeTheyAreFor(t *testing.T) {
	for _, kind := range []gpxKind{gpxWaypoints, gpxRoute, gpxTrack} {
		got := gpxName("abc123", kind)
		if !strings.HasSuffix(got, string(kind)+".gpx") {
			t.Errorf("%q does not say it is the %s file", got, kind)
		}
	}
}

func TestClimbReadsAsAHeight(t *testing.T) {
	for in, want := range map[float64]string{0: "0", 999: "999", 1000: "1,000", 2138: "2,138", 12345: "12,345"} {
		if got := commas(in); got != want {
			t.Errorf("commas(%v) = %q, want %q", in, got, want)
		}
	}
}
