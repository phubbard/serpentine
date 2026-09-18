# serpentine-api contract (phase 1, draft)

Base: `https://serpentine.phfactor.net/v1`. JSON. Basic auth until the app has its own key scheme.
Coordinates are `[lon, lat]` in requests and responses (GraphHopper convention), **except** the
`apple_maps_url`, which Apple wants as `lat,lon`.

## POST /plan

```json
{
  "mode": "loop" | "out_and_back" | "point_to_point",
  "start": [-117.16, 32.72],
  "end": [-116.60, 33.08],          // point_to_point only
  "turnaround": [-116.60, 33.08],   // out_and_back only; or omit and give distance_m
  "distance_m": 150000,             // loop / out_and_back target
  "heading_deg": 90,                // loop initial direction, optional
  "seed": 7,                        // loop variety, optional
  "twistiness": 0.7,                // 0..1 → per-request custom_model tightening
  "avoid": ["unpaved", "ferries"],  // default both
  "charging": { "enabled": true, "soc_start": 0.95, "soc_min_arrival": 0.20 }
}
```

Response:

```json
{
  "distance_m": 151230, "time_s": 9800,
  "polyline": [[lon,lat,ele], ...],
  "legs": [ { "from": 0, "to": 812, "instructions": [ {"text": "...", "distance_m": 1200, "sign": 2} ] } ],
  "segments": [ { "i0": 0, "i1": 40, "curvature": 0.91, "max_speed_kmh": 72, "road_class": "secondary", "urban_density": "rural", "surface": "asphalt" } ],
  "energy": { "kwh_est": 9.4, "soc_end_est": 0.38 },
  "chargers": [ { "id": "nrel:12345", "name": "...", "lonlat": [..], "km_from_start": 71.2, "connector": "J1772", "level": 2, "ports": 2, "network": "ChargePoint", "soc_arrival_est": 0.41, "dwell_min_to_80": 55 } ],
  "handoff": {
    "apple_maps_url": "https://maps.apple.com/directions?source=32.72,-117.16&waypoint=...&destination=32.72,-117.16&mode=driving&avoid=tolls,highways&start=3",
    "waypoints": [[lon,lat], ...],   // ≤ 12, at divergence points
    "google_maps_url": "https://www.google.com/maps/dir/?api=1&origin=...&waypoints=...|...&destination=...&travelmode=driving"
  },
  "gpx_url": "/v1/plan/<hash>.gpx"
}
```

Errors: 400 with `{"error": "..."}`; 422 if no route within `routing.max_visited_nodes`.

## POST /chargers

Same `chargers` array for an arbitrary `polyline` + `soc_start`. Used when the user drags the route.

## GET /health

`{"ok": true, "graphhopper": "11.0", "graph_date": "2026-09-16", "nrel": "ok"}`

## Waypoint selection for handoff (the interesting bit)

Given our polyline P and Apple's own A→B route between successive chosen waypoints, pick the
minimum set of via-points such that Apple's route stays within ~200 m of P. Greedy: start with
[start, end]; ask GraphHopper for the *fastest* car route between each pair as a stand-in for what
Apple will do; where it diverges from P by > 200 m for > 1 km, insert a waypoint at the midpoint of
the divergent stretch on P; repeat until no divergence or 12 waypoints. Apple's actual behaviour is
measured in Phase 0 and this heuristic tuned to it.
