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
- [x] **Mac via Catalyst** — done 2026-09-19 (ADR-024): builds, runs and archives with no code changes;
      `make build-mac|archive-testflight-mac|upload-testflight-mac`. Waiting only on macOS being enabled
      in ASC → App Information before the first `.pkg` upload. As specified:
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

## Phase 2c — Operations: a dashboard worth trusting

Design in ADR-026. Counters only, no events, no rider in them; LAN-only by living outside `/v1/*`,
which the Pi's Caddy is the only thing that proxies.

- [x] **Counters in serpentine-api** — done 2026-09-20: hourly buckets for plans by mode/budget/charging, outcomes
      and cache hits, a latency histogram, upstream failures (GraphHopper, NREL, OCM), charge-plan
      feasibility, tile proxy hit rate. A test that asserts the metric struct holds no coordinates,
      no IPs and nothing per-ride — the guard rail matters more than the numbers.
- [x] **`/stats.json` and `/stats`** — done 2026-09-20 (verified LAN 200, public 404): the JSON plus a static embedded dashboard (no external
      anything) answering: is it up, is the graph current, rides today, p50/p95, what is failing, are
      the charger sources answering. Add the sentence to `/v1/privacy` when it ships.
- [ ] **Optional persistence** (~1 h): append the hourly aggregate to `~/serpentine-api/stats.jsonl`,
      rotate at 90 days, so a restart doesn't erase the week. Aggregates only, never events.

## Phase 3 — Ride quality

- [ ] Traffic proxy v2: HPMS AADT for CA state highways conflated onto GraphHopper edges as a
      custom encoded value (`aadt_bucket`), or rejected with reasons in DECISIONS
- [x] **Plan by time, not distance** ("I have 2 hours on Saturday") — server side, 2026-09-19 (ADR-018);
      app switch still to do: `duration_s` in `POST /v1/plan`
      as an alternative to `distance_m`. The server seeds candidates from `duration_s` × a twisty-road
      speed (start ~55 km/h, then learn it from GraphHopper's own `time_s` on scored loops). It scores
      against the *total* time, including charging dwell and detours when `charging` is on, and
      rescales the winner by the time ratio. It aims under the budget, never over: overrunning by 20 min
      breaks a promise, finishing early doesn't. App: a Distance / Time switch on the plan form, a time
      slider (30 min – 8 h), and the result shows "1 h 52 of 2 h". GraphHopper's motorcycle times are
      optimistic on twisty roads, so calibrate against the first real ride logs (ADR-014).
- [ ] Time-boxed loops with a stop: "3 hours with lunch" → the above plus one amenity stop near the
      midpoint
**Commute** (tester feedback 2026-09-19): "given work and home, vary the route". Split into slices so
the useful part ships first. Measured 2026-09-19: GraphHopper's `alternative_route` *does* work under
LM (3 paths plain, 2 once the twistiness model is applied, ~0.1 s), so alternatives come before
via-points. Home and Work live on the phone; the server keeps seeing two points per request, exactly
like any A→B plan, so the privacy policy is unaffected.

- [x] **Commute slice 1 — A→B with a detour budget** (2026-09-19, ADR-019): the app gets a destination and an "extra
      time you'll spend" control; `point_to_point` gains `max_extra_s`, routing the fastest line as a
      baseline and stepping twistiness down until the curvy route fits the cap. Useful on its own (the
      long-standing A→B item). **Finding: valley commutes have nothing to offer.** San Jose → NVIDIA HQ
      returns the same 12-minute route whatever the budget (0.1 km curvy); Los Gatos → NVIDIA likewise.
      Santa Cruz → NVIDIA over CA 9 has 19 km of curves and they're already on the fastest line. Slice 2
      is only worth building for riders with hills between home and work — confirm with the tester first.
**Parked 2026-09-19 — the commute use case evaporated on contact with the rider.** Asked directly, the
San Jose tester said: *"Oh I don't commute, I just go drive in the hills on weekends and free days…
I'm only a few miles from HQ so I drive a car for that."* That matches ADR-019's measurement (a valley
commute has no curves to sell) and removes the demand as well as the supply. Slice 1 shipped and is
useful on its own as A→B. Slices 2 and 3 below stay written down but unbuilt until a real rider asks
for them — which for this tester is weekend hill rides, i.e. loops, out-and-backs and time budgets,
all of which already exist.

- [ ] ~~**Commute slice 2 — a different route each day**~~ (parked; ~4–5 h if revived): score `alternative_route` candidates
      with `score.go`, reject any over the cap, rotate by day number (repeatable, differs Mon/Tue).
      Needs an alternatives fixture and an ADR. Fallback if 2 alternatives prove too few: seeded
      via-points offset from the direct line (+2–3 h).
- [ ] ~~**Commute slice 3 — commute polish**~~ (parked; ~3–4 h if revived): saved Home and Work (UserDefaults), to-work /
      to-home swap, and the phone sending its last ~5 simplified polylines so the server penalises them
      (reuse `corridorModel`, ADR-015, weaker multiplier; watch the 64 KB body cap). Then screenshots
      and the public pages.
- [ ] ~~**Commute tuning**~~ (parked with the above): our profile is tuned for empty
      mountain roads, and a "curvy" detour through town means lights and residential streets. Likely
      needs an urban_density penalty — per-request if possible, since a base-profile change costs a
      25-minute reimport per iteration (ADR-009). Whether a variant is *pleasant* is a judgment only
      riding it can settle.
- [ ] Route variety: seed rotation and "not this road again" exclusions
- [ ] Charger reliability: Open Charge Map as a second source; flag single-port sites

## Phase 3b — The garage: other bikes (and gas)

Decisions from the 2026-09-19 interview are in ADR-020. Shape: the **app owns the garage**, the
**server stays stateless** — every plan request carries the resolved vehicle, so a hand-edited bike
behaves exactly like a catalog one.

- [x] **Vehicle on the wire** — done 2026-09-20 (ADR-025): `vehicle` in `POST /v1/plan` (electric: usable kWh, city/highway Wh/km,
      AC kW, DC kW, connectors; combustion: tank, L/100 km, reserve). No vehicle = today's SR/S, so
      shipped builds keep working. Replaces the package constants in `energy.go` and the hardcoded
      `J1772,TESLA` in `nrel.go`.
- [x] **Catalog** at `GET /v1/vehicles` — done 2026-09-19: 16 entries (Zero MY2025 street/dual-sport,
      LiveWire ONE + S2s, Can-Am Pulse/Origin, two petrol placeholders), each carrying its source and a
      confidence flag, with a test enforcing both and NREL-valid connectors. Planning still ignores it
      until the vehicle rides in the request. Gaps: LiveWire and Can-Am don't publish usable-vs-gross
      capacity, and Zero no longer says what speeds its highway figures assume. As originally specified: (static, versioned, cacheable; app caches it and can plan
      offline-ish): Zero lineup, LiveWire (One, S2 Del Mar, S2 Mulholland), Can-Am (Origin, Pulse),
      plus generic gas bikes. Spec-sheet numbers with sources in the file, each field user-editable in
      the app — an edited bike is just a custom vehicle.
- [ ] **Connectors**: NREL takes `J1772`, `J1772COMBO` (CCS1), `CHADEMO`, `TESLA`, `NEMA*` — **there is
      no `NACS` filter value** (verified 2026-09-19). Tesla hardware arrives as `TESLA` and is split by
      `ev_charging_level`, so "NACS" in the catalog means TESLA + DC. DC stations carry
      `ev_dc_fast_num`, `ev_network` and connector lists, which is what stop selection needs.
- [x] **Adapters stay on the phone** — done 2026-09-20: the garage entry records what the bike takes natively and what
      the rider has adapters for; the app sends the union. Setup asks once (bikes, which chargers to
      show, adapters, consumption tweak).
- [ ] **Flat charging rate, stated plainly in the UI** ("assumes a steady 6.6 kW"). Real DC tapers, so
      a flat rate flatters fast charging — say so where the estimate appears. Taper curves only when a
      rider complains with a real log.
- [ ] **Gas bikes** (both current testers ride one): fuel stops from **`amenity=fuel` in our own OSM
      extract** — no third party, no key, principle 1 intact; needs an osmium/Go pass into a small
      static index on axiom. Most rides need nothing: with 150–250 mi of tank and 5-minute fills this
      is "warn when the ride outruns your tank, and show fuel near the route", not a charge plan.
- [~] **Charger reliability** — Open Charge Map lookup shipped 2026-09-19 (ADR-022): status, last-verified
      date and rider fault check-ins on stops and backups, with a dead stop dropped and replanned.
      Coverage is the limit: nothing listed near the Ramona stop, full data in the Santa Cruz mountains.
      Arrival-reserve rule and the backup in the ride view shipped the same day (ADR-023).
      Still to do: retry overhead in DC stop estimates (only matters once a DC-capable bike is in the
      garage).
      (ADR-021 rules out the obvious approach: reliability varies per stall,
      not per network, so no blocklists). Cheap wins needing no new data: require enough charge on
      arrival to reach the backup site, surface the backup in the app, prefer multi-stall sites, and
      add retry overhead to DC stop estimates (LiveWire's own advice is up to three attempts; owners
      report 10–15 min just to start). Open Charge Map as a second source, and possibly anonymous
      "worked / didn't" reports, are the only routes to real data — both still need Paul's decision.
- [ ] App Store listing stays **"Serpentine EV"** for now; revisit if gas bikes become a headline
      feature (name, screenshots and the public pages move together — see CLAUDE.md).

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
