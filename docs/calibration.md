# Calibration: turning the estimates into measurements

Three numbers in this project started as honest guesses that had never met a real ride. They are all
written to err in the safe direction, which is not the same as being right.

**Status: one ride logged (2026-11-06), none of the three constants changed yet.** That ride is
written up below. It confirmed the *structure* of the time allowance and showed the remaining error
runs optimistic; it could not pin the rates, because a single Friday-afternoon Eco-mode ride
confounds time of day, ride mode and congestion in one number. The method, though, now works
end to end and is a single command — which is the thing that was actually missing.

## What to record

Ride any loop the app plans, with the plan saved so the predicted figures survive, and record:

| | why |
|---|---|
| The plan's `id`, and its `time_s`, `free_flow_time_s`, `delay_s`, `distance_m`, `stats` | `stats` carries the road-class × urban-density kilometres the time allowance is computed from, so the fit can be solved rather than guessed at. Keep the whole JSON. |
| Wall-clock start and finish, and **time spent stopped** (fuel, coffee, photographs, lights at a level crossing) | The allowance models riding, not the rider. Moving time is what to compare; total time without the stops subtracted is useless. |
| Indicated SoC at start and finish, to the percent | The energy model's only real check. |
| Charge stops: arrival SoC, departure SoC, minutes on the plug, and the station's rated kW | Separates a wrong consumption figure from a wrong charge-rate assumption. |
| Weather, roughly: temperature, wind, wet or dry | A cold headwind will not be in the model and should not be fitted into it. |
| **Ride mode** (Eco / Sport / Rain / Custom) | Eco caps acceleration and raises regen, so it moves *both* numbers at once: the same roads come out slower *and* cheaper than in Sport. The exported MBB log does not name the mode anywhere — this is the one field that has to be written down by hand. |

A GPX track log at 1 Hz with `<time>` on every point is worth more than everything above put
together, and `tools/calibrate_ride.py` turns one into the whole fit:

```
tools/calibrate_ride.py ride.gpx --mbb <MBB>.bin --bms <BMS>.bin --mode eco
```

It map-matches the track onto the production graph (`/match` on axiom:8989), so `road_class` and
`urban_density` come from the same source the planner uses, and prints measured delay per class
against the rates in `server/timing.go`. Given the Zero logs it also subtracts the time the bike
was keyed off and reads the SoC drop.

Two things that look like details and are not:

- **Map-match the track; do not re-plan the route.** The rates have to be fitted on the roads
  actually ridden. A ride the planner would not have chosen is the *most* informative kind, because
  it probes the classes where the desk estimate had least to go on.
- **Take distance, measured time and free-flow time from the same intervals.** Taking kilometres
  from the matched path and seconds from the track puts a different denominator under each and
  manufactures garbage — it produced a confident −84 s/km on one class and 70 s/km on a freeway
  before being caught. The tool does a single pass over track intervals for exactly this reason.

## First measured ride — 2026-11-06, San Diego, 42.6 km

The first ride logged against the model. A Beeline GPX at 1 Hz (3254 points, every one timestamped)
plus the bike's own MBB and BMS logs, **Eco mode**, Friday 15:00–16:14 local.

The bike log is what makes it a measurement rather than an anecdote. The track showed three stops
of 124 s, 106 s and 150 s between 23:07 and 23:11 UTC that could have been traffic signals; the MBB
log says `State change from RUN to STOP` at 23:07:05, `Hibernating for 3600 sec`, then `Board was
reset. Source: Hib Wake Pin` at 23:12:24. The bike was parked, and the Beeline — a separate device
with its own battery — kept logging. Without that, 6.2 minutes of parking would have been fitted
into the signal-delay rate.

Riding time, parking removed: **67.5 min against GraphHopper's 44.4 min free-flow, +52 %.** Our
allowance predicted 57.5 min, so it closes a bit over half the gap.

| class/density | km | measured s/km | model s/km | ratio |
|---|---|---|---|---|
| arterial/residential | 24.45 | 34.7 | 24.5 | **1.42** |
| arterial/rural | 8.91 | 9.8 | 5.0 | **1.97** |
| arterial/city | 3.10 | 48.4 | 36.0 | **1.34** |
| local/residential | 0.38 | 9.0 | 8.0 | 1.12 |
| freeway/residential | 1.66 | 1.2 | 0 | — |
| freeway/rural | 3.77 | 69.9 | 0 | — |

**What this settles.** The *shape* is right. `freeway/residential` measured 1.2 s/km against a
modelled 0, which is the grade-separated rule confirmed. The three arterial rows order correctly
(city > residential > rural) and all three want the same correction to within a factor of 1.5,
which is what you want to see from a model whose structure is sound and whose scale is off.

**What it does not settle: the rates.** Every one of those ratios is an *upper* bound, for three
independent reasons, and the tool cannot separate them from one ride:

1. **Friday 15:00–16:14 is the worst hour of the week** on a San Diego arterial. We have no
   time-of-day input and never will (principle 1 forbids live traffic), so the rates have to
   represent a *typical* ride, not the peak. Fitting them here would make every Sunday ride read late.
2. **Eco mode** caps acceleration, and the launch from a signal is precisely what the per-km signal
   term is modelling. Some unknown share of the 1.42 is the ride mode.
3. **`freeway/rural` is congestion, not structure.** That row is 5.4 km of I-805 ("Jacob Dekema
   Freeway", posted 104–106 km/h) ridden at a median 18 mph at 16:05 on a Friday. It contributes
   4.4 min of the 10 min residual all by itself, and it is the one thing here we deliberately refuse
   to model. Do not let it pull the arterial rates up.

So: **the rates were not revised on this ride.** What it establishes is that they are not
over-corrected — the suspicion that prompted the class × density split in ADR-045 — and that the
error that remains is in the optimistic direction, which is the wrong direction per ADR-018.

**Next:** the same loop in Sport on a Sunday morning, which separates causes 1 and 2 at a stroke,
and one rural-twisty ride, where `arterial/rural` carries the kilometres instead of 8.9 of them.

## 1. The ride-time allowance (ADR-045)

**Current values**, in `server/timing.go` — seconds of delay per kilometre:

| | city | residential | rural |
|---|---|---|---|
| arterial (trunk/primary/secondary/tertiary) | 36 | 24 | 5 |
| local streets | 10 | 8 | 3 |

plus 12 s/km over curvy road (curvature < 0.94), and nothing at all on grade-separated road.

**How to fit it.** `delay_s` is linear in the per-class kilometres, so each logged ride gives one
equation:

```
moving_time − free_flow_time_s  =  Σ (km in class c at density d) × rate[c][d]  +  curvy_km × rate_curvy
```

Two or three rides with genuinely different mixes — one urban, one rural-twisty, one mixed — are
enough to pin the rates that matter rather than solving a seven-unknown system from one ride. Fit
the arterial row first: it carries most of the error and most of the kilometres.

**What good looks like.** Predicted within ~10 % of moving time, and **biased late rather than
early** — ADR-018's rule is that coming in under is fine and running over is a broken promise, so a
tie goes to the slower estimate.

**Superseded external check.** Beeline quoted 26.1 mph for a 26.5 mi route around Del Mar on
2026-11-06, against our 26.3 mph for a 23.2 mi loop in the same area, and that agreement was the
only evidence the allowance had. The ride above measured the same corridor directly at **23.5 mph
including signals**, so the Beeline number is now the less reliable of the two: it was a different
route, and it is still **not known** whether their figure is traffic-aware. Two planners agreeing
was never calibration — it is two desk estimates landing in the same decade.

## 2. `budgetSpeedKMH` (ADR-018)

**Current value: 42 km/h**, in `server/plan.go`. It is only the *first* distance guess for a
`duration_s` request; `planForBudget` then rescales against the route's own corrected time, up to
three attempts. It still matters, because a guess that is badly high can run out of attempts while
still overrunning — which is exactly what 55 did once rides started being charged for signals.

**How to fit it.** Nothing to measure directly. Once the time allowance is fitted, take the low end
of the corrected average speed across a spread of real plans and use that. Low on purpose: the loop
grows a short ride happily and shrinks a long one grudgingly.

**Known rough edge.** The shortest loop the API will plan is 20 km (`minLoopM`), and in town 20 km
is over half an hour once the allowance is applied — so the shortest budget the API accepts, 30
minutes, can be unsatisfiable. The plan reports this in `budget.note` rather than overrunning
silently or routing below the documented minimum. If it turns out to annoy anyone, the fix is to
lower `minLoopM`, which is a loop-quality question, not a timing one.

## 3. The energy model (ADR-008, ADR-014)

**Current values**, in `server/energy.go` and the catalogue entry `zero-srs-2020`:

- `climbKWhPerM = 0.00103`, `descentKWhPerM = 0.00026` (~30 % regen)
- `consumptionMargin = 1.10`, `chargeEfficiency = 0.90`
- the 2020 SR/S at 14.4 kWh max / 12.2 nominal, 6.0 kW AC, J1772 only

The desk estimate for the 2020 bike is roughly 47 / 77 Wh/km city / highway, and the catalogue note
already suspects it of being about 5 % optimistic.

**How to fit it.** `SoC_start − SoC_finish` times usable kWh is the measured consumption. Compare it
with the plan's `energy` figure *before* `consumptionMargin` is applied, so the physics and the
safety margin are fitted separately — a margin that is quietly absorbing a modelling error is a
margin that is not there when it is needed. Climb and descent terms need a ride with real elevation
to separate them from the flat consumption rate; a flat ride cannot do it.

**What good looks like.** Predicted consumption within ~10 % of measured, and the margin still
intact on top. Per principle 4 this one is conservative on purpose: being wrong here loses trust
faster than a bad route, and an optimistic charge plan strands somebody.

**First measurement, 2026-11-06 (Eco).** The BMS logs SoC as integer percent every ~185 s, at
payload offset 20 of its type-`0x4b` record. Over the ride: **65 % → 49 %, 16 points**, which on the
12.2 kWh nominal pack is **1.95 kWh** over 42.6 km — about **46 Wh/km**.

The model predicts 2.81 kWh for the same route, or 2.55 kWh with `consumptionMargin` removed. So the
physics term alone is **~31 % high**, and the model as shipped is ~44 % high.

**This is not a reason to loosen it.** Four things have to be resolved first, and all of them push
the same way:

- **Eco mode** raises regen and caps acceleration. A Sport-mode ride on these roads would consume
  materially more, and Sport is what the model has to be safe for.
- **23.5 mph average with constant stop-start** is the most regen-favourable riding there is. Zero's
  161 mi city rating comes from a standardised cycle, not from Friday traffic.
- **Which capacity SoC is a percentage of** is unconfirmed. Against the 14.4 kWh max rather than the
  12.2 nominal, measured becomes 2.30 kWh and the pre-margin error drops to ~11 %. That single
  unresolved question moves the answer by a third.
- **Integer percent** on a 12.2 kWh pack is ±0.12 kWh per point, and the last 6.5 min of the ride
  went unlogged (SoC sat at 49 from 23:55), so the true drop may be 17 points.

The asymmetry matters: an over-cautious energy model costs somebody a charge stop they didn't need,
and an optimistic one leaves them on the hard shoulder. **Loosening it needs several rides including
Sport and sustained highway** — the 76.6 Wh/km highway figure has still never met a real ride at all.

**One tooling gap:** `/match` returned 2D coordinates, so the climb and descent terms could not be
checked here. Request elevation from the match endpoint before fitting those.

## Where the answers go

- New constants → the files above, with the measured basis in the comment, *not* a bare number.
- The reasoning and the measurement → a note on ADR-045 (time) or ADR-014 (energy) in
  `docs/DECISIONS.md`. Append; don't rewrite.
- The one-line status → `CLAUDE.md`, and the Memento page `/projects/serpentine.md`.
- Anything that cost more than ten minutes to find out → `infra/README.md`.
