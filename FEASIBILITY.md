# Serpentine — feasibility and market research

*Curvy-road motorcycle ride planner for iPhone, charger-aware for a Zero SR/S, out-and-back and loop routes, no subscription, Apple Maps voice navigation.*

Research date: 2026-09-16. Prices are US App Store / vendor pages as of that date. Items marked **[unverified]** could not be confirmed from a primary source.

---

## 1. Verdict

Feasible. The routing problem is largely solved by open-source GraphHopper; charger data is free via NREL; the specific combination requested (curvy routing + EV-aware planning + one-time purchase + Apple Maps voice) is a gap nobody fills. Two caveats dominate:

1. **"Use the iPhone's voice navigation" is only partly achievable.** Apple exposes no navigation engine, UI, or Siri voice to third parties. The workable path is handing an ordered waypoint list to the Apple Maps app via the iOS 18.4+ unified URL; Apple re-routes between waypoints with its own engine.
2. **The charging side is harder than the routing side.** The SR/S is J1772 AC only (6.6 kW, 12.6 kW with the Rapid Charger module) until Zero's 2027 DC fast-charge option ships. Charge stops are 1–2 hour events and the energy model has to be trustworthy.

Two load-bearing claims — Apple's repeated `waypoint=` parameter and GraphHopper's `curvature` encoded value — were verified directly against primary documentation.

---

## 2. Market: existing apps

| App | Twisty routing | Loops | Traffic | EV chargers | Pricing (US) | Voice nav |
|---|---|---|---|---|---|---|
| Calimoto | 4 levels (Twisty = paid) | Yes, radial dial for distance/direction | No live traffic; closures reported stale | No | $49.99/yr sub; $89.99 one-time "Navigation Package" | In-app + CarPlay |
| Kurviger | 4 profiles (fastest / fast & curvy / curvy / extra curvy), mixable per segment; avoidance strength 1–5 | Yes: 300 km free / 600 km paid | Closures only | Charger *search* at plan time only (Dec 2024); no plug type, not in navigation | Tourer $12.99/yr, Tourer+ $24.99/yr | In-app + CarPlay (Apr 2025) |
| Scenic | Curviness slider + "AI beauty routes" | Yes, by distance + direction | Closure/incident display | No | $59.90/yr sub | In-app + CarPlay |
| Rever | On/off "Twisty" (Pro) | No native loop | Weather alerts only | No (fuel POIs only) | $39.99/yr sub | In-app; CarPlay "hasn't been optimized in a while" |
| Detecht | Yes (Premium) | Yes, one-click | No | No | $60/yr sub | In-app + CarPlay beta |
| MyRoute-app | "Winding" option | Not found | Not found | Not found | €79 / €99 lifetime | In-app + CarPlay |
| Furkot (web) | "Prefer curvy roads" | No | No | Yes: range-based stops via NREL, car-oriented | Free + paid pass | None; hands off to Waze/OsmAnd/Scenic |
| Harley-Davidson app | Avoid-highways only; "gravitates toward major roads" | No | No | No | Free | In-app |
| Apple Maps | None | No | Yes | Cars | Free | Yes |
| Google Maps | Two-wheeler mode exists in 39 countries — **not US, not Europe** | No | Yes | Cars | Free | Yes |
| Waze | Motorcycle Mode (Jul 2026) — LatAm/SEA only | No | Yes | No | Free | Yes |
| TomTom GO Ride | Had fast/thrilling/super-thrilling + round trip | — | — | — | **Discontinued Sept 2024** | — |

### Newer entrants

- **Cardo Ride** (ex-RISER): "Super Curvy Routing" with levels, CarPlay, offline. $59.99/yr sub. 4.6★ (3K+). Complaints: cluttered UI, battery.
- **Stegra.io** (Jul 2024): curvy/gravel/scenic modes, round trips by time or distance, CarPlay, offline-first. $49.99/yr sub. Complaint: curvy mode still uses highways.
- **Kurvo**: curvature-scored roads, rally pace-notes voice. $49.99/yr sub. No CarPlay/offline.
- **Motobit** (€29.99/yr), **Vroom GPS** (free, non-profit), **MotoVault**, **Twisties.ai** (free, undocumented), **TwistyRoad** ("coming soon").

### EV / Zero-specific

- **Zero NextGen app**: 2.7★ (84 ratings) on US App Store. Has a charger locator. **Turn-by-turn navigation was removed in v2.10.0**; no route planning. Zero's official guidance is "use PlugShare."
- **ABRP** ($49.99/yr) and **Chargeway** ($24.99/yr): no motorcycle vehicle models **[absence inferred from listings]**.
- **PlugShare**: no motorcycle-specific filter or planner found **[unverified]**.
- **Furkot** is the only planner combining a curvy preference with range-based charging stops, but it's web-only, car-oriented, and has no navigation.

### Gaps no app fills

1. Charger-aware twisty routing for electric motorcycles.
2. Live traffic on curvy routes (all dedicated apps lack it; Apple/Google/Waze have traffic but no twisty routing in the US).
3. A "moderate pace" / speed-profile preference. Calimoto ETAs assume speed-limit riding.
4. Out-and-back with a different return road; time-boxed loops with a lunch/charge stop.
5. Route variety (Calimoto "takes you on the same roads every time"; Scenic extra-curvy produces dead ends; Rever routes sport bikes onto unpaved roads).
6. One-time purchase. Only MyRoute-app lifetime and Calimoto's $89.99 package avoid subscriptions; subscription fatigue is a recurring review complaint (Rever, Calimoto).
7. US search/address quality — Calimoto is European-formatted; Kurviger is Europe-weighted (226 US ratings vs ~8K German).
8. Pushing a curvy route into Apple Maps for Siri/CarPlay nav — no app does it.
9. Battery drain / stability complaints across Rever, Detecht, Cardo Ride, Calimoto.

### Market size signals

- US fleet: 8.79M registered on-road motorcycles (IIHS 2023); MIC says 11.6M in use, ~$48B industry.
- Calimoto claims 1M+ users, 230M miles ridden in 2025; 1M+ Play downloads; US Maps & Nav rank #306 vs Germany #46.
- Rever: 1M users (2020); 16K US App Store ratings, the largest US iOS base among dedicated apps.
- **EV segment is tiny**: LiveWire sold 653 bikes in 2025, ~70% of the 50+ hp on-road EV segment, so the whole US premium e-moto market is <1,000 units/yr. Zero: +89% NA retail YoY, no unit counts disclosed. The EV feature is a differentiator and a personal itch, not a market.

---

## 3. Routing engine

### GraphHopper (Java, Apache-2) — recommended

- **Custom models (JSON) expose a `curvature` encoded value**: `"beeline distance" / edge_distance (0..1)`, curvy roads < 1. Also `max_speed`, `urban_density` (RURAL / RESIDENTIAL / CITY), `road_class`, `average_slope`, `max_slope`, `surface`, `lanes`, `change_angle`.
- Ships a `curvature.json` example (`curvature >= 0.98 → multiply_by 0.4`), which the config comments describe as useful "for a motorcycle profile."
- `max_speed_calculator.enabled: true` fills `max_speed` from legal defaults (westnordost/osm-legal-default-speeds) where OSM lacks `maxspeed` — only ~12% of OSM roads globally have one tagged.
- `algorithm=round_trip` with `round_trip.distance`, `round_trip.seed`, `heading`; `alternative_route` also available. Custom models require flexible mode (`ch.disable=true`) via POST `/route`.
- Elevation: SRTM/CGIAR auto-download; `average_slope` usable in cost models.
- Kurviger is built on GraphHopper — proof this stack works for exactly this use case.
- **Sizing**: Geofabrik us-west PBF is 3.2 GB; North America (12 GB) imports in ~1 h. A 16 GB VPS should import and serve US-West with one profile **[extrapolated, unverified]**.
- **On-device iOS is dead** (graphhopper-ios / j2objc abandoned ~2020).

Sketch of a cost model:

```json
{
  "priority": [
    { "if": "curvature >= 0.98", "multiply_by": "0.4" },
    { "if": "max_speed > 105", "multiply_by": "0.5" },
    { "if": "max_speed < 40", "multiply_by": "0.7" },
    { "if": "urban_density == CITY", "multiply_by": "0.3" },
    { "if": "road_class == MOTORWAY || road_class == TRUNK", "multiply_by": "0.2" },
    { "if": "road_class == RESIDENTIAL", "multiply_by": "0.5" },
    { "if": "surface == GRAVEL || surface == DIRT", "multiply_by": "0.1" }
  ],
  "distance_influence": 70
}
```

Out-and-back with a different return road: route to the turnaround, then compute the return with penalties on the outbound edges (Kurviger's `avoid_edges`; GraphHopper OSS doesn't expose it directly — small piece of work).

### Alternatives

- **Valhalla** (C++, MIT): motorcycle costing has `use_highways` and `use_trails` only; **no curvature attribute**, no round trips. Only engine buildable for on-device iOS (community `Rallista/valhalla-mobile` Swift package, v0.5.1 Apr 2026), but adding curviness means a C++ fork of mjolnir + sif plus multi-GB tile downloads. Not recommended.
- **OSRM**: Lua profiles can't see way geometry, so per-edge sinuosity is impossible without pre-tagging. Not recommended.
- **pgRouting**: trivial to weight by sinuosity in SQL, but no instructions, no round trips, graph rebuilt per query. Good for offline analysis only.

### Hosted APIs

| API | Curvy | Round trip | Free tier | Paid |
|---|---|---|---|---|
| **Kurviger API** | Yes (only hosted API with true curvy routing) | Yes (≤300 km) | None | Add-on to a GraphHopper paid plan; each request = 10× GH credits; package price unpublished; "Powered by Kurviger" attribution required |
| GraphHopper Directions | Via custom models (flexible mode) **[hosted `curvature` support unverified]** | Yes (2 credits) | 500 credits/day, non-commercial | Basic €69/mo (5K/day), Standard €199, Premium €479 |
| Stadia Maps (Valhalla) | No | No | 200K credits/mo non-commercial | Starter $20 (≈50K routes), Standard $80, Pro $250 |
| Mapbox Directions | No | No | 100K req/mo | $2.00/1K |
| Google Routes | No (TWO_WHEELER mode, not US) | No | 10K/mo Essentials | $5–15/1K |
| HERE Routing v8 | No | No | ~5K/mo | ~$0.75/1K |
| TomTom Routing | No | No | 20K/mo | Unpublished |

---

## 4. Data

### Curviness
Computed from OSM geometry at import (GraphHopper) or in PostGIS. Adam Franco's **Curvature** project (GPL-3) publishes free ODbL KML/GeoJSON at kml.roadcurvature.com by region, useful for validation and seeding "known good roads."

### Speed limits
OSM `maxspeed` is sparse; fill with legal defaults via GraphHopper's `max_speed_calculator`. State-specific rules are approximate.

### Traffic — the weakest leg
- No free road-level traffic data. Mapbox Traffic Data and TomTom Traffic Stats are enterprise/contact-sales. Strava Metro is bike/ped only.
- **FHWA HPMS** publishes AADT, posted speed, functional class per state as ArcGIS feature services, but only for Federal-Aid roads, and conflating onto OSM edges is a real map-matching project. **Caltrans AADT** 2023 is available as points on the state highway network.
- Realistic v1: proxies. `urban_density` + `road_class` (penalize city/residential, prefer rural secondary/tertiary) get most of "minimal traffic" for the roads riders care about. Flag as the feature most likely to underdeliver.

### Elevation
AWS Terrain Tiles (free, no account), Open Topo Data (NED 10 m CONUS, self-hostable), or GraphHopper's built-in SRTM import.

---

## 5. EV chargers

### Data
- **NREL AFDC API**: free, api.data.gov key, 1,000 req/hr. Has a **`nearby-route` endpoint that takes a polyline** and returns stations along it. Filters: `ev_connector_type` (J1772, J1772COMBO, TESLA, CHADEMO, NACS/J3271), `ev_charging_level`, `ev_network`, `status`. Per-port kW is not a reliable field **[unverified]**.
- **Open Charge Map**: free API, CC BY 4.0, attribution required.
- **PlugShare**: commercial API by request. **Chargeway**: no public API found.

### The bike (verified against dealer spec sheet and Zero accessory page)
- 2026 SR/S: 17.3 kWh max / 15.1 kWh nominal; 171 mi city / 116 mi highway; MSRP $20,995.
- **Standard onboard charger is 6.6 kW AC via J1772.** Optional dealer-installed 6 kW Rapid Charger module → 12.6 kW AC, same J1772 inlet, fits SR/S 2022–2026, can't coexist with Power Tank. L2 20–80%: 1.4 h at 6.6 kW, 43 min with Rapid Charger.
- **"Rapid Charge" is not CCS/DC.** No current Zero has CCS. Zero announced (2 Sept 2026) Level 3 DC fast charging (CCS, 20–80% <30 min) for 2027 models with a retrofit accessory "planned" for 2022+ Cypher III bikes. No kW, price, or network partner announced.
- **NACS**: Zero sells the Tesla Tap Mini adapter ($260) for Tesla Destination chargers / Wall Connectors (≤60 A), explicitly **not** Superchargers. ~40K additional L2 points in North America.
- Practical inference: 12.6 kW needs a ≥53 A J1772 pedestal; most public L2 is 32–40 A (6.6–9.6 kW). **Plan on 6.6 kW and 1–2 hour dwell times.** A charge stop is a lunch stop.

### Planner design
Filter NREL to J1772 (any level) plus TESLA + Level 2 (via adapter); exclude DC Fast until 2027. Run `nearby-route` on the planned polyline, cache server-side, apply a conservative SoC model (consumption on twisty roads sits between the city and highway figures), insert stops as via-points, re-route. Range/energy model errors will hurt trust more than routing quirks.

---

## 6. Voice navigation on iPhone

### What's not possible
- No third-party access to Apple's navigation engine, guidance UI, or Siri voice. `MKDirections.Request` has no waypoints; `MKRoute.Step` has plain-text instructions only, no maneuver enum, no timing, and only for Apple-computed routes. Nothing in WWDC24/25 changed this; nothing found from WWDC26 **[not exhaustively verified]**.
- Siri voices are blocked from `AVSpeechSynthesizer` (impersonation rationale). No GPX import into Apple Maps.
- CarPlay *screen* for your own app needs the `com.apple.developer.carplay-maps` entitlement, which is opaque and slow to get. Voice-only needs no entitlement.

### Path A — Apple Maps handoff (verified against Apple docs)
Since iOS 18.4 / watchOS 11.4, the unified URL supports multistop directions:

```
https://maps.apple.com/directions
  ?source=32.7157,-117.1611
  &waypoint=33.0119,-116.9530
  &waypoint=33.1234,-116.6789
  &destination=32.7157,-117.1611
  &mode=driving
  &avoid=tolls,highways
  &start=3
```

- `waypoint` can be repeated; `avoid` accepts `tolls,highways,busy-roads`; `start=N` auto-starts navigation after N seconds.
- You get Siri voice, Apple Watch wrist taps, CarPlay, and helmet-headset audio for free.
- **Caveats**: Apple recomputes the route between each waypoint pair with its own engine, so waypoints must sit at decision points where Apple would otherwise choose the boring road. Every waypoint is a *stop* and is announced as such. Undocumented cap of ~13 stops observed in the Maps UI **[not confirmed for the URL path — test it]**. No route-shaping or polyline parameter.
- Google Maps universal link: `waypoints=` up to 9, `dir_action=navigate`, same limitations; `comgooglemaps://` scheme has no waypoints.
- Effort: ~1 day. Adequate for a 200-mile loop with 10–12 shaping points; inadequate for routes threading many small roads.

### Path B — in-app turn-by-turn on your own route
| SDK | Custom route from your engine | Waypoints | Voice | Cost |
|---|---|---|---|---|
| **Ferrostar** (stadiamaps) | Yes — `CustomRouteProvider` / `RouteAdapter`, parses OSRM-flavored JSON | Per route | `SpokenInstructionObserver` on `AVSpeechSynthesizer` with audio ducking | BSD, free. v0.53 (Jun 2026), SwiftUI + MapLibre, still 0.x |
| Mapbox Nav SDK v3 | Partial — custom routing provider removed in v3; route-follow via Map Matching (≤100 coords) | 25 / 100 | Yes | Free 100 MAU + 1K trips; then $0.30/MAU + $0.08/trip |
| TomTom Nav SDK iOS | Yes — `supportingPoints` polyline reconstruction | 150 | Yes | Public preview; sales-led pricing **[unverified]** |
| HERE SDK Navigate | Yes | Many | Yes | Contract only |
| Google Nav SDK | Route tokens only, no via waypoints | 25 | Yes | Enterprise only |
| MapLibre Navigation iOS | Any OSRM-compatible server | Per server | Inherited **[undocumented]** | MIT; tiny community |

- Ferrostar is the right choice. Gap: GraphHopper's response isn't OSRM-flavored, so write a small adapter (GraphHopper instructions → OSRM steps) or re-route the shaped polyline through a Valhalla endpoint (Stadia Starter, $20/mo) for guidance. Test this seam early.
- Voice quality: stock system voice is mediocre. Users can download Enhanced/Premium voices in Settings > Accessibility (no API to trigger it). Personal Voice (iOS 17+) works via `usesPersonalVoice`. Upgrade path is neural TTS — cloud, or on-device Piper with pre-rendered phrases for offline.
- Headsets: Cardo/Sena receive any app's audio via standard phone pairing (enable the headset's "Navigation App" setting). CarPlay helmet displays (CHIGEE etc.) route audio Display ↔ iPhone ↔ headset, so any app works for audio.
- Apple Watch haptics: Maps-only. A watch companion can fire its own `WKInterfaceDevice.play(.directionUp/.directionDown)` — extra build.

### Recommendation
Ship Path A first (a day of work, gives the Siri experience). Add Path B (Ferrostar) as the "faithful route" mode once the handoff loses your roads too often.

---

## 7. Architecture and cost

**Indie stack**
1. Self-hosted GraphHopper on a 16 GB VPS, US-West extract, one motorcycle profile with the JSON custom model above, `max_speed_calculator` on, LM hybrid mode with a baked profile (only tighten per request), `round_trip` + `alternative_route` + `heading`.
2. NREL `nearby-route` on the planned polyline, cached; simple SoC model; stops inserted as via-points.
3. iOS: MapLibre + Ferrostar; Apple Maps handoff button; Stadia Maps tiles.
4. Plan B / demand validation: Kurviger API on GraphHopper Basic (€69/mo + unpublished add-on) — zero ops, but their definition of "curvy."

**Monthly cost** (assumes ~12 routing requests per active user per month)
- 1K users: VPS ~$25–40, Stadia Starter $20, NREL/terrain $0, Apple dev $99/yr → **≈ $50–70/mo**
- 10K users: 32 GB VPS or two nodes $60–120, Stadia Standard $80, CDN ~$10 → **≈ $150–250/mo**
- Avoid: Mapbox Nav SDK at 10K MAU ≈ $3K/mo; hosted GraphHopper Premium + Kurviger ≈ $520+/mo.

Compatible with a one-time purchase price: route planning is bursty and cacheable, and there's no live-traffic serving cost.

---

## 8. Risks, ranked

1. **Round-trip route quality** — lollipops, backtracking, absurd detours from over-penalized straights. Iterative tuning; this is where Kurviger has years of polish.
2. **Flexible-mode latency** for 150–300 km loops (seconds per request; `routing.max_visited_nodes` caps). Mitigate with LM hybrid.
3. **No real traffic data.** Proxies only.
4. **Energy / charge-time model** — unreliable per-port amperage, Tesla destination chargers only via adapter, CCS not until 2027.
5. **GraphHopper → Ferrostar format seam.**
6. **Rural speed-limit gaps** in OSM; legal-default inference is approximate.
7. **Licensing**: OSM and Curvature outputs are ODbL (share-alike on derived databases); OCM is CC BY; Kurviger requires on-screen attribution.

## 9. First experiment

Before anything else, spend 20 minutes hand-building an Apple Maps `/directions` URL with 12–15 waypoints on a known San Diego backcountry loop and see (a) how many waypoints it accepts and (b) how often Apple's routing between them picks the wrong road. That single test decides how much of the "use iPhone voice nav" requirement Path A satisfies.

---

## Sources

**Apple / iOS**
- https://developer.apple.com/documentation/mapkit/unified-map-urls
- https://developer.apple.com/documentation/mapkit/mkdirections/request
- https://developer.apple.com/documentation/mapkit/mkroute/step
- https://developer.apple.com/videos/play/wwdc2025/204/
- https://developer.apple.com/forums/thread/784030
- https://developer.apple.com/forums/thread/682438 (Siri voice)
- https://developer.apple.com/documentation/bundleresources/entitlements/com.apple.developer.carplay-maps
- https://routerra.io/blog/apple-maps-more-then-15-stops
- https://developers.google.com/maps/documentation/urls/get-started

**Routing engines**
- https://github.com/graphhopper/graphhopper/blob/master/docs/core/custom-models.md
- https://github.com/graphhopper/graphhopper/blob/master/core/src/main/resources/com/graphhopper/custom_models/curvature.json
- https://github.com/graphhopper/graphhopper/blob/master/docs/web/api-doc.md
- https://github.com/graphhopper/graphhopper/issues/2392
- https://www.graphhopper.com/blog/2022/06/27/host-your-own-worldwide-route-calculator-with-graphhopper/
- https://www.graphhopper.com/pricing/
- https://github.com/boldtrn/kurviger-api-documentation
- https://kurviger.com/en/api_use
- https://valhalla.github.io/valhalla/api/route/api-reference/
- https://github.com/Rallista/valhalla-mobile
- https://github.com/Project-OSRM/osrm-backend/blob/master/docs/profiles.md

**Nav SDKs**
- https://github.com/stadiamaps/ferrostar
- https://stadiamaps.github.io/ferrostar/route-providers.html
- https://stadiamaps.github.io/ferrostar/ios-getting-started.html
- https://stadiamaps.com/pricing/
- https://www.mapbox.com/pricing
- https://docs.mapbox.com/ios/navigation/v3/guides/migration/migrate-core/
- https://docs.tomtom.com/navigation/ios/guides/routing/waypoints-and-custom-routes
- https://docs.here.com/here-sdk/docs/ios-introduction-editions
- https://community.sena.com/hc/en-us/community/posts/216698826-Waze-audio-

**Data**
- https://developer.nrel.gov/docs/transportation/alt-fuel-stations-v1/
- https://openchargemap.org/develop
- https://github.com/adamfranco/curvature
- https://kml.roadcurvature.com/
- https://www.fhwa.dot.gov/policyinformation/hpms/shapefiles.cfm
- https://gisdata-caltrans.opendata.arcgis.com/datasets/d8833219913c44358f2a9a71bda57f76_0/about
- https://registry.opendata.aws/terrain-tiles/
- https://www.opentopodata.org/

**Zero SR/S**
- https://www.newcenturymoto.com/showroom/specs/2026/zero/srs
- https://zeromotorcycles.com/accessories/products/6-kw-rapid-charger
- https://electrek.co/2026/09/02/zero-will-soon-add-the-main-feature-its-electric-motorcycles-have-always-lacked/
- https://www.rideapart.com/news/716652/zero-motorcycles-ev-tesla-nacs-charging-adapter/
- https://apps.apple.com/us/app/zero-motorcycles-nextgen/id1354186695
- https://zeromotorcycles.com/ride-electric/charging-and-range

**Competitors and market**
- https://apps.apple.com/us/app/calimoto-motorcycle-routes/id1209129603
- https://calimoto.com/en/pricing
- https://apps.apple.com/us/app/kurviger-motorcycle-navigation/id6473445827
- https://kurviger.com/en/features
- https://forum.kurviger.com/t/integration-of-charging-points/3289
- https://scenic.app/premium/
- https://www.rever.co/pro
- https://www.rever.co/faqs
- https://www.detechtapp.com/premium
- https://www.myrouteapp.com/en/shop
- https://help.furkot.com/features/ev-charging-stations.html
- https://www.tomtomforums.com/threads/go-ride-app-discontinuation.34663/
- https://developers.google.com/maps/documentation/routes/coverage-two-wheeled
- https://blog.google/waze/waze-updates-gemini-motorcycle-mode/
- https://apps.apple.com/us/app/riser-motorcycle-app/id1087005682
- https://apps.apple.com/app/id6504736302 (Stegra)
- https://electrek.co/2026/02/11/the-one-company-that-sells-nearly-three-quarters-of-all-electric-motorcycles/
- https://www.motorcyclepowersportsnews.com/zero-motorcycles-closes-2025-with-strong-global-growth-across-sales-dealers-and-product-lines/
- https://www.consumeraffairs.com/insurance/motorcycle-industry-statistics-by-state.html
