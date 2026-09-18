# CLAUDE.md — serpentine

Curvy-road motorcycle ride planner for iPhone. Favours plentiful turns, moderate posted speeds and low
traffic over minimum time; plans charge stops for a Zero SR/S; generates loops and out-and-back rides;
hands off to Apple Maps for voice navigation. **No subscription, no telemetry, no third-party runtime
dependencies in the app.** Owner: Paul Hubbard (pfh@phfactor.net). Solo project, self-hosted.

Read this file first. Then `docs/ROADMAP.md` for what to build next and `docs/DECISIONS.md` for why
things are the way they are. `FEASIBILITY.md` is the September 2026 research that started this and is
the reference for competitor and API facts; don't re-research what it already answers.

## Status (2026-09-18)

- Feasibility done. Routing engine POC stood up on axiom, first successful import pending (see infra gotchas).
- No app code yet. No server API code yet. `infra/` is the only executable content.
- GitHub: `git@github.com:phubbard/serpentine.git`, branch `main`.
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
server/              (phase 1) Go API in front of GraphHopper + NREL — not yet created
ios/                 (phase 2) SwiftUI app — not yet created
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
   ├─► GraphHopper 11, :8989, Docker, us-west graph, profile "motorcycle" (LM/hybrid mode)
   ├─► NREL AFDC API (chargers along route, filtered J1772 / TESLA L2)   ← server-side, key never on phone
   └─► (later) elevation, HPMS AADT, cached tiles
```

Principles that constrain every design choice here:

1. **The phone talks to serpentine.phfactor.net and nothing else.** No Mapbox, no analytics, no font CDN,
   no NREL key on device. Map display is MapKit (Apple, free, already on the phone). This is Paul's
   house privacy rule (`/skills/privacy-and-third-party-policy.md` in Memento) and it is not negotiable.
2. **Routing cost is data-driven and tunable without a re-import.** All "curvy / moderate / rural"
   logic lives in `infra/graphhopper/custom_models/serpentine.json`. Changing multipliers = restart.
   Adding an encoded value = re-import (~1 h). Per-request `custom_model` may only tighten (≤1
   multipliers) because the profile uses LM preparation.
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
- No secrets in the repo, ever. `*.env`, `*.local.xcconfig`, `.p8`, tokens → gitignored. The
  `.gitignore` already covers `*.env`, `data/`, `*.osm.pbf`, `graph-cache/`, Xcode user state.
- No cloud CI. Everything builds and ships from the dev Mac via Makefile. Copy
  `apple-deployment-playbook.md` from `~/code/mapbook/` into `ios/` when that phase starts and follow it.
- Write scar tissue down: every gotcha that cost more than 10 minutes goes in `infra/README.md`
  (ops) or `docs/DECISIONS.md` (design). Future agents read those before touching anything.

### Apple specifics (from `/projects/apple-developer-account.md`, Memento)

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

- Axiom hosts GraphHopper in Docker Desktop. **Docker Desktop's VM memory must be ≥ 24 GB** (default
  was 7.57 GB and OOM-killed the import). Compose sets `-Xmx14g`.
- Custom model file **must not be named `motorcycle.json`** — GraphHopper ships a built-in with that
  name and refuses the clash. Ours is `serpentine.json`; the API profile name is still `motorcycle`.
- Corrupt SRTM tiles from an interrupted run (`Unexpected end of ZLIB input stream` on
  `/data/elevation/demNNNNNN`) → `rm -rf data/elevation data/graph-cache` and restart. Tiles come
  from `srtm.kurviger.de`.
- Pass1 of the us-west import is ~50 s on the M4 Max; the whole import incl. urban density and LM
  prep should be well under an hour. If it's much longer, something is swapping.
- `bind_host: 0.0.0.0` in `config.yml`: port 8989 and the `/maps` tuning UI are open to the whole
  LAN. Fine, but the LAN is a public /24 (`204.128.136.0/24`) — anything that does "is this a
  private network?" checks will be confused (SABnzbd was). Caddy exposes only `/route /info /health
  /nearest /isochrone` publicly, behind basic auth.
- GraphHopper request shape (POST `/route`): `points` are `[lon, lat]`, `profile: "motorcycle"`,
  `algorithm: "round_trip"` + `round_trip.distance` (m) + `round_trip.seed` + `heading` for loops,
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
