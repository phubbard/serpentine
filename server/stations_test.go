package main

import (
	"math"
	"path/filepath"
	"testing"
	"time"
)

func station(id int, lon, lat float64) nrelStation {
	s := nrelStation{ID: id, Name: "test", Lon: lon, Lat: lat}
	s.EVChargingUnits = append(s.EVChargingUnits, struct {
		ChargingLevel string `json:"charging_level"`
		Connectors    map[string]struct {
			PowerKW   *float64 `json:"power_kw"`
			PortCount int      `json:"port_count"`
		} `json:"connectors"`
	}{ChargingLevel: "2", Connectors: map[string]struct {
		PowerKW   *float64 `json:"power_kw"`
		PortCount int      `json:"port_count"`
	}{"J1772": {PortCount: 2}}})
	return s
}

// The local search must answer the question the API used to: everything within the corridor, each
// carrying how far off the route it sits, in km (ADR-028).
func TestLocalCorridorSearchMatchesBruteForce(t *testing.T) {
	// A route heading east from Ramona, and stations scattered around it.
	route := [][]float64{{-116.868, 33.042}, {-116.80, 33.042}, {-116.70, 33.05}, {-116.60, 33.06}}
	var all []nrelStation
	id := 1
	for lon := -117.0; lon < -116.4; lon += 0.02 {
		for lat := 32.9; lat < 33.2; lat += 0.02 {
			all = append(all, station(id, lon, lat))
			id++
		}
	}
	store := newStationStore(filepath.Join(t.TempDir(), "s.json"))
	store.replace(all, time.Now())

	const corridorMiles = 2.0
	got := store.nearbyRoute(route, corridorMiles)

	// Brute force over the same sampled points the store uses.
	corridorKM := corridorMiles * 1.609344
	want := map[int]float64{}
	for _, s := range all {
		d := math.Inf(1)
		for _, p := range route {
			if x := haversineKM(p, []float64{s.Lon, s.Lat}); x < d {
				d = x
			}
		}
		if d <= corridorKM {
			want[s.ID] = d
		}
	}
	if len(got) != len(want) {
		t.Fatalf("found %d stations, brute force found %d", len(got), len(want))
	}
	seen := map[int]bool{}
	for _, s := range got {
		if seen[s.ID] {
			t.Errorf("station %d returned twice", s.ID)
		}
		seen[s.ID] = true
		if d, ok := want[s.ID]; !ok {
			t.Errorf("station %d is outside the corridor", s.ID)
		} else if math.Abs(s.OffRouteKM-d) > 0.02 {
			t.Errorf("station %d: off-route %.2f km, want %.2f", s.ID, s.OffRouteKM, d)
		}
		if s.OffRouteKM > corridorKM {
			t.Errorf("station %d is %.2f km out, beyond the %.2f km corridor", s.ID, s.OffRouteKM, corridorKM)
		}
	}
}

func TestStoreRoundTripsThroughDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stations.json")
	store := newStationStore(path)
	if ok, _ := store.canAnswer(); ok {
		t.Fatal("an empty store must not claim it can answer")
	}
	store.replace([]nrelStation{station(1, -116.8, 33.0), station(2, -116.9, 33.1)}, time.Now())
	// refresh writes the file; simulate that half without the network.
	if err := writeStations(path, store); err != nil {
		t.Fatal(err)
	}
	reopened := newStationStore(path)
	if err := reopened.loadFile(); err != nil {
		t.Fatal(err)
	}
	if reopened.count != 2 {
		t.Errorf("reloaded %d stations, want 2", reopened.count)
	}
	if ok, age := reopened.canAnswer(); !ok || age > time.Minute {
		t.Errorf("reloaded store: ok=%v age=%v", ok, age)
	}
}

// A nil store is the "no local copy configured" case and must simply decline.
func TestNilStoreDeclines(t *testing.T) {
	var s *stationStore
	if ok, _ := s.canAnswer(); ok {
		t.Error("a nil store said it could answer")
	}
}
