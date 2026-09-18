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
