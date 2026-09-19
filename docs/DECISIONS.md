# Decisions

Short ADRs. Append new ones; amend old ones with a dated note rather than rewriting.

## ADR-001 · 2026-09-17 · GraphHopper, self-hosted, not Valhalla/OSRM/hosted API

GraphHopper 11 custom models expose `curvature`, `max_speed`, `urban_density`, `road_class`,
`surface`, `average_slope` as JSON rules; `round_trip` and alternatives are built in; Kurviger is
proof the stack works for this exact use. Valhalla has no curvature costing (C++ fork needed); OSRM
Lua profiles can't see way geometry. Hosted Kurviger API needs a paid GraphHopper plan plus an
unpublished add-on and on-screen attribution. Axiom has 128 GB, so self-hosting is free.

## ADR-002 · 2026-09-17 · Axiom hosts it; Caddy on the Pi 5 fronts it

Fleet per Memento: Pi 5 (8 GB) is serve-only at best, NAS (32 GB, weak CPU) is the fallback,
axiom is the only box with headroom next to a 71 GB LLM. Nothing new opens on the UCG-Max; one more
Caddy site block on the existing 443.

## ADR-003 · 2026-09-17 · LM (hybrid) preparation, not CH, not pure flexible

CH would freeze the weighting and forbid per-request `custom_model`. Pure flexible A* on 200–300 km
loops takes seconds-to-tens-of-seconds. LM gives ~10× over flexible while allowing per-request
models that only tighten (multipliers ≤ 1). Consequence: every rule in `serpentine.json` is a
penalty; "prefer curvy" is written as "penalise straight".

## ADR-004 · 2026-09-18 · Apple Maps handoff for voice nav; Ferrostar deferred

No third-party access to Apple's guidance engine or Siri voice exists. iOS 18.4 unified URL supports
repeated `waypoint=` and `start=N`. Cost: Apple re-routes between waypoints, treats each as a stop,
~13-stop UI cap. Ship this first; measure how often Apple leaves the intended road (Phase 0). Only if
that measurement is bad, add Ferrostar (BSD, custom route provider, AVSpeech voice) — which drags in
MapLibre and therefore self-hosted tiles to preserve the one-host rule. Mapbox Nav SDK rejected:
removed custom routing providers in v3 and bills per MAU.

## ADR-005 · 2026-09-18 · Out-and-back = two routes with an area penalty  *(tentative)*

GraphHopper OSS has no `avoid_edges`. Plan: route out to the turnaround; compute the return with a
per-request `custom_model` using `in_area_*` rules over a buffered polygon of the outbound polyline
(GraphHopper supports `areas` in custom models), `multiply_by 0.1` inside the buffer. Verify that
`areas` works in LM mode as a tightening rule; if not, fall back to a heading-constrained
alternative. Revisit after Phase 0.

## ADR-006 · 2026-09-18 · Phone talks to one host; server in Go; MapKit for display

Paul's privacy rule: an app talks to the service it is for and nothing else. So NREL, GraphHopper,
elevation all sit behind `serpentine.phfactor.net`, and the map on the phone is MapKit (no Mapbox,
no MapLibre tiles from a CDN). Server is Go for a single static binary with stdlib HTTP, matching
the no-dependencies stance; Python stays for tooling. *Tentative on Go vs Flask — Paul to confirm.*

## ADR-007 · 2026-09-18 · Custom model file is `serpentine.json`

GraphHopper ships a built-in `motorcycle.json` and refuses a user file with the same name
(`Custom model file name 'motorcycle.json' is already used for built-in profiles`). Profile name in
the API stays `motorcycle`.

## ADR-008 · 2026-09-18 · SR/S energy model assumptions

17.3 kWh max / 15.1 kWh nominal; 116 mi highway / 171 mi city; J1772 AC only, 6.6 kW stock, 12.6 kW
with the Rapid Charger module but assume 6.6 kW at public L2 (most pedestals are 32–40 A); Tesla
destination chargers usable via Tesla Tap Mini (not Superchargers); no CCS until Zero's 2027 option.
Charger filter: NREL `ev_connector_type` in {J1772, TESLA} and `ev_charging_level` Level 2. Twisty
consumption assumed between city and highway figures until a ride log says otherwise.

## ADR-009 · 2026-09-18 · Tune per request; `serpentine.json` is a base profile

Supersedes the "changing multipliers = restart" assumption in CLAUDE.md. GraphHopper 11 writes
`name|Profile.getVersion()` for every profile into the graph properties; the version hashes the
profile hints, which include the resolved custom model. On load, any mismatch throws `Profiles do not
match` — a full re-import (~25 min), not a restart. `profiles_lm[].preparation_profile` shares
landmarks but the served profile is still hashed, so it doesn't help.

So: `serpentine.json` is a conservative base (access, surface, road class, density, curvature,
speed) changed only when re-importing anyway. All tuning — twistiness slider, per-ride preferences,
the Phase 0 "rides Paul would choose" work — is a per-request `custom_model` that serpentine-api
layers on. LM allows that as long as every multiplier is ≤ 1. Cost: LM's heuristic gets looser the
more a request tightens, so queries slow down; measured headroom is large (loops 35–200 ms vs 3 s
target). v0.2 of the base (this import) splits `TRACK` out to 0.1.

## ADR-010 · 2026-09-18 · Loops: generate candidates and score, don't trust one round_trip

GraphHopper's `round_trip` builds a triangle of beeline vertices and snaps each to the nearest
drivable edge. Observed on the first import: 150 km requests return 163–349 km; from downtown SD
every east/south heading fails because a vertex lands in Mexico (outside the extract); seed 1 heading
45 snapped a vertex onto Marron Valley Road (a border track) that no weighting change routed around;
Ramona loops were good rides but crossed Clairemont on Genesee. serpentine-api will fan out ~6–12
seed × heading requests (each 35–200 ms), request `details`, score each on the share of
track/unpaved/city/trunk distance and on distance error, retry with a scaled `round_trip.distance`
to hit the target within ~10 %, and return the best. The scoring rules are serpentine's, not
GraphHopper's, and they're the natural home for the traffic proxy later.

*Amended 2026-09-18:* round_trip can leave **spurs** — ride up a road to a turning point and back
(Palomar loop: 4.2 km each way; see ADR-012). `pass_through` doesn't prevent it without turn costs.
Stats now carry `repeated_km` (road ridden more than once, sampled every 50 m, 60 m match) and loop
candidates pay 5 × its fraction, the same weight as dirt.

## ADR-011 · 2026-09-18 · Public endpoint without auth; path allowlist instead

`serpentine.phfactor.net` fronts GraphHopper with no basic auth (Paul's call). The app has no
accounts, and a secret baked into an app binary isn't a secret anyway. Cost control comes from
exposing only the endpoints the phone needs (`/route /info /health /nearest`); the `/maps` UI,
`/spt`, `/isochrone` and `/mvt` stay LAN-only. GraphHopper's own guards (`routing.max_visited_nodes`
3 M, `non_ch.max_waypoint_distance` 1000 km) bound any single request. Revisit if logs show abuse:
Caddy has no built-in rate limiter, so the next step would be limits inside serpentine-api (Phase 1),
which replaces GraphHopper as the upstream here.

## ADR-012 · 2026-09-18 · First Apple Maps handoff measurement: handoff is viable

Opened the demo ride's unified `/directions` URL (7 hand-picked junction waypoints, `avoid=tolls,highways`)
on iOS from Safari. Apple accepted all 7 as stops (reverse-geocoded to street addresses), honoured
`avoid=highways` as the default option (a 2 h 52 min highway option offered second), and its route was
**139 mi / 3 h 25 min against GraphHopper's 139.8 mi / 3.4 h** — distance agreement under 1 % means it
followed our roads, Sunrise Hwy included. With waypoints at junctions where Apple's fastest route would
diverge, the handoff reproduces our route. Phase 4 (Ferrostar) stays deferred.

Gotcha: an endpoint inside the mall's private lot made Apple warn "Walking required to reach
destination". Handoff source/destination/waypoints must sit on public, named roads; serpentine-api
should snap them to an edge whose `road_class` isn't SERVICE (or use the first named street on the
polyline). Still unmeasured: behaviour at 12–15 waypoints, and how the stops feel under voice
guidance on an actual ride.

Testing gotcha: `maps.apple.com` is a universal link. Tapped from another app (Notes, Messages, a
web page on another domain) it opens the Maps app; pasted into Safari's address bar, or loaded in a
tab already on maps.apple.com, it stays in Safari as the web map with no "Open in Maps" button, and
the web map's routing differs (3 h 15 min vs the app's 3 h 25 min for the same URL). Only the app
result counts. The iOS app uses `UIApplication.shared.open(url)`, which goes straight to Maps.

*2026-09-18, 10 waypoints:* the API's Palomar loop (10 waypoints) was accepted as 10 stops, but Apple
measured 81 mi vs our 88 mi (−8 %), against < 1 % on the demo. Suspected: the South Grade / East Grade
waypoints sit 1 km into roads that start near CA-76, so Apple reaches them and turns back instead of
climbing Palomar. To confirm from Apple's step list; if so, place waypoints mid-road for long roads.

*Resolved 2026-09-18:* wrong suspect. Routing a near-shortest path (distance_influence 2000) through
the same 10 waypoints gives 80.5 mi — Apple's 81. Apple followed every stop, the Palomar climb
included; the gap was **our** route riding a 4.2 km out-and-back spur around a round_trip turning
point on E Valley Pkwy / Valley Center Rd, which Apple rightly skipped. Fixed in scoring (ADR-010
amendment). Useful method: "shortest path through our waypoints" predicts Apple's distance, so
any leg where it's much shorter than ours is a place Apple will leave our route.

## ADR-013 · 2026-09-18 · Handoff waypoints go into each significant road, not at divergences

API.md's first plan was a divergence search: route the *fastest car* path between waypoints as a
stand-in for Apple, insert waypoints where it leaves ours. There is no car profile in the graph and
adding one costs a re-import (ADR-009), and the demo ride showed a simpler rule is enough: a waypoint
a short way *into* each road Apple must use forces it onto that road. serpentine-api puts one 1 km
into each named road ≥ 3 km (one per road even when a short differently-named bridge splits it),
skips anything within 2 km of the endpoints, and keeps the 10 longest. Source/destination move to
the first/last non-service road. If the on-road test shows Apple straying on connectors, add a car
profile at the next re-import and revisit the divergence search.

## ADR-014 · 2026-09-18 · Charging: NREL via developer.nlr.gov, conservative SR/S energy model

NREL moved: `developer.nrel.gov` no longer resolves (2026-09); the same API and api.data.gov keys
answer at `developer.nlr.gov`. The gateway ignores an `api_key` in a POST form body, so the key goes
in `X-Api-Key` (also keeps it out of URLs and logs). It lives in `~/serpentine-api/nrel.key` on
axiom (mode 600), passed with `-nrel-key-file`; never in the repo or Memento. NREL now publishes
per-connector `power_kw`, contradicting FEASIBILITY.md's "unverified" note — but the SR/S's 6.6 kW
onboard charger caps everything anyway.

Energy model (amends ADR-008): per-km consumption interpolated by posted speed between Zero's city
rating (171 mi on 15.1 kWh, ≤ 50 km/h) and highway rating (116 mi, ≥ 100 km/h); climbing at
0.00103 kWh/m (320 kg, 85 % drivetrain); descents regenerate 0.00026 kWh/m (~30 %); +10 % on
everything; plan against 15.1 kWh nominal, never the 17.3 maximum. First real number: the 142 km
Palomar loop estimates 14.4 kWh (95 % of the pack), which is very likely pessimistic — SRTM's 3,150 m
of ascent is noise-inflated and 30 % regen is low. Deliberately conservative until a ride log from
the Zero app calibrates it; being wrong the other way strands the rider.

Stop choice: furthest reachable site (fewest stops, longest legs), ties to more ports. Alternatives
considered for later: prefer sites with food/amenities (Phase 3), prefer higher-power sites when the
bike has the Rapid Charger module (12.6 kW).

*Amended 2026-09-18:* `chargers` is a selection, not the corridor. A 150 km loop from UTC has 173
usable sites within 2 mi; the response now carries each stop, up to 2 backups within 10 km of it
along the route (the stop's ports may be taken), and the 2 best alternates per 20 km, ranked on
ports, power, 24 h access and off-route distance, Tesla-only sites lower (adapter). Each has a
`role`; `energy.chargers_nearby` keeps the full count. UTC loop: 173 → 16. Calibration waits for
the bike, which arrives in a few weeks (early-to-mid October 2026) — until then the model stays
deliberately pessimistic.

## ADR-015 · 2026-09-18 · Out-and-back via a corridor penalty on the return (resolves ADR-005)

ADR-005's plan works: GraphHopper accepts a per-request `areas` MultiPolygon (±300 m rectangles
every 0.5 km along the outbound polyline) and a `multiply_by` rule on it under LM, in ~130 ms. The
multiplier matters: at 0.1 Julian → Ramona came home via Alpine (105 km against 36 out); at 0.3,
0.5 and 0.7 it took Old Julian Hwy at the same 35 km. 0.3 is the default — strong enough to prefer a
parallel road, weak enough not to force absurd detours; shared road is reported (`shared_km`)
rather than forbidden. The first and last 2 km are left out of the corridor because they are
shared by necessity. Turnarounds for distance-only requests are fanned out like loops (ADR-010)
and scored with an extra shared-road term. Known bias: the return is usually longer than the out
leg, so totals land 6–13 % over target after one rescale; a second rescale or an asymmetric radius
could tighten that if it matters on the road.

## ADR-016 · 2026-09-18 · Test-page map: caching raster proxy now, self-hosted vector tiles later

GraphHopper's `/maps` UI loads tiles straight from tile.openstreetmap.org (`defaultTiles:
'OpenStreetMap'` in its config.js), which would hand every viewer's IP and viewport to a third party
and re-expose raw GraphHopper publicly — both against the one-host rule. Instead the test page draws
our own plan with Leaflet 1.9.4 (BSD-2, vendored from the npm tarball with integrity checked,
embedded at `/v1/static/`) over tiles from `/v1/tiles/{z}/{x}/{y}.png`: a disk cache on axiom that
fetches each tile once from OSM with an identifying User-Agent, ≤ 2 upstream connections,
single-flight per tile, serves stale on upstream failure, z ≤ 17. OSM sees axiom, never the viewer.
Tile paths are locations, so the request log records `/v1/tiles` without z/x/y. The page carries
OSM attribution.

It's a stopgap: the goal is self-hosted **vector** tiles (Protomaps PMTiles for California or
us-west, a few GB to low tens of GB; planet ~120 GB) rendered with vendored MapLibre GL, a
self-hosted style, glyphs and sprites — zero third-party requests ever, and the same stack Phase 4
(Ferrostar) would need. Roadmap item.

## ADR-017 · 2026-09-19 · No third party in the request path; minimal access log; privacy policy

Preparing the external-TestFlight privacy policy exposed two gaps between principle 1 and the
deployment. (1) Publicly, `serpentine.phfactor.net` was **Cloudflare-proxied** (orange cloud;
split-horizon DNS hid it on the LAN), so Cloudflare terminated TLS and could read every plan
request, start coordinates included, from any rider on cellular. Paul switched it to **DNS-only**
(grey cloud, CNAME → `webserver.phfactor.net`), like news/raven/ping: the home WAN IP was already
public through those, and small JSON gains nothing from a CDN. Cost: no Cloudflare DDoS absorption;
revisit with rate limits in serpentine-api if abused. (2) **Caddy's access log** kept full client IPs,
all headers and the test page's tile paths (map locations) under default retention. The site block now
filters it: IPs masked to /16 (v4) and /32 (v6), request/response headers and remote port dropped,
`/v1/tiles/z/x/y` collapsed to `/v1/tiles`, 30-day retention. Plan bodies were never logged.

The privacy policy lives at `https://serpentine.phfactor.net/v1/privacy` (`server/web/privacy.html`,
embedded) and describes exactly this: start point to our server, kept in memory ≤ 24 h
(`planCacheTTL`, tested against the page), route shape to NREL only when charging is on, Apple for
maps/search/navigation, truncated-IP 30-day access log. Any change to data flow must update the page.

## ADR-018 · 2026-09-19 · Time budgets are planned by correction, not by predicting speed

Riders have hours, not kilometres: "two hours on Saturday" is the real request. `duration_s` on a
`loop` or `out_and_back` now replaces `distance_m` (`docs/API.md`).

Predicting distance from a time budget needs a speed, and no single number works: the same two hours
buys 75 mi around Ramona and rather less on Palomar's switchbacks, and GraphHopper's motorcycle
speeds are themselves optimistic on tight roads. So the server doesn't try to be right first time. It
guesses low (`budgetSpeedKMH` = 55), plans, then rescales the distance by the ratio of the budget to
the *route's own* time and plans again — at most `budgetTries` (3) attempts, stopping as soon as the
ride lands inside the budget. The fan-out is cheap (35–200 ms a candidate), so worst case is about
three times a normal plan and comfortably inside the 60 s request timeout. Measured from Ramona:
90 min → 87 min ride, 2 h → 113 min, 4 h → 236 min.

Two rules make the result trustworthy. It aims at **97 % of the budget and prefers the longest ride
that still fits**, because finishing early is a mild disappointment and running over strands someone
in the dark. And with charging on, the budget counts **total** time — dwell and charger detours
included — so a ride needing a 90-minute stop shrinks rather than quietly costing 5½ hours; the
4-hour Ramona test came back 136 mi with one stop, 3 h 41 total.

The response's `budget` block (`target_s`, `total_s`, `fits`) exists so the app can say "1 h 52 of
2 h" without re-deriving anything, and so `fits: false` (shortest possible loop still too long)
is visible rather than silent.

Not done: the candidate *scoring* still uses the estimated distance, so a time-budget plan is scored
against a target that may be 10 % off. It matters little because the rescale fixes the winner, but if
time budgets become the common case, score against time directly. Calibrate `budgetSpeedKMH` from the
first real ride logs (ADR-014).

## ADR-019 · 2026-09-19 · A→B buys curves with a time budget; flat commutes have nothing to sell

Commute feedback ("given work and home, vary the route") starts with the simplest useful piece:
`point_to_point` plus `max_extra_s`, the detour the rider will accept over the quickest way.

The server can't ask GraphHopper for "curvy but no more than 15 minutes longer" — `twistiness` is a
priority multiplier, and how many minutes it costs depends entirely on the terrain between the two
points. So it measures instead: route the quick way as a baseline, then try the requested twistiness
and step it down (full, two thirds, one third) until one lands inside the cap, keeping the twistiest
that fits. Four routes at worst, each ~100–300 ms. The `detour` block reports `fastest_s`, `extra_s`
and the `twistiness` actually afforded, so the app can say what the curves cost.

**The measurement that matters is negative.** On the tester's real commute, downtown San Jose to
NVIDIA HQ in Santa Clara, every budget from 0 to 30 minutes returns the same 12-minute route with
0.1 km of curvy road. Los Gatos → NVIDIA is the same story. The valley is a street grid: there is no
better road to buy, so the budget is unspent. Where the terrain has something — Santa Cruz → NVIDIA
over CA 9 — the good roads are already on the fastest line (19 km of curves at +0 min) and the budget
adds a little more.

The consequence for the roadmap: **commute variety (slice 2) is only worth building for riders with
hills between home and work.** Route alternatives and day-rotation can't invent curves that the map
doesn't have. Before building it, ask the tester what their commute actually crosses; if it's valley
grid, the honest answer is that Serpentine has nothing to offer on that trip, and the feature should
target the weekend-ride-home case (a long way round, not a commute) instead.
