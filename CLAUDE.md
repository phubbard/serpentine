# CLAUDE.md — serpentine

Curvy-road motorcycle ride planner for iPhone. Favours plentiful turns, moderate posted speeds and low
traffic over minimum time; plans charge stops for a Zero SR/S; generates loops and out-and-back rides;
hands off to Apple Maps for voice navigation. **No subscription, no telemetry, no third-party runtime
dependencies in the app.** Owner: Paul Hubbard (pfh@phfactor.net). Solo project, self-hosted.

Read this file first. Then `docs/ROADMAP.md` for what to build next and `docs/DECISIONS.md` for why
things are the way they are. `FEASIBILITY.md` is the September 2026 research that started this and is
the reference for competitor and API facts; don't re-research what it already answers.

## Status (2026-09-18)

- Feasibility done. GraphHopper serving on axiom:8989 (first good import 2026-09-18); loops 35–200 ms.
- serpentine-api (Go, `server/`) running on axiom:8990 via launchd, public at `/v1`: `point_to_point`,
  `loop` and `out_and_back` plans, loop scoring, charge stops (NREL), Apple Maps handoff, GPX. Browser test page with a
  map at `https://serpentine.phfactor.net/v1/`, product page `/v1/about` (also `/` via Caddy), support
  `/v1/support`, privacy `/v1/privacy` (`server/web/`, embedded; tiles via a caching proxy in
  `~/serpentine-api/tiles` on axiom — self-hosted vector tiles are the planned replacement).
- iOS app v0.1.0 in `ios/` (SwiftUI + MapKit, xcodegen; iPhone + iPad split view, Mac Catalyst planned): plan loop / out-and-back from location or
  search, charging, map + stats, Apple Maps handoff, GPX share. `make -C ios build|test|run`.
  On TestFlight since 2026-09-18 (0.1.0 build 29) as **"Serpentine EV"** (ASC app id 6813768490;
  home-screen name stays "Serpentine"), internal group "Internal" with automatic distribution. `make -C server test|run|deploy|logs`. No app code yet.
- GitHub: `git@github.com:phubbard/serpentine.git`, branch `main`.
- Rate limits (ADR-030): 24 plans in flight, 20/min per caller (burst 8), 429 + Retry-After. Measured
  ceiling ~16 plans/s at 32 concurrent. `make -C server deploy` retries the launchd bootstrap: doing
  it immediately after a bootout fails with "Input/output error" and leaves the service down.
- Ops dashboard: `http://axiom:8990/stats` (LAN only — Caddy proxies `/v1/*` and `/`, so `/stats`
  404s from outside). Counters only, hourly, kept in `~/serpentine-api/stats.db` via the `sqlite3`
  CLI and restored at startup (ADR-026, ADR-027). `SIGKILL` loses up to 5 minutes of counts.
- Shared agent memory: Memento page `/projects/serpentine.md` at `http://webserver:8321/mcp` (see
  Memento section). Keep it in sync with major status changes.

## Repository layout

```
CLAUDE.md            this file
README.md            one-paragraph public description
FEASIBILITY.md       market + technical research, costs, risks, sources (Sept 2026)
docs/
  ROADMAP.md         phases with acceptance criteria — the work queue
  DECISIONS.md       ADR-style log; append, never rewrite history
  API.md             the serpentine server API contract (phase 1); the iOS app codes against this
infra/
  README.md          axiom + Pi 5 runbook, smoke tests, gotchas
  docker-compose.yml GraphHopper 11 on axiom
  graphhopper/config.yml, custom_models/serpentine.json
  caddy/serpentine.caddyfile   Pi 5 site block
demo/
  utc-ramona-sunrise.json  hardwired demo ride (UTC → Ramona → Julian → Sunrise Hwy S1 → Alpine → UTC)
tools/
  demo_route.py      routes a demo through GraphHopper; prints roads, GPX/GeoJSON, Apple/Google Maps URLs
server/              Go API in front of GraphHopper (+ NREL later): main.go, plan.go (loops),
                     score.go, handoff.go (Apple Maps), gh.go; Makefile deploys to axiom via launchd
ios/                 SwiftUI app: project.yml (xcodegen), Makefile, Serpentine/ (API/, Model/, Views/),
                     SerpentineTests/ (decodes real API fixtures), tools/ (TestFlight notes, icon)
```

## Architecture

```
iPhone app (SwiftUI, MapKit)
   │  HTTPS, one host only: serpentine.phfactor.net
   ▼
Caddy on webserver (Pi 5, .3)          ← the house's only internet-facing host, 80/443 only
   │  reverse_proxy
   ▼
serpentine-api on axiom (Mac Studio M4 Max, 128 GB, .7)   ← phase 1, Go, :8990
   ├─► GraphHopper 11, :8989, Docker, whole-US graph (ADR-029), profile "motorcycle" (LM/hybrid)
   ├─► NREL AFDC: the whole US fetched once a day into ~/serpentine-api/stations.json (ADR-028);
   │     corridor search is local. Key only on axiom, X-Api-Key. Per-plan calls are the fallback.
   └─► (later) elevation, HPMS AADT, cached tiles
```

Principles that constrain every design choice here:

1. **The phone talks to serpentine.phfactor.net and nothing else.** No Mapbox, no analytics, no font CDN,
   no NREL key on device. Map display is MapKit (Apple, free, already on the phone). This is Paul's
   house privacy rule (`/skills/privacy-and-third-party-policy.md` in Memento) and it is not negotiable.
2. **Routing cost is data-driven and tuned per request.** `infra/graphhopper/custom_models/serpentine.json`
   is the baked-in *base* profile; **editing it forces a full re-import (~25 min)** because GraphHopper
   hashes every profile into the graph (ADR-009). Tuning lives in the per-request `custom_model`
   (serpentine-api sends it), which in LM mode may only tighten (multipliers ≤ 1). Fold tuned rules
   into the base file only when re-importing anyway (new OSM extract, new encoded value).
3. **The energy model is conservative.** SR/S: 17.3 kWh max / 15.1 nominal, 116 mi highway / 171 mi
   city, **J1772 AC only**, 6.6 kW stock (12.6 kW with Rapid Charger — assume 6.6 at public L2).
   No CCS until Zero's 2027 option. A charge stop is 1–2 h. Being wrong here loses trust faster than
   a bad route.
4. **Voice nav is Apple Maps handoff first.** Unified URL `https://maps.apple.com/directions?…` with
   repeated `waypoint=`, `mode=driving`, `avoid=tolls,highways`, `start=N`. Apple re-routes between
   waypoints and announces each as a stop; ~13-stop cap observed in UI, unconfirmed for URL. In-app
   turn-by-turn (Ferrostar, BSD) is phase 4 and only if handoff proves inadequate — it would add
   MapLibre, which violates principle 1 unless tiles are self-hosted.
5. **One-time purchase.** Server cost must stay flat: cache routes, no live traffic, no per-request
   third-party billing.

## Working on this repo

### Conventions (Paul's, verified across his other repos)

- Direct, technically fluent communication. Concrete recommendations, caveats flagged. No hedging.
- **Stdlib over dependencies.** Go: `net/http`, `encoding/json`, no frameworks. Swift: Foundation,
  SwiftUI, MapKit, CoreLocation — no SPM packages until a decision in `docs/DECISIONS.md` says so.
- Python is acceptable for one-off tooling (analysis scripts, profile tuning), not for the server.
- Commits: imperative subject, body explains *why*. Build number for iOS = `git rev-list --count HEAD`.
- No secrets in the repo, ever. `*.env`, `*.local.xcconfig`, `.p8`, tokens → gitignored. The root
  `.gitignore` covers `*.env`, `data/`, `*.osm.pbf`, `graph-cache/`; `ios/.gitignore` covers
  `Serpentine.local.xcconfig`, `Build.gen.xcconfig`, the generated `.xcodeproj` and `build/`.
- No cloud CI. Everything builds and ships from the dev Mac via Makefile. Copy
  `apple-deployment-playbook.md` from `~/code/mapbook/` into `ios/` when that phase starts and follow it.
- **Ship a feature, update the shop window.** `server/web/about.html` is the public product page
  (`/` and `/v1/about`); `server/web/support.html` answers questions about what the app does. A
  user-visible feature isn't done until both say the truth about it — and the page's screenshots come
  from the build that shipped the feature, not an older one. Same for `README.md` and `docs/API.md`.
- Write scar tissue down: every gotcha that cost more than 10 minutes goes in `infra/README.md`
  (ops) or `docs/DECISIONS.md` (design). Future agents read those before touching anything.

### Apple specifics (from `/projects/apple-developer-account.md`, Memento)

- Simulator names repeat across runtimes (two "iPhone 16"s); the Makefile pins `SIM_OS` too.
  `make -C ios test SIM_DEVICE="iPhone 16" SIM_OS=18.4` checks the deployment floor.
- Driving the simulator: short taps don't flip a `Toggle`; use a ~0.2 s tap.
- TestFlight signing uses **Xcode's signed-in Apple ID** (Account Holder mints the distribution
  cert). That session expires silently: export fails with "Unable to log in with account" then
  "No profiles for 'net.phfactor.serpentine' were found" — sign in again under Xcode → Settings →
  Accounts. `SIGN_WITH_KEY=1` signs with the ASC API key instead, but the App Manager key gets
  "Cloud signing permission error" (cloud-managed distribution certs need an Admin-role key).
- Mac screenshots: size the Catalyst window to 1440×900 points (`osascript -e 'tell application
  "System Events" to tell process "Serpentine" to set size of window 1 to {1440, 900}'`) and capture it
  with `screencapture -o -x -l <CGWindowID>` — on a Retina display that lands exactly on 2880×1800, the
  largest size ASC takes. Window captures carry an alpha channel, so convert as for iOS.
- App Store screenshots without tapping: Debug builds accept `-screenshotPlan` (add `-outAndBack`)
  as launch arguments and plan the Ramona charging ride on launch: `xcrun simctl launch <udid>
  net.phfactor.serpentine -screenshotPlan`. Clean status bar: `xcrun simctl status_bar <udid> override
  --time 9:41 --batteryState charged --batteryLevel 100`. Charging plans take ~30 s; wait for map tiles.
  ASC rejects alpha: `sips -s format jpeg` into `upload/`.
- Commit before `make upload-testflight`: a dirty tree makes the scheme stamp build N+1 while the
  Makefile (and `testflight-notes`) expect N.

- Team ID `NSR65JVW9F` (paid individual). Bundle ID: **`net.phfactor.serpentine`** — chosen once,
  never changed ("pick it like a tattoo"). Display name can change; bundle ID cannot.
- Automatic signing, `-allowProvisioningUpdates`, xcconfig with gitignored `.local.xcconfig`
  holding `DEVELOPMENT_TEAM`, `APP_STORE_KEY_ID`, `APP_STORE_ISSUER_ID`. xcconfig comments are
  `//`, never `#`.
- `GENERATE_INFOPLIST_FILE: NO`, maintained Info.plist with `ITSAppUsesNonExemptEncryption=false`,
  `LSApplicationCategoryType=public.app-category.navigation`, `PrivacyInfo.xcprivacy` for
  required-reason APIs. Location usage strings are mandatory (CoreLocation for "start from here").
- TestFlight via `make upload-testflight` + `tools/set-testflight-notes.rb` (copy verbatim from
  mapbook/BMLogger). App Manager-role ASC key or beta-group distribution 403s.
- Minimum iOS **18.4** — required for the unified Maps URL with waypoints. Don't support older.

### Infra specifics

- Axiom hosts GraphHopper in Docker Desktop. **Docker Desktop's VM memory must be ≥ 80 GB** for the
  whole-US graph (7.57 GB OOM-killed the first us-west import; 24 GB was enough for us-west alone).
  Compose sets `-Xmx48g`. Resident size while serving is ~19 GB.
- Custom model file **must not be named `motorcycle.json`** — GraphHopper ships a built-in with that
  name and refuses the clash. Ours is `serpentine.json`; the API profile name is still `motorcycle`.
- Corrupt SRTM tiles from an interrupted run (`Unexpected end of ZLIB input stream` on
  `/data/elevation/demNNNNNN`) → `rm -rf data/elevation data/graph-cache` and restart. Tiles come
  from `srtm.kurviger.de`.
- Whole-US import is **63 min** on the M4 Max (pass2 19 min over 161.7 M ways, subnetworks 46 s,
  landmarks 29.5 min), producing a 13 GB graph from an 11.3 GB extract: 70.5 M nodes, 87 M edges.
  us-west was ~25 min. Much longer than that means swapping — check Docker's VM memory first.
  **Import into a second container writing a different `graph.location` while the live one serves**
  (ADR-029): the swap is a compose edit and a restart, not an hours-long outage.
- `graph.dataaccess.default_type` must be `RAM_STORE`; plain `RAM` never writes `graph-cache/` and
  every restart re-imports.
- Round trips: POST key is `headings` (plural); `heading` is silently ignored. Downtown SD can't loop
  east/south (Mexico is outside the extract); Ramona can't loop due north. Loops overshoot distance 10–130 % and can snap vertices
  onto tracks — serpentine-api must generate and score several candidates.
- `bind_host: 0.0.0.0` in `config.yml`: port 8989 and the `/maps` tuning UI are open to the whole
  LAN. Fine, but the LAN is a public /24 (`204.128.136.0/24`) — anything that does "is this a
  private network?" checks will be confused (SABnzbd was). `https://serpentine.phfactor.net` is live
  (2026-09-18), **no auth by decision**. The repo's `infra/caddy/serpentine.caddyfile` allowlists
  `/route /info /health /nearest` (everything else → 404); deployed on the Pi and verified
  2026-09-18. Keep the Pi's `/etc/caddy/Caddyfile` block identical to the repo file.
- GraphHopper request shape (POST `/route`): `points` are `[lon, lat]`, `profile: "motorcycle"`,
  `algorithm: "round_trip"` + `round_trip.distance` (m) + `round_trip.seed` + `headings` for loops,
  `custom_model` for per-request tightening, `details: ["curvature","max_speed","urban_density",
  "road_class","surface"]` to get per-segment attributes back for scoring/diagnostics.

### Memento (shared agent memory)

Streamable-HTTP MCP at `http://webserver.phfactor.net:8321/mcp`, bearer token in
`~/.config/memento/agents-shared.env` on the Mac (`MEMENTO_AGENTS_SHARED_TOKEN`; Claude Code has it
registered at user scope as `memento`). Read `/projects/serpentine.md`, `/infrastructure/webserver.md`,
`/infrastructure/llm-inference-host.md`, `/skills/apple-release-pipeline.md`,
`/skills/privacy-and-third-party-policy.md` before large changes. Writes go propose → review → apply
via `memory_execute`; keep the serpentine page a prose summary ≤ ~2000 tokens, one subject.

## What "done" looks like for the POC

A `curl` to `https://serpentine.phfactor.net/route` returns a 150 km seeded loop from San Diego
that a rider would recognise as a good ride (Palomar / Mesa Grande / 79 / 76 territory), in < 3 s,
and `open "https://maps.apple.com/directions?…"` built from its waypoints starts Siri voice nav that
mostly follows the intended roads. After that, phases 1–3 in `docs/ROADMAP.md`.
