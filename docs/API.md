# serpentine-api contract (phase 1)

Base: `https://serpentine.phfactor.net/v1` (LAN: `http://axiom:8990/v1`). JSON. **No auth**
(ADR-011). Coordinates are `[lon, lat]` everywhere (GraphHopper convention) **except** inside the
Maps URLs, which Apple and Google want as `lat,lon`. Implemented in `server/`; this file is the
contract the iOS app codes against — change both together.

Status: `point_to_point`, `loop` and `charging` implemented; `out_and_back` returns 400 until
built; `POST /chargers` not built.

## POST /plan

```json
{
  "mode": "loop" | "point_to_point" | "out_and_back",
  "start": [-116.868, 33.042],
  "end": [-116.60, 33.08],          // point_to_point only
  "distance_m": 150000,             // loop: 20 000 – 500 000
  "heading_deg": 90,                // loop: optional; omitted = try 8 compass directions
  "seed": 7,                        // loop: optional, default 1; change it for a different loop
  "twistiness": 0.5,                // 0..1, default 0.5 → per-request custom_model (ADR-009)
  "avoid": ["unpaved", "ferries"],  // default both; [] to allow them
  "charging": {                     // optional; omitted or enabled=false → no energy/chargers
    "enabled": true,
    "soc_start": 1.0,                // default 1.0
    "soc_min_arrival": 0.15,         // default 0.15; never plan below this
    "charge_to": 0.9                 // default 0.9
  }
}
```

Defaults are filled before hashing, so a request with explicit defaults is the same plan (same `id`)
as one that omits them.

Response (200):

```json
{
  "id": "1f0c…",                    // stable hash of the normalised request + profile version
  "mode": "loop",
  "distance_m": 141800, "time_s": 8700, "ascend_m": 2400,
  "polyline": [[lon, lat, ele], ...],
  "roads": [ { "name": "South Grade Road (CR S6)", "km": 11.2 }, ... ],      // in order, merged
  "instructions": [ { "text": "Turn left onto …", "distance_m": 1200, "time_s": 90, "sign": -2, "i": 812 } ],
  "stats": {
    "km": 141.8, "curvy_km": 28.0,
    "road_class_km": { "secondary": 80.1, "primary": 50.2, ... },
    "urban_density_km": { "rural": 120.0, "residential": 21.8 },
    "surface_km": { "asphalt": 130.0, "missing": 11.8 }
  },
  "loop": {                          // loop mode only
    "target_m": 150000, "heading_deg": 315, "seed": 6, "score": -0.103,
    "candidates": 16, "failed": 6, "requested_m": 112500
  },
  "energy": {                       // charging requests only
    "usable_kwh": 15.1, "kwh_est": 14.42, "soc_start": 0.6, "soc_min_arrival": 0.15, "charge_to": 0.9,
    "soc_end_est": 0.29, "feasible": true, "stops": 1, "charge_min": 98,
    "total_time_s": 13517,           // riding + charging + charger detours
    "warning": "..."                 // present when feasible is false
  },
  "chargers": [                      // charging requests only; every usable site, in route order
    { "id": "nrel:282931", "name": "…", "lonlat": [lon, lat], "address": "…", "network": "ChargePoint Network",
      "ports": 10, "power_kw": 6.5,  // advertised, 0 = unpublished; the bike charges at ≤ 6.6
      "connectors": ["J1772", "TESLA"], "hours": "24 hours daily", "pricing": "…",
      "km_from_start": 55.3, "off_route_km": 0.0, "soc_arrival_est": 0.26,
      "stop": true, "dwell_min": 98 }
  ],
  "handoff": {
    "apple_maps_url": "https://maps.apple.com/directions?source=lat,lon&waypoint=…&destination=lat,lon&mode=driving&avoid=tolls,highways",
    "google_maps_url": "https://www.google.com/maps/dir/?api=1&origin=…&waypoints=…|…&destination=…&travelmode=driving",
    "source": [lon, lat], "destination": [lon, lat],
    "waypoints": [[lon, lat], ...],  // ≤ 10
    "waypoint_roads": ["Pala Road", "Charge: <site name>", ...]   // charge stops always included
  },
  "gpx_url": "/v1/plan/1f0c….gpx"
}
```

`sign` is GraphHopper's turn code (-3 sharp left … 0 straight … 3 sharp right, 4 finish, 5 via, 6
roundabout). `i` indexes `polyline`.

Errors: `{"error": "..."}` with 400 (bad request / not implemented), 422 (the engine couldn't route
it — point off the map, or no loop from this start), 502 (routing engine or charger data down), 503
(charging requested but the server has no NREL key). An infeasible charge plan is still a 200:
`energy.feasible` is false and `energy.warning` says why.

The iOS app opens `apple_maps_url` with `UIApplication.shared.open` (it goes straight to Maps; see
ADR-012 for why pasting it into Safari behaves differently).

## GET /plan/{id}.gpx

The plan's track as GPX 1.1 with elevation, for Kurviger/Garmin users. Served from an in-memory
cache (24 h, 500 plans); 404 after that — request the plan again.

## GET /health

`{"ok": true, "version": "13-a0f5c3b", "graphhopper": "11.0", "graph_data_date": "…", "graph_import_date": "…", "chargers": true}`;
503 with `"ok": false` if GraphHopper is unreachable.

## Loop generation (ADR-010)

GraphHopper's `round_trip` makes a triangle of snapped vertices, overshoots distance by 10–130 % and
can snap onto tracks or fail near the coast/border. The API requests 75 % of the target from 16
candidates (8 headings × 2 seeds, or the requested heading ±30° × 2 seeds), scores each (lower is
better) —

```
5·track+service+unpaved + 2·city + 0.5·residential + 2·motorway+trunk − 1·curvy + 2·|km−target|/target
```

(each term a fraction of route length) — then rescales the winner's `round_trip.distance` once if
it is still more than 10 % off. Measured on axiom: 100–330 ms, within 3–7 % of target.

## Handoff waypoints (ADR-013)

One waypoint 1 km into each named road of ≥ 3 km on our route, one per road, none within 2 km of
the endpoints, at most 10 (longest roads kept). Apple has to drive that road to reach the stop.
Source and destination move to the first/last non-service road so Apple never says "walking
required". Verified by hand on the demo ride (ADR-012); the ≥ 12-waypoint cap is still unmeasured.

## Charge stops (ADR-014)

NREL `nearby-route` (2 mi corridor; public, open, Level 2, J1772 or Tesla) is called once per plan.
Pedestals within 150 m merge into one site. Energy is summed along the polyline (see ADR-014 for the
model); when the pack would end below `soc_min_arrival`, the stop is the furthest site still
reachable above it, charged to `charge_to` at `min(advertised kW, 6.6)`, up to 3 stops. The route
itself is not re-routed through the charger; the charger becomes a handoff waypoint and Apple routes
the detour. Detours are costed as 2 × off-route distance.
