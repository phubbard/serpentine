package main

import (
	"math"
	"sort"
	"strings"
)

// The bike, carried in the plan request (ADR-025). The app owns the garage; the server stays
// stateless, so a rider's hand-edited numbers behave exactly like a catalogue entry. Omitting it
// keeps the Zero SR/S behaviour every build before 2026-09-20 relied on.
//
// Consumption is Wh/km rather than a range claim, because ranges are quoted against speeds
// manufacturers no longer publish (ADR-020) and Wh/km is what the model actually needs.
type vehicle struct {
	Name           string   `json:"name,omitempty"`
	Kind           string   `json:"kind,omitempty"` // "electric" (default); "combustion" is not planned for yet
	UsableKWh      float64  `json:"usable_kwh,omitempty"`
	CityWhPerKM    float64  `json:"city_wh_per_km,omitempty"`
	HighwayWhPerKM float64  `json:"highway_wh_per_km,omitempty"`
	ACkW           float64  `json:"ac_kw,omitempty"`
	DCkW           float64  `json:"dc_kw,omitempty"`
	Connectors     []string `json:"connectors,omitempty"`
}

// srs is the bike this app was built around, and the default when a request names no vehicle:
// 15.1 kWh nominal, 171 mi city / 116 mi highway, 6.6 kW onboard, J1772 (ADR-008, ADR-014).
func srs() vehicle {
	return vehicle{
		Name: "Zero SR/S", Kind: "electric", UsableKWh: 15.1,
		CityWhPerKM:    15.1 / (171 * 1.609344) * 1000,
		HighwayWhPerKM: 15.1 / (116 * 1.609344) * 1000,
		ACkW:           6.6,
		// TESLA is here because today's behaviour assumes the Tap adapter for Tesla destination
		// chargers (ADR-014). Adapters are really a fact about the rider, not the bike (ADR-020),
		// so the app's garage will send what its owner actually carries.
		Connectors: []string{"J1772", "TESLA"},
	}
}

// NREL's connector vocabulary, verified against the API 2026-09-19. NACS is not a value it accepts:
// Tesla hardware is "TESLA", split by charging level (ADR-020).
var nrelConnectors = map[string]bool{"J1772": true, "J1772COMBO": true, "CHADEMO": true, "TESLA": true}

// dcConnector reports whether a connector is a DC fast one.
func dcConnector(c string) bool { return c == "J1772COMBO" || c == "CHADEMO" }

func (v *vehicle) normalize() error {
	if v.Kind == "" {
		v.Kind = "electric"
	}
	if v.Kind != "electric" {
		return badf(`vehicle kind %q is not supported yet; only "electric"`, v.Kind)
	}
	if v.Name == "" {
		v.Name = "your bike"
	}
	if v.UsableKWh <= 0 || v.UsableKWh > 60 {
		return badf("vehicle usable_kwh must be between 0 and 60")
	}
	if v.CityWhPerKM <= 0 || v.HighwayWhPerKM <= 0 {
		return badf("vehicle needs city_wh_per_km and highway_wh_per_km above zero")
	}
	if v.CityWhPerKM > 600 || v.HighwayWhPerKM > 600 {
		return badf("vehicle consumption above 600 Wh/km is not a motorcycle")
	}
	if v.ACkW < 0 || v.ACkW > 25 || v.DCkW < 0 || v.DCkW > 400 {
		return badf("vehicle charge rates are out of range")
	}
	if len(v.Connectors) == 0 {
		v.Connectors = []string{"J1772"}
	}
	for _, c := range v.Connectors {
		if !nrelConnectors[c] {
			return badf("connector %q is not one NREL knows: J1772, J1772COMBO, CHADEMO, TESLA", c)
		}
	}
	// Sorted so two requests naming the same bike hash to the same cache key.
	sort.Strings(v.Connectors)
	// A bike that can only fast-charge would be sent to Level 2 posts it cannot use — the LiveWire
	// ONE is exactly this case. Refuse rather than plan a ride that strands someone (ADR-025).
	if v.ACkW < 2 && !v.acConnectors() {
		return badf("%s charges too slowly on AC to plan Level 2 stops, and DC fast charging isn't planned yet", v.Name)
	}
	return nil
}

// acConnectors reports whether any of the bike's connectors is one we currently search for: Level 2
// AC. DC planning is the next step and will widen this (ADR-025).
func (v *vehicle) acConnectors() bool {
	for _, c := range v.Connectors {
		if !dcConnector(c) {
			return true
		}
	}
	return false
}

// searchConnectors is what goes to NREL. Today only the AC ones, because station parsing reads
// Level 2 units; DC connectors are carried in the vehicle and ignored here until that changes.
func (v *vehicle) searchConnectors() string {
	var out []string
	for _, c := range v.Connectors {
		if !dcConnector(c) {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		out = []string{"J1772"}
	}
	return strings.Join(out, ",")
}

// kwhPerKM interpolates between the bike's city and highway consumption by posted speed.
func (v *vehicle) kwhPerKM(speedKMH float64) float64 {
	city, hwy := v.CityWhPerKM/1000, v.HighwayWhPerKM/1000
	switch {
	case speedKMH <= citySpeedKMH:
		return city
	case speedKMH >= highwaySpeedKMH:
		return hwy
	default:
		f := (speedKMH - citySpeedKMH) / (highwaySpeedKMH - citySpeedKMH)
		return city + f*(hwy-city)
	}
}

func (v *vehicle) segmentKWh(km, speedKMH, dEleM float64) float64 {
	e := km * v.kwhPerKM(speedKMH)
	if dEleM > 0 {
		e += dEleM * climbKWhPerM
	} else {
		e += dEleM * descentKWhPerM // negative: regen
	}
	return e * consumptionMargin
}

// chargeMinutes to go from socFrom to socTo at a station advertising stationKW (0 = unknown).
// Flat rate, deliberately: a real curve tapers, and the assumption is stated in the app (ADR-020).
func (v *vehicle) chargeMinutes(socFrom, socTo, stationKW float64) float64 {
	if socTo <= socFrom {
		return 0
	}
	kw := v.ACkW
	if stationKW > 0 {
		kw = math.Min(kw, stationKW)
	} else {
		kw = math.Min(kw, defaultStationKW)
	}
	if kw <= 0 {
		return 0
	}
	return (socTo - socFrom) * v.UsableKWh / (kw * chargeEfficiency) * 60
}
