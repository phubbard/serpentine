# Roadmap

Phases are sequential; each has acceptance criteria that can be checked with a command or a ride.
Update the checkboxes as things land and note the commit.

## Phase 0 — Routing POC on axiom  *(in progress)*

- [x] GraphHopper 11 config + `serpentine.json` custom model (`infra/`)
- [x] Docker Desktop memory raised to ≥ 24 GB
- [x] Clean us-west import completes; `curl http://axiom:8989/health` → 200 (2026-09-18, ~25 min; graph persisted with `RAM_STORE`)
- [x] Seeded 150 km loop returns in < 3 s (35–200 ms; quality issues → ADR-010)
- [x] Caddy front works: `https://serpentine.phfactor.net/health` → 200 (2026-09-18; no auth by decision, path allowlist in `infra/caddy/`)
- [ ] Profile tuned on `/maps` against Palomar / Mesa Grande / 79 / 76 until three different seeded
      loops all look like rides Paul would choose. Tune as a per-request `custom_model` (ADR-009), save
      it as `infra/graphhopper/custom_models/tuning.json`, record the reasoning in `DECISIONS.md`.
- [ ] Hand-built Apple Maps `/directions` URL with 12–15 waypoints from a real loop: note the waypoint
      cap and how many legs Apple re-routes onto the wrong road. **This measurement decides how much
      of phase 3 is needed.**
      Use the demo ride: `tools/demo_route.py` prints the URL for `demo/utc-ramona-sunrise.json` (7 waypoints).
      *2026-09-18, preview only (ADR-012):* 7 waypoints accepted; Apple's avoid-highways route is 139 mi /
      3 h 25 min vs ours 139.8 mi / 3.4 h — same roads. Still to do: 12–15 waypoint cap, and a real ride
      to check that the stops-and-voice experience holds up on the road.

## Phase 1 — `server/`: serpentine-api (Go)

A thin API the phone talks to. Hides GraphHopper, NREL and any keys. Runs on axiom next to
GraphHopper; Caddy points at it instead of GraphHopper directly.

Endpoints (contract in `docs/API.md`):

- `POST /v1/plan` — A→B, loop, or out-and-back. Returns polyline, distance, time, turn list,
  per-segment attributes, an `apple_maps_url`, and a `waypoints` array chosen at route-divergence
  points (not evenly spaced) for the handoff.
- `POST /v1/chargers` — chargers along a polyline via NREL `nearby-route`, filtered to J1772 and
  TESLA Level 2, with a conservative arrival-SoC estimate per stop from the energy model.
- `GET /v1/health`

Acceptance:
- [ ] `go build` produces one static binary, stdlib only (`net/http`, `encoding/json`, `log/slog`)
- [ ] `launchd` plist on axiom, same pattern as LM Studio's `ai.lmstudio.server.plist`
- [ ] Out-and-back returns a different road home: second leg computed with a per-request
      `custom_model` that penalises edges within ~300 m of the outbound polyline (see DECISIONS ADR-005)
- [ ] Loops via candidate fan-out + scoring (ADR-010): no track/unpaved vertices, distance within
      ~10 % of target, falls back gracefully when a heading points off the map
- [ ] Route cache keyed on request hash; repeat requests served in < 50 ms
- [ ] NREL key read from env, never logged, never returned
- [ ] Energy model unit-tested against the SR/S spec numbers and one real ride log

## Phase 2 — `ios/`: SwiftUI app, TestFlight

- [ ] Xcode project `net.phfactor.serpentine`, iOS 18.4+, SwiftUI + MapKit + CoreLocation, zero SPM
- [ ] Plan screen: start (current location or search via `MKLocalSearch`), mode (loop / out-and-back /
      A→B), distance or time budget, "twistiness" slider (maps to per-request `custom_model`),
      "include charge stop" toggle
- [ ] Result screen: MapKit polyline with segments coloured by curvature, distance / time / est. kWh,
      charger pins with dwell estimate, "Navigate in Apple Maps" button (unified URL, `start=3`),
      "Copy GPX" for Kurviger/Garmin users
- [ ] Saved rides (local only, `Codable` to Application Support; no iCloud in v1)
- [ ] Makefile + `apple-deployment-playbook.md` + `tools/set-testflight-notes.rb` copied from mapbook
- [ ] Info.plist: location usage strings, `ITSAppUsesNonExemptEncryption=false`, category
      `public.app-category.navigation`, `PrivacyInfo.xcprivacy`
- [ ] Internal TestFlight build installed on Paul's phone; one real ride completed via handoff

## Phase 3 — Ride quality

- [ ] Traffic proxy v2: HPMS AADT for CA state highways conflated onto GraphHopper edges as a
      custom encoded value (`aadt_bucket`), or rejected with reasons in DECISIONS
- [ ] Time-boxed loops: "3 hours with lunch" → loop + one stop with amenities near the midpoint
- [ ] Route variety: seed rotation and "not this road again" exclusions
- [ ] Charger reliability: Open Charge Map as a second source; flag single-port sites

## Phase 4 — In-app turn-by-turn  *(only if phase 0's handoff measurement is bad)*

- [ ] Ferrostar (BSD) with `CustomRouteProvider` fed by serpentine-api (OSRM-format adapter)
- [ ] Self-hosted vector tiles (so the phone still talks to one host) — Protomaps `.pmtiles` on axiom
      behind Caddy is the likely shape
- [ ] Voice: `AVSpeechSynthesizer` first, pre-rendered neural phrases later

## Explicitly not planned

- Subscription, accounts, cloud sync, analytics of any kind
- Android (until iOS is done and someone asks)
- Live traffic (no free source exists; see FEASIBILITY.md §4)
- CarPlay screen (needs `com.apple.developer.carplay-maps` entitlement; audio via headset already works)
