# serpentine

Curvy-road motorcycle ride planner for iPhone, iPad and Mac (Catalyst). Favours roads with plentiful turns, moderate speeds and
minimal traffic over minimum time; plans charge stops for electric bikes; generates loops and out-and-back
rides; hands off to Apple Maps for voice navigation. No subscription.

Plan by distance, by the time you have ("two hours on Saturday"), or A→B with a detour budget — the
quick way plus however long you'll spend on better roads. Status: routing engine and API running in-house; app v0.1 on TestFlight for iPhone, iPad and Mac as
"Serpentine EV". Product page: <https://serpentine.phfactor.net/v1/about>; support:
<https://serpentine.phfactor.net/v1/support>; privacy policy: <https://serpentine.phfactor.net/v1/privacy>. `serpentine-api` plans
point-to-point rides, scored loops and out-and-backs with charge stops (plus backup chargers) and an
Apple Maps handoff; try it at
<https://serpentine.phfactor.net/v1/>.

- `FEASIBILITY.md` — market survey, technical feasibility, costs, risks, sources (Sept 2026)
- `server/` — serpentine-api (Go, stdlib only): loops, time budgets, per-bike charging against a local
  daily copy of every US public charger, with
  charger-reliability checks, Apple Maps handoff, the
  vehicle catalog (`vehicles.json`, served at `/v1/vehicles`), browser test page
  with a map (vendored Leaflet, cached OSM tiles)
- `infra/` — GraphHopper 11 motorcycle profile (whole-US graph), docker-compose, Caddy site block,
  setup + smoke tests
- `demo/`, `tools/` — a hardwired demo ride and the script that routes it
- `ios/AppStore/screenshots/` — App Store screenshots: 6.9" (1320×2868), 6.5" (1284×2778), 13" iPad
  (2064×2752), Mac (2880×1800); `upload/` holds the opaque JPEGs ASC takes
- `docs/` — roadmap, decisions (ADR-001..027, including what the charging research found and what it
  rules out), API contract
- `CLAUDE.md` — orientation for agents and future me: architecture, conventions, gotchas
