package main

import (
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// Fixtures: gh_palomar.json is the 142 km Ramona → Palomar loop serpentine-api chose on
// 2026-09-18; nrel_palomar.json is NREL nearby-route (2 mi, L2 J1772/Tesla) for that polyline.

func TestEnergyMatchesSRSSpec(t *testing.T) {
	// Flat highway at 113 km/h for 116 mi, and flat city at 40 km/h for 171 mi, should each use
	// the whole 15.1 kWh nominal pack, plus the safety margin.
	hwy := segmentKWh(116*1.609344, 113, 0)
	city := segmentKWh(171*1.609344, 40, 0)
	want := usableKWh * consumptionMargin
	for name, got := range map[string]float64{"highway": hwy, "city": city} {
		if math.Abs(got-want) > 0.01 {
			t.Errorf("%s: %.2f kWh, want %.2f", name, got, want)
		}
	}
	// 1000 m of climbing costs about a kWh; the same descent returns about a quarter of it.
	if c := segmentKWh(0, 60, 1000) / consumptionMargin; c < 0.9 || c > 1.2 {
		t.Errorf("1000 m climb = %.2f kWh", c)
	}
	if up, down := segmentKWh(0, 60, 1000), segmentKWh(0, 60, -1000); -down > up/2 {
		t.Errorf("regen %.2f should be well under climb %.2f", -down, up)
	}
	if chargeMinutes(0.2, 0.9, 19.2) != chargeMinutes(0.2, 0.9, 0) {
		t.Error("the bike's 6.6 kW onboard charger caps any station, known or unknown power")
	}
	if m := chargeMinutes(0.2, 0.9, 6.6); m < 100 || m > 120 {
		t.Errorf("20→90 %% at 6.6 kW took %.0f min, want ~107", m)
	}
}

func palomarSites(t *testing.T) (*ghPath, []float64, []charger) {
	t.Helper()
	p := fixturePath(t, "gh_palomar.json")
	stations, err := parseNREL(loadFixture(t, "nrel_palomar.json"))
	if err != nil {
		t.Fatal(err)
	}
	cum := cumulativeKM(p.Points.Coordinates)
	sites := sitesFromStations(stations)
	placeOnRoute(sites, p.Points.Coordinates, cum)
	return p, cum, sites
}

func TestSitesMergePedestals(t *testing.T) {
	stations, _ := parseNREL(loadFixture(t, "nrel_palomar.json"))
	_, _, sites := palomarSites(t)
	if len(sites) >= len(stations) {
		t.Errorf("%d stations should merge into fewer sites, got %d", len(stations), len(sites))
	}
	for i, s := range sites {
		if s.Ports == 0 || len(s.Connectors) == 0 {
			t.Errorf("site %s has no usable ports", s.Name)
		}
		if i > 0 && s.KMFromStart < sites[i-1].KMFromStart {
			t.Error("sites must be in route order")
		}
		if s.OffRouteKM > chargerCorridorMiles*1.609344+0.5 {
			t.Errorf("%s is %.1f km off route, outside the corridor", s.Name, s.OffRouteKM)
		}
		for _, c := range s.Connectors {
			if c != "J1772" && c != "TESLA" {
				t.Errorf("%s lists %s, which the SR/S can't use", s.Name, c)
			}
		}
	}
}

func opts(start, min, to float64) chargingOpts {
	return chargingOpts{Enabled: true, SocStart: &start, SocMinArrival: &min, ChargeTo: &to}
}

func TestPlanChargingPalomar(t *testing.T) {
	p, cum, sites := palomarSites(t)
	energy := energyProfile(p, cum)
	if kwh := energy[len(energy)-1]; kwh < 8 || kwh > usableKWh*1.5 {
		t.Fatalf("Palomar loop estimate %.1f kWh is implausible", kwh)
	}

	// Starting half full, the loop can't be done without a stop.
	sum := planCharging(sites, energy, float64(p.Time)/1000, opts(0.5, 0.15, 0.9))
	if !sum.Feasible || sum.Stops == 0 {
		t.Fatalf("half pack: want a feasible plan with a stop, got %+v", sum)
	}
	for _, c := range sites {
		if c.Stop && (c.SocArrival < 0.15 || c.DwellMin <= 0) {
			t.Errorf("stop %s: arrival %.2f, dwell %.0f", c.Name, c.SocArrival, c.DwellMin)
		}
	}
	if sum.SocEndEst < 0.15 || sum.TotalTimeS <= float64(p.Time)/1000 {
		t.Errorf("end soc %.2f, total time %.0f s vs ride %.0f s", sum.SocEndEst, sum.TotalTimeS, float64(p.Time)/1000)
	}

	// Nearly empty in Ramona: the chargers 0.3 km away are the first stop.
	_, _, sites = palomarSites(t)
	sum = planCharging(sites, energy, float64(p.Time)/1000, opts(0.16, 0.15, 0.9))
	first := -1.0
	for _, c := range sites {
		if c.Stop {
			first = c.KMFromStart
			if c.SocArrival < 0.15 || c.SocArrival > 0.16 {
				t.Errorf("first stop must keep its arrival estimate (~0.16), got %.2f", c.SocArrival)
			}
			break
		}
	}
	if !sum.Feasible || first < 0 || first > 2 {
		t.Errorf("near-empty start should charge in Ramona first (stop at %.1f km), got %+v", first, sum)
	}

	// No chargers on the route: say so instead of pretending.
	sum = planCharging(nil, energy, float64(p.Time)/1000, opts(0.5, 0.15, 0.9))
	if sum.Feasible || sum.Warning == "" {
		t.Errorf("no chargers should be infeasible with a warning, got %+v", sum)
	}
}

func TestChargeStopBecomesHandoffWaypoint(t *testing.T) {
	p, cum, sites := palomarSites(t)
	planCharging(sites, energyProfile(p, cum), 0, opts(0.5, 0.15, 0.9))
	var stops []charger
	var forced []forcedWaypoint
	for _, c := range sites {
		if c.Stop {
			stops = append(stops, c)
			forced = append(forced, forcedWaypoint{idx: c.idx, pt: c.LonLat, label: "Charge: " + c.Name})
		}
	}
	h := buildHandoff(p, cum, roadsOf(p, cum), forced)
	if len(h.Waypoints) > maxHandoffWaypoints {
		t.Errorf("%d waypoints exceeds the cap", len(h.Waypoints))
	}
	found := 0
	for i, r := range h.WaypointRoads {
		if strings.HasPrefix(r, "Charge: ") {
			found++
			if h.Waypoints[i] != stops[found-1].LonLat {
				t.Errorf("charge waypoint should be the charger itself")
			}
		}
	}
	if found != len(stops) {
		t.Errorf("%d charge stops, %d in handoff", len(stops), found)
	}
}

func TestPlanWithCharging(t *testing.T) {
	var calls atomic.Int32
	gh := fakeGH(t, nil, &calls)
	defer gh.Close()
	var sawKey atomic.Bool
	nrel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawKey.Store(r.Header.Get("X-Api-Key") == "test-key" && r.URL.Query().Get("api_key") == "")
		_ = r.ParseForm()
		if !strings.HasPrefix(r.PostForm.Get("route"), "LINESTRING(") || r.PostForm.Get("ev_connector_type") != "J1772,TESLA" {
			t.Errorf("unexpected NREL request: %v", r.PostForm)
		}
		_, _ = w.Write(loadFixture(t, "nrel_palomar.json"))
	}))
	defer nrel.Close()

	api := testServer(gh)
	defer api.Close()
	code, _ := post(t, api.URL+"/v1/plan", `{"mode":"point_to_point","start":[-117.2116,32.867],"end":[-116.4185,32.87],"charging":{"enabled":true}}`)
	if code != 503 {
		t.Errorf("no NREL key configured: got %d, want 503", code)
	}

	api2 := testServerWithNREL(gh, newNRELClient(nrel.URL, "test-key"))
	defer api2.Close()
	code, res := post(t, api2.URL+"/v1/plan", `{"mode":"point_to_point","start":[-117.2116,32.867],"end":[-116.4185,32.87],"charging":{"enabled":true,"soc_start":0.6}}`)
	if code != 200 || res["energy"] == nil || res["chargers"] == nil {
		t.Fatalf("got %d %v", code, res["error"])
	}
	if !sawKey.Load() {
		t.Error("NREL key must go in the X-Api-Key header, not the URL")
	}
}

func TestSelectChargersTrims(t *testing.T) {
	p, cum, sites := palomarSites(t)
	sum := planCharging(sites, energyProfile(p, cum), 0, opts(0.5, 0.15, 0.9))
	sel := selectChargers(sites)
	if len(sel) >= len(sites) {
		t.Errorf("selection %d should be smaller than %d sites", len(sel), len(sites))
	}
	stops, backups := 0, 0
	perBin := map[int]int{}
	for i, c := range sel {
		switch c.Role {
		case "stop":
			stops++
		case "backup":
			backups++
		case "alternate":
			perBin[int(c.KMFromStart/alternateBinKM)]++
		default:
			t.Errorf("%s has no role", c.Name)
		}
		if i > 0 && c.KMFromStart < sel[i-1].KMFromStart {
			t.Error("selection must stay in route order")
		}
	}
	if stops != sum.Stops {
		t.Errorf("every stop must be kept: %d of %d", stops, sum.Stops)
	}
	if backups == 0 || backups > backupsPerStop*sum.Stops {
		t.Errorf("backups %d, want 1..%d", backups, backupsPerStop*sum.Stops)
	}
	for b, n := range perBin {
		if n > alternatesPerBin {
			t.Errorf("bin %d has %d alternates", b, n)
		}
	}
}

func TestSiteQuality(t *testing.T) {
	good := charger{Ports: 8, PowerKW: 7.2, Hours: "24 hours daily", Connectors: []string{"J1772"}, OffRouteKM: 0.2}
	far := good
	far.OffRouteKM = 3
	tesla := good
	tesla.Connectors = []string{"TESLA"}
	hours := good
	hours.Hours = "7am-7pm M-F"
	for name, worse := range map[string]charger{"far": far, "tesla-only": tesla, "limited hours": hours} {
		if siteQuality(&worse) >= siteQuality(&good) {
			t.Errorf("%s should rank below a close, 24 h J1772 site", name)
		}
	}
}
