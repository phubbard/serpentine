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
- [x] `go build` produces one static binary, stdlib only (`net/http`, `encoding/json`, `log/slog`) — `server/`, 2026-09-18
- [x] `launchd` plist on axiom, same pattern as LM Studio's `ai.lmstudio.server.plist` — `make deploy`, KeepAlive verified
- [x] Out-and-back returns a different road home: second leg computed with a per-request
      `custom_model` that penalises edges within ~300 m of the outbound polyline (ADR-005, ADR-015)
- [x] Loops via candidate fan-out + scoring (ADR-010): no track/unpaved vertices, distance within
      ~10 % of target, falls back gracefully when a heading points off the map (3–7 %, 100–330 ms)
- [x] Handoff waypoints on significant roads, endpoints on public roads (ADR-013)
- [x] Caddy points `/v1/*` at serpentine-api (`infra/caddy/serpentine.caddyfile`) — live and verified 2026-09-18
- [x] `out_and_back` mode (ADR-015)
- [x] Browser test page at `/v1/` with a map: vendored Leaflet + caching OSM tile proxy (ADR-016)
- [x] Loops penalise repeated road (round_trip spurs; ADR-010 amendment, ADR-012 resolution)
- [ ] **Self-hosted vector tiles** replace the tile proxy: Protomaps PMTiles extract (California or
      us-west) on axiom, vendored MapLibre GL + style/glyphs/sprites, served under `/v1/` (ADR-016)
- [ ] Verify forest roads: a 150 km Ramona loop (seed 1) used Eagle Peak / Boulder Creek / Engineers
      Rd (FR 13S06/08/03), tagged asphalt in OSM but partly dirt in reality further along; and with
      `avoid: unpaved` it still carried 0.8 km dirt + 0.7 km gravel. Check against imagery; consider
      penalising `unclassified` roads with a forest-road ref or missing surface
- [x] Route cache keyed on request hash; repeat requests served in < 50 ms (in-memory, ~1 ms)
- [x] NREL key read from env or a mode-600 key file, never logged, never returned (ADR-014)
- [x] Charge-stop planning in `POST /v1/plan` (`charging`), stops become handoff waypoints
- [~] Energy model unit-tested against the SR/S spec numbers — **still needs one real ride log** (ADR-014);
      the bike arrives in a few weeks (early-to-mid October 2026)

## Phase 2 — `ios/`: SwiftUI app, TestFlight

- [x] Xcode project `net.phfactor.serpentine`, iOS 18.4+, SwiftUI + MapKit + CoreLocation, zero SPM
      (`ios/`, xcodegen, Swift 6 strict concurrency; builds and tests on iOS 27 and the 18.4 floor)
- [x] Plan screen: start (current location or search via `MKLocalSearch`), mode (loop / out-and-back),
      distance, "twistiness" slider (maps to per-request `custom_model`), direction, charging toggle +
      starting charge. *Still to do: A→B mode, time budget (phase 3), commute (phase 3).*
- [x] Result screen: MapKit polyline, distance / time / climb, energy + charge stops with dwell,
      charge-stop and turnaround pins, "Navigate in Apple Maps" (verified opening Maps with 10 stops
      in the simulator), "Share GPX", "Another" ride. *Still to do: colour segments by curvature.*
- [ ] Saved rides (local only, `Codable` to Application Support; no iCloud in v1)
- [x] Makefile + `apple-deployment-playbook.md` + `tools/set-testflight-notes.rb` copied from mapbook
      (iOS-only: `make build|test|run|upload-testflight|testflight-notes`, MIN_BUILDS=1)
- [x] Info.plist: location usage strings, `ITSAppUsesNonExemptEncryption=false`, category
      `public.app-category.navigation`, `PrivacyInfo.xcprivacy` (no tracking, no collected data)
- [x] App Store Connect app record for `net.phfactor.serpentine` ("Serpentine EV"), internal group
- [x] First TestFlight build uploaded: 0.1.0 (29), 2026-09-18
- [ ] Internal TestFlight build installed on Paul's phone; one real ride completed via handoff
      (bike arrives early-to-mid October 2026)

## Phase 2b — iPad and Mac

- [x] iPad: `TARGETED_DEVICE_FAMILY 1,2`, all four orientations, `NavigationSplitView` root (plan form
      in the sidebar, ride in the detail column; collapses to the old push on iPhone), 600 pt map on
      regular width. 13" App Store screenshots in `ios/AppStore/screenshots/13/` (2026-09-19).
      Ships with the next TestFlight build; iPad screenshots become mandatory from then on.
- [ ] **Mac via Catalyst** (one ASC listing, a `.pkg` beside the `.ipa`; mapbook is the template):
  - `project.yml`: `SUPPORTS_MACCATALYST: YES`, `DERIVE_MACCATALYST_PRODUCT_BUNDLE_IDENTIFIER: NO`
    (keep `net.phfactor.serpentine`), `MACCATALYST_DEPLOYMENT_TARGET` = the macOS twin of iOS 18.4
    (15.4). Native Catalyst, not "Designed for iPad": mapbook hit TCC requests silently no-opping in
    the compat layer.
  - New `Serpentine.entitlements`: `app-sandbox`, `network.client` (serpentine.phfactor.net +
    MapKit), `personal-information.location`. Without the location entitlement Catalyst TCC returns
    denied and the app never appears in Privacy & Security.
  - Makefile: `archive-testflight-mac` / `upload-testflight-mac` (`generic/platform=macOS,variant=Mac
    Catalyst`, `ExportOptions-TestFlight-Mac.plist`, `altool --type osx`), `MIN_BUILDS=2` for
    `testflight-notes`. Never run the iOS and Mac uploads in parallel (shared DerivedData).
  - ASC: add the macOS platform under App Information before the first `.pkg`, or it's rejected.
    Mac App Store only; no Developer-ID DMG channel, because the app is sold once through the Store.
  - Mac-specific checks: "Navigate in Apple Maps" opens Maps.app (the rider sends it to the phone
    from there), whether macOS honours `waypoint=` in the unified URL, ShareLink GPX → Finder,
    Wi-Fi location accuracy for "Use my location".
  - Polish: ⌘R plan / ⌘⇧R another via `.commands`, a sensible minimum window size, the sidebar
    toggle in the toolbar. Mac screenshots (2880×1800) for the listing.

## Phase 3 — Ride quality

- [ ] Traffic proxy v2: HPMS AADT for CA state highways conflated onto GraphHopper edges as a
      custom encoded value (`aadt_bucket`), or rejected with reasons in DECISIONS
- [ ] **Plan by time, not distance** ("I have 2 hours on Saturday"): `duration_s` in `POST /v1/plan`
      as an alternative to `distance_m`. The server seeds candidates from `duration_s` × a twisty-road
      speed (start ~55 km/h, then learn it from GraphHopper's own `time_s` on scored loops). It scores
      against the *total* time, including charging dwell and detours when `charging` is on, and
      rescales the winner by the time ratio. It aims under the budget, never over: overrunning by 20 min
      breaks a promise, finishing early doesn't. App: a Distance / Time switch on the plan form, a time
      slider (30 min – 8 h), and the result shows "1 h 52 of 2 h". GraphHopper's motorcycle times are
      optimistic on twisty roads, so calibrate against the first real ride logs (ADR-014).
- [ ] Time-boxed loops with a stop: "3 hours with lunch" → the above plus one amenity stop near the
      midpoint
- [ ] **Commute mode** (tester feedback 2026-09-19): saved Home and Work, "take me to work" / "take me
      home", and a *different* good route each day within a time budget over the fastest (e.g. +10 /
      +20 / +30 min). Server: `mode: commute` = A→B through a seeded via-point offset from the direct
      line (perpendicular offset scaled to the budget), scored on curvature within the time cap. Seed =
      day number so each day differs and the same day is repeatable. Check first whether GraphHopper's
      `alternative_route` works in LM/hybrid mode; if so, alternatives are cheaper than via-points.
      Variety: the app keeps the last ~5 commute polylines *on the device* and sends them as a penalty
      corridor (the out-and-back mechanism, ADR-015), so "not yesterday's roads" never needs server
      state. Home and Work stay on the phone (UserDefaults); the server sees only two points per request,
      the same as any A→B plan, so the privacy policy doesn't change. Morning and evening are just A↔B
      swapped. Charging is rarely relevant to a commute; leave it off by default.
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
