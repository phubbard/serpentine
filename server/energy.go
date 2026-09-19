package main

import "math"

// SR/S energy model (ADR-008, ADR-014). Conservative on purpose: being wrong here loses trust
// faster than a bad route. Replace the constants with fitted values once a real ride log exists.

const (
	usableKWh = 15.1 // nominal pack, not the 17.3 kWh maximum

	// Zero's rated range on 15.1 kWh: 116 mi highway (~113 km/h), 171 mi city.
	highwayKWhPerKM = usableKWh / (116 * 1.609344) // 0.081
	cityKWhPerKM    = usableKWh / (171 * 1.609344) // 0.055
	citySpeedKMH    = 50.0                         // at or below: city rate
	highwaySpeedKMH = 100.0                        // at or above: highway rate
	defaultSpeedKMH = 70.0                         // max_speed missing

	// Lifting ~320 kg (bike + rider + gear) 1 m is 0.00087 kWh; /0.85 drivetrain. Regen on
	// descents returns ~30 % of the potential energy.
	climbKWhPerM   = 0.00103
	descentKWhPerM = 0.00026

	consumptionMargin = 1.10 // on everything above

	bikeChargeKW     = 6.6 // onboard charger, stock (ADR-008)
	chargeEfficiency = 0.90
	defaultStationKW = 6.6 // when NREL doesn't publish power
)

// flatKWhPerKM interpolates between the city and highway figures by posted speed.
func flatKWhPerKM(speedKMH float64) float64 {
	switch {
	case speedKMH <= citySpeedKMH:
		return cityKWhPerKM
	case speedKMH >= highwaySpeedKMH:
		return highwayKWhPerKM
	default:
		f := (speedKMH - citySpeedKMH) / (highwaySpeedKMH - citySpeedKMH)
		return cityKWhPerKM + f*(highwayKWhPerKM-cityKWhPerKM)
	}
}

func segmentKWh(km, speedKMH, dEleM float64) float64 {
	e := km * flatKWhPerKM(speedKMH)
	if dEleM > 0 {
		e += dEleM * climbKWhPerM
	} else {
		e += dEleM * descentKWhPerM // negative: regen
	}
	return e * consumptionMargin
}

// energyProfile returns cumulative kWh at every polyline vertex, floored at zero (regen on an
// opening descent can't charge a full pack).
func energyProfile(p *ghPath, cum []float64) []float64 {
	coords := p.Points.Coordinates
	speed := make([]float64, len(coords))
	for i := range speed {
		speed[i] = defaultSpeedKMH
	}
	for _, d := range p.Details["max_speed"] {
		if d.IsNum && d.Num > 0 {
			for i := d.From; i < d.To && i < len(speed); i++ {
				speed[i] = d.Num
			}
		}
	}
	out := make([]float64, len(coords))
	for i := 1; i < len(coords); i++ {
		dEle := 0.0
		if len(coords[i]) > 2 && len(coords[i-1]) > 2 {
			dEle = coords[i][2] - coords[i-1][2]
		}
		out[i] = out[i-1] + segmentKWh(cum[i]-cum[i-1], speed[i-1], dEle)
		if out[i] < 0 {
			out[i] = 0
		}
	}
	return out
}

// chargeMinutes to go from socFrom to socTo at a station advertising stationKW (0 = unknown).
func chargeMinutes(socFrom, socTo, stationKW float64) float64 {
	if socTo <= socFrom {
		return 0
	}
	kw := bikeChargeKW
	if stationKW > 0 {
		kw = math.Min(kw, stationKW)
	} else {
		kw = math.Min(kw, defaultStationKW)
	}
	return (socTo - socFrom) * usableKWh / (kw * chargeEfficiency) * 60
}
