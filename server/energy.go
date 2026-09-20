package main

// The energy model (ADR-008, ADR-014, ADR-025). Conservative on purpose: being wrong here loses
// trust faster than a bad route. The bike-specific half lives in vehicle.go; these are the physics
// and the safety margin. Fit them to a real ride log when one exists.

const (
	// Physics and margins, not bike specs — the bike comes in the request (ADR-025).
	citySpeedKMH    = 50.0 // at or below: the bike's city consumption
	highwaySpeedKMH = 100.0
	defaultSpeedKMH = 70.0 // when max_speed is missing

	// Lifting ~320 kg (bike + rider + gear) 1 m is 0.00087 kWh; /0.85 drivetrain. Regen on
	// descents returns ~30 % of the potential energy.
	climbKWhPerM   = 0.00103
	descentKWhPerM = 0.00026

	consumptionMargin = 1.10 // on everything above

	chargeEfficiency = 0.90
	defaultStationKW = 6.6 // when NREL doesn't publish power
)

// energyProfile returns cumulative kWh at every polyline vertex, floored at zero (regen on an
// opening descent can't charge a full pack).
func energyProfile(v *vehicle, p *ghPath, cum []float64) []float64 {
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
		out[i] = out[i-1] + v.segmentKWh(cum[i]-cum[i-1], speed[i-1], dEle)
		if out[i] < 0 {
			out[i] = 0
		}
	}
	return out
}
