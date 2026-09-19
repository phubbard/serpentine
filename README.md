# serpentine

Curvy-road motorcycle ride planner for iPhone. Favours roads with plentiful turns, moderate speeds and
minimal traffic over minimum time; plans charge stops for a Zero SR/S; generates loops and out-and-back
rides; hands off to Apple Maps for voice navigation. No subscription.

Status: routing engine and API running in-house; iOS app not started. `serpentine-api` plans
point-to-point rides and scored loops with charge stops and an Apple Maps handoff; try it at
<https://serpentine.phfactor.net/v1/>.

- `FEASIBILITY.md` — market survey, technical feasibility, costs, risks, sources (Sept 2026)
- `server/` — serpentine-api (Go, stdlib only): loops, charging, Apple Maps handoff, browser test page
- `infra/` — GraphHopper 11 motorcycle profile, docker-compose, Caddy site block, setup + smoke tests
- `demo/`, `tools/` — a hardwired demo ride and the script that routes it
- `docs/` — roadmap, decisions, API contract
- `CLAUDE.md` — orientation for agents and future me: architecture, conventions, gotchas
