package main

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// GPX export, shaped for the Beeline Moto II (ADR-039).
//
// The Beeline is the only navigation target that reaches us through a *file* rather than a URL, and
// it is the only one that cannot search for a charger on its own. So the file has to carry the plan,
// not just the shape of the road.
//
// Beeline offers three import modes that behave completely differently, and it picks the mode from
// what the file contains, in the order track → route → waypoints. A file holding a <trk> is treated
// as a track whatever else is in it. That is why this emits **one intent per file** rather than one
// rich file:
//
//	waypoints  <wpt> only   Beeline routes between our charge stops, with turn-by-turn. The default.
//	route      <rte>+<wpt>  Map-matched to Beeline's own network, so our road choices may be altered.
//	track      <trk> only   Exact path, no re-routing — and no turn prompts at all.
//
// Note that GPX 1.1's schema order (metadata?, wpt*, rte*, trk*) is a different thing from Beeline's
// mode precedence. Document order is fixed by the schema and must not be shuffled to try to steer
// which mode is chosen; separate files are the only honest lever.
type gpxKind string

const (
	gpxWaypoints gpxKind = "waypoints"
	gpxRoute     gpxKind = "route"
	gpxTrack     gpxKind = "track"
	// gpxAll is the one to share with a rider (ADR-041). Hardware showed that a device builds its
	// import menu from what the file holds rather than collapsing it to one intent, so a file with
	// both a track and the stops offers every mode and keeps the ride intact.
	gpxAll gpxKind = "all"

	// trackSpacingM decimates the breadcrumb. A trackpoint every few metres is wasted bytes on a
	// 53 mm screen; 30 m sits in the middle of the 25–50 m the device is happy with.
	trackSpacingM = 30.0

	// gpxMaxBytes is Beeline's hard per-file limit. Exceeding it is a failed import, not a
	// degraded one, so the writer refuses rather than shipping a file that cannot be opened.
	gpxMaxBytes = 4 << 20

	// gpxWaypointWarn is where waypoint counts stop being tested territory. There is no published
	// cap; this is the point at which we say so rather than pretend.
	gpxWaypointWarn = 25

	// wptNameBudget is roughly what survives in the device's waypoint list before truncation.
	wptNameBudget = 45
)

func gpxKindOf(s string) (gpxKind, bool) {
	switch gpxKind(strings.ToLower(strings.TrimSpace(s))) {
	case gpxWaypoints:
		return gpxWaypoints, true
	case gpxRoute:
		return gpxRoute, true
	case gpxAll:
		return gpxAll, true
	case gpxTrack, "":
		// Unchanged default: a bare .gpx has always been a track, and other apps (Kurviger,
		// Garmin, Calimoto) take one happily. Changing it would silently alter every existing
		// "Share GPX" button on both clients.
		return gpxTrack, true
	}
	return "", false
}

// gpxName is the file's base name, which is what a rider sees when picking the file to import.
func gpxName(id string, kind gpxKind) string {
	return fmt.Sprintf("serpentine-%s-%s.gpx", id, kind)
}

// writePlanGPX renders one plan as one intent. It buffers through a counting writer so the size
// limit is enforced on what is actually produced rather than on an estimate.
func writePlanGPX(w io.Writer, res *planResult, kind gpxKind) error {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<gpx version="1.1" creator="serpentine" xmlns="http://www.topografix.com/GPX/1/1">` + "\n")
	fmt.Fprintf(&b, "  <metadata><name>%s</name></metadata>\n", esc(planTitle(res)))

	switch kind {
	case gpxWaypoints:
		writePoints(&b, rideStops(res))
	case gpxRoute:
		// Waypoints first: GPX 1.1 fixes the order as wpt*, then rte*, then trk*.
		writePoints(&b, chargerPoints(append(chargeStops(res), backups(res)...), res))
		writeRte(&b, res)
	case gpxTrack:
		writeTrk(&b, res)
	case gpxAll:
		// Schema order again: wpt* before trk*. The stops are the same ones the waypoints file
		// carries, so a rider who imports this as "waypoints only" gets exactly that file's ride.
		writePoints(&b, rideStops(res))
		writeTrk(&b, res)
	}
	b.WriteString("</gpx>\n")

	if b.Len() > gpxMaxBytes {
		return fmt.Errorf("gpx: %s export is %d bytes, over Beeline's %d-byte limit", kind, b.Len(), gpxMaxBytes)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func planTitle(res *planResult) string {
	switch res.Mode {
	case "loop":
		return "Serpentine loop"
	case "out_and_back":
		return "Serpentine out and back"
	default:
		return "Serpentine ride"
	}
}

// chargeStops is what the rider must actually stop at. Nothing else becomes a <wpt>: in
// waypoints-only mode every <wpt> is a routing via-point, so a shaping point would bend the route
// and clutter the device's editor.
func chargeStops(res *planResult) []charger {
	var out []charger
	for _, c := range res.Chargers {
		if c.Stop {
			out = append(out, c)
		}
	}
	return out
}

// backups ride along in the route file only. The device cannot search for a charger, and a waypoint
// can be skipped with the side buttons but never added, so a thin margin is worth a skippable
// marker. They are deliberately absent from the waypoints file, where they would become via-points
// and route the rider through a charger they did not plan to visit.
func backups(res *planResult) []charger {
	var out []charger
	for _, c := range res.Chargers {
		if !c.Stop && c.Role == "backup" {
			out = append(out, c)
		}
	}
	return out
}

// wptPoint is one <wpt> to emit: somewhere to be, what to call it, and whatever we can say about it.
type wptPoint struct {
	lonLat [2]float64
	name   string
	cmt    string
	desc   string
}

// rideStops is what a waypoints-only file must hold for the device to have a ride at all: where it
// begins, what it must visit in order, and where it ends.
//
// Charge stops alone are not a route. A one-stop ride exported as a single <wpt>, and a Beeline
// read it as 0.0 miles on the first hardware test (2026-10-21) — correctly, because one point has
// no length and no direction. ADR-039 reasoned that a shaping point would bend the route, which is
// true, and missed that a route needs endpoints before it can be bent.
//
// Both ends come from the handoff, which has already moved them onto public road (`publicEndpoints`)
// — a charger car park or a driveway is a bad place to ask a device to start. The middle comes from
// the handoff's forced waypoints, which are by construction exactly the charge stops and an
// out-and-back turnaround, already sorted into route order. Road-shaping waypoints stay out: each
// one is a via-point the rider would have to skip past, and holding the roads is what the route
// file is for.
func rideStops(res *planResult) []wptPoint {
	byLabel := make(map[string]charger, len(res.Chargers))
	for _, c := range res.Chargers {
		byLabel[chargeLabel(c)] = c
	}
	out := []wptPoint{{lonLat: res.Handoff.Source, name: "Start"}}
	for i, label := range res.Handoff.WaypointRoads {
		if i >= len(res.Handoff.Waypoints) {
			break // defensive: the two are built together and should never diverge
		}
		switch {
		case label == turnaroundLabel:
			out = append(out, wptPoint{lonLat: res.Handoff.Waypoints[i], name: turnaroundLabel})
		case strings.HasPrefix(label, chargePrefix):
			if c, ok := byLabel[label]; ok {
				out = append(out, chargerPoint(c, res))
			}
		}
	}
	// On a loop this repeats the start, which is what closes it; drop it and the device rides to
	// the last stop and stops there.
	return append(out, wptPoint{lonLat: res.Handoff.Destination, name: "Finish"})
}

func chargerPoint(c charger, res *planResult) wptPoint {
	return wptPoint{lonLat: c.LonLat, name: wptName(c), cmt: wptComment(c, res), desc: c.Address}
}

func chargerPoints(cs []charger, res *planResult) []wptPoint {
	out := make([]wptPoint, 0, len(cs))
	for _, c := range cs {
		out = append(out, chargerPoint(c, res))
	}
	return out
}

func writePoints(b *strings.Builder, pts []wptPoint) {
	for _, p := range pts {
		fmt.Fprintf(b, "  <wpt lat=\"%.5f\" lon=\"%.5f\">\n", p.lonLat[1], p.lonLat[0])
		// Schema order inside wptType: ele, time, …, name, cmt, desc, …
		fmt.Fprintf(b, "    <name>%s</name>\n", esc(p.name))
		if p.cmt != "" {
			fmt.Fprintf(b, "    <cmt>%s</cmt>\n", esc(p.cmt))
		}
		if p.desc != "" {
			// Beeline reads the address into its editor and keeps it on re-export, so this is
			// never left empty when we have one.
			fmt.Fprintf(b, "    <desc>%s</desc>\n", esc(p.desc))
		}
		b.WriteString("  </wpt>\n")
	}
}

// wptName is the one channel we have into the device's own screen, so it leads with what decides a
// rider's next move: where it is, how long they are stuck there, and what they arrive with. The
// numbers are never truncated — the site name gives way instead, because "Ramona Shopping Sun…" is
// still recognisable while "25 mi" cut in half is not.
func wptName(c charger) string {
	var facts []string
	if c.Ports > 0 && c.PowerKW > 0 {
		facts = append(facts, fmt.Sprintf("%dx %gkW", c.Ports, round1(c.PowerKW)))
	} else if c.Ports > 0 {
		facts = append(facts, fmt.Sprintf("%d ports", c.Ports))
	}
	if c.Stop {
		if c.DwellMin > 0 {
			facts = append(facts, fmt.Sprintf("%.0f min", c.DwellMin))
		}
		facts = append(facts, fmt.Sprintf("arrive %.0f%%", c.SocArrival*100))
	} else {
		facts = append(facts, "backup")
	}
	tail := " — " + strings.Join(facts, " — ")

	name := c.Name
	if room := wptNameBudget - len([]rune(tail)); len([]rune(name)) > room {
		if room < 8 { // nothing useful survives; keep the facts and a stub
			room = 8
		}
		name = strings.TrimRight(string([]rune(name)[:room-1]), " ,-") + "…"
	}
	return name + tail
}

// wptComment carries what will not fit in the name: the charge either side of the stop, and what
// the next leg costs. Beeline's own ETA excludes charging dwell and will always read optimistic, so
// the dwell is stated here as well as in the name.
func wptComment(c charger, res *planResult) string {
	if !c.Stop {
		return "Backup charger — only if the planned stop is dead or busy."
	}
	var parts []string
	if res.Energy != nil {
		parts = append(parts, fmt.Sprintf("Arrive %.0f%% / depart %.0f%%.",
			c.SocArrival*100, res.Energy.ChargeTo*100))
	}
	if km, climb, ok := legAfter(c, res); ok {
		parts = append(parts, fmt.Sprintf("Next leg %.0f mi, %s ft climb.", km/1.609344, commas(climb*3.28084)))
	}
	return strings.Join(parts, " ")
}

// legAfter measures from this stop to the next one, or to the end of the ride. Climb is summed from
// the polyline's own elevations rather than estimated, so it matches what the ride screen shows.
func legAfter(c charger, res *planResult) (km, climbM float64, ok bool) {
	coords := res.Polyline
	if c.idx <= 0 || c.idx >= len(coords) {
		return 0, 0, false
	}
	end := len(coords) - 1
	for _, o := range res.Chargers {
		if o.Stop && o.idx > c.idx && o.idx < end {
			end = o.idx
		}
	}
	for i := c.idx + 1; i <= end; i++ {
		km += haversineKM(coords[i-1], coords[i])
		if len(coords[i]) > 2 && len(coords[i-1]) > 2 {
			if d := coords[i][2] - coords[i-1][2]; d > 0 {
				climbM += d
			}
		}
	}
	return km, climbM, true
}

// writeRte emits the corridor, not the path. Beeline map-matches a route to its own network
// whatever we send, so the job of these points is to pin our road choices — which is exactly what
// the Apple/Google handoff waypoints already do (ADR-013), sparse and chosen by road length.
func writeRte(b *strings.Builder, res *planResult) {
	b.WriteString("  <rte>\n")
	fmt.Fprintf(b, "    <name>%s</name>\n", esc(planTitle(res)))
	pts := append([][2]float64{res.Handoff.Source}, res.Handoff.Waypoints...)
	pts = append(pts, res.Handoff.Destination)
	for _, p := range pts {
		fmt.Fprintf(b, "    <rtept lat=\"%.5f\" lon=\"%.5f\"></rtept>\n", p[1], p[0])
	}
	b.WriteString("  </rte>\n")
}

func writeTrk(b *strings.Builder, res *planResult) {
	b.WriteString("  <trk>\n")
	fmt.Fprintf(b, "    <name>%s</name>\n", esc(planTitle(res)))
	b.WriteString("    <trkseg>\n")
	for _, c := range decimate(res.Polyline, trackSpacingM) {
		if len(c) > 2 {
			fmt.Fprintf(b, "      <trkpt lat=\"%.5f\" lon=\"%.5f\"><ele>%.0f</ele></trkpt>\n", c[1], c[0], c[2])
		} else {
			fmt.Fprintf(b, "      <trkpt lat=\"%.5f\" lon=\"%.5f\"></trkpt>\n", c[1], c[0])
		}
	}
	b.WriteString("    </trkseg>\n  </trk>\n")
}

// decimate thins a polyline to roughly one point per spacingM, always keeping the first and last so
// the ride still starts and ends where it did.
func decimate(coords [][]float64, spacingM float64) [][]float64 {
	if len(coords) < 3 {
		return coords
	}
	out := [][]float64{coords[0]}
	acc := 0.0
	for i := 1; i < len(coords)-1; i++ {
		acc += haversineKM(coords[i-1], coords[i]) * 1000
		if acc >= spacingM {
			out = append(out, coords[i])
			acc = 0
		}
	}
	return append(out, coords[len(coords)-1])
}

// esc escapes text for XML content. encoding/xml does the work so the rules stay Go's and not ours.
func esc(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// commas groups thousands, because "2138 ft" is read as a number and "2,138 ft" is read as a height.
func commas(f float64) string {
	s := fmt.Sprintf("%.0f", f)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if neg {
		s = "-" + s
	}
	return s
}
