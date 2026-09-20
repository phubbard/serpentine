package main

import (
	"math"
	"testing"
)

func TestVehicleValidation(t *testing.T) {
	ok := func(mod func(v *vehicle)) *vehicle {
		v := srs()
		mod(&v)
		return &v
	}
	for _, tc := range []struct {
		name string
		v    *vehicle
		bad  bool
	}{
		{"the default bike", ok(func(v *vehicle) {}), false},
		{"a petrol bike isn't modelled yet", ok(func(v *vehicle) { v.Kind = "combustion" }), true},
		{"no battery", ok(func(v *vehicle) { v.UsableKWh = 0 }), true},
		{"a car-sized battery", ok(func(v *vehicle) { v.UsableKWh = 75 }), true},
		{"no consumption", ok(func(v *vehicle) { v.CityWhPerKM = 0 }), true},
		{"car-sized consumption", ok(func(v *vehicle) { v.HighwayWhPerKM = 700 }), true},
		{"a connector NREL doesn't know", ok(func(v *vehicle) { v.Connectors = []string{"NACS"} }), true},
		{"CCS is a known connector", ok(func(v *vehicle) { v.Connectors = []string{"J1772", "J1772COMBO"} }), false},
		// The LiveWire ONE: DC only, ~1.4 kW on AC. Planning Level 2 stops for it would strand a rider.
		{"DC-only bike", ok(func(v *vehicle) {
			v.Name, v.ACkW, v.DCkW, v.Connectors = "LiveWire ONE", 1.4, 13, []string{"J1772COMBO"}
		}), true},
	} {
		err := tc.v.normalize()
		if tc.bad && err == nil {
			t.Errorf("%s: accepted, want rejected", tc.name)
		}
		if !tc.bad && err != nil {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
}

func TestVehicleConnectorsAreSortedForTheCache(t *testing.T) {
	a, b := srs(), srs()
	a.Connectors = []string{"TESLA", "J1772"}
	b.Connectors = []string{"J1772", "TESLA"}
	if err := a.normalize(); err != nil {
		t.Fatal(err)
	}
	if err := b.normalize(); err != nil {
		t.Fatal(err)
	}
	ra := planRequest{Mode: "loop", Start: &[2]float64{-116.868, 33.042}, DistanceM: 100000, Vehicle: &a}
	rb := planRequest{Mode: "loop", Start: &[2]float64{-116.868, 33.042}, DistanceM: 100000, Vehicle: &b}
	if ra.cacheKey() != rb.cacheKey() {
		t.Error("the same bike listed in a different order must hash the same")
	}
}

// A thirstier bike with a smaller pack must need charging sooner. This is the whole point of the
// garage: the numbers come from the rider's machine, not from the one this app was written around.
func TestThirstierBikeNeedsMoreStops(t *testing.T) {
	p, cum, sitesA := palomarSites(t)
	_, _, sitesB := palomarSites(t)

	small := srs()
	small.Name, small.UsableKWh = "Can-Am Pulse", 8.9
	small.CityWhPerKM, small.HighwayWhPerKM = 8.9/(100*1.609344)*1000, 8.9/(80*1.609344)*1000
	if err := small.normalize(); err != nil {
		t.Fatal(err)
	}

	big := srs()
	sumBig := planCharging(&big, sitesA, energyProfile(&big, p, cum), 0, opts(1.0, 0.15, 0.9))
	sumSmall := planCharging(&small, sitesB, energyProfile(&small, p, cum), 0, opts(1.0, 0.15, 0.9))

	if sumSmall.UsableKWh != 8.9 || sumBig.UsableKWh != 15.1 {
		t.Fatalf("the summary must report the bike it planned for: %.1f and %.1f", sumSmall.UsableKWh, sumBig.UsableKWh)
	}
	if sumSmall.Stops <= sumBig.Stops && sumSmall.Feasible {
		t.Errorf("the smaller bike planned %d stops against the bigger bike's %d", sumSmall.Stops, sumBig.Stops)
	}
}

// A faster onboard charger must mean shorter stops, not different ones.
func TestFasterChargerShortensStops(t *testing.T) {
	slow, fast := srs(), srs()
	fast.ACkW = 12.6 // the Rapid Charge Module
	mSlow := slow.chargeMinutes(0.2, 0.9, 12.6)
	mFast := fast.chargeMinutes(0.2, 0.9, 12.6)
	if !(mFast < mSlow) {
		t.Errorf("12.6 kW took %.0f min, 6.6 kW took %.0f", mFast, mSlow)
	}
	// The station is still the limit: a 3 kW post charges at 3 kW whatever the bike can take.
	if got := fast.chargeMinutes(0.2, 0.9, 3.0); math.Abs(got-slow.chargeMinutes(0.2, 0.9, 3.0)) > 0.01 {
		t.Error("a slow station should cap both bikes equally")
	}
}
