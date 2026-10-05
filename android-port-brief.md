# Serpentine — Android port brief

**Read this first if you are the Android instance.** Then [`docs/API.md`](docs/API.md), which is the
real spec, then [`sync.md`](sync.md) for the line-by-line parity matrix, then [`CLAUDE.md`](CLAUDE.md)
for the principles and the accumulated scar tissue.

Target repo: `phubbard/serpentine-android` (private, **not yet created**), checkout at
`~/code/serpentine-android`. iOS original is `phubbard/serpentine`, checkout at
`~/code/claude-cowork/serpentine`.

---

## The one thing that shapes everything

**This is a client port, not an app port.** Serpentine is client–server. Curvy routing, loop
generation and scoring, the energy model, charge-stop selection, charger reliability, the handoff URL
and GPX are all in `serpentine-api` on axiom, already running, already serving the iOS app, and
entirely platform-agnostic. You are writing a second client against a stable HTTP contract.

Concretely, you do **not** port: GraphHopper, custom models, the loop scorer, the energy model, NREL
or Open Charge Map integration, rate limiting, or anything in `server/`. If you find yourself
reimplementing route logic, stop — it belongs on the server, and changing it there serves both
clients at once.

What you **do** port: the UI, local persistence, location, map rendering, place search, and the
navigation handoff. That is roughly `ios/Serpentine/Views/` and `ios/Serpentine/Model/`, about 2,000
lines of Swift, against an API surface of two POST endpoints and a handful of GETs.

## Decisions locked in

- **Kotlin 2.x + Jetpack Compose.** Same as mapbook-android; no reason to differ.
- **The server is the source of truth.** Any behaviour change that isn't purely presentational is a
  server change, made once, benefiting both clients.
- **One network peer.** The app talks to `https://serpentine.phfactor.net` and nothing else —
  *except* whatever map stack we settle on. That exception is unresolved; see Open question #1 below
  and in sync.md. Do not add analytics, crash reporting SDKs, font CDNs or ad networks. Ever.
- **No subscription.** One-time purchase is a product principle, not a pricing experiment.
- **Distribution via the Play internal testing track**, mirroring TestFlight as the "shipped" bar.
  Paul's Play Console account and signing setup already exist from mapbook — see
  `/projects/mapbook-android.md` in Memento before reinventing any of it.

## Unresolved before you write map code

**Do not pick a map stack on day one.** CLAUDE.md principle 1 exists because of a house privacy rule
(`/skills/privacy-and-third-party-policy.md` in Memento) and is published to users at `/v1/privacy`.
On iOS, MapKit satisfies it — Apple's own, already on the device. On Android the obvious choice is the
Google Maps SDK, which means the app talks to Google. mapbook-android does exactly that, so there is
precedent; but Serpentine's privacy page makes stronger claims. **Ask Paul.** The alternatives are
self-hosted vector tiles with MapLibre (already on the roadmap for the web test page, keeps the rule,
costs more work) or accepting Google and amending the privacy page for Android.

The same question applies to place search: iOS uses `MKLocalSearch`; the Android equivalent is Google
Places.

## Stack (proposed, confirm before building)

| Concern | Choice | Notes |
|---|---|---|
| UI | Jetpack Compose + Material 3 | |
| DI | Hilt | Matches mapbook-android. |
| HTTP | Retrofit + OkHttp, or Ktor | Trivial surface: two POSTs, three GETs. **Must honour 429 + `Retry-After`.** |
| JSON | kotlinx.serialization | Wire is snake_case; configure the naming strategy once, centrally. |
| Maps | ❓ unresolved | See above. |
| Location | FusedLocationProviderClient | Foreground only. No background tracking — the app deliberately does not know where anyone has been. |
| Local storage | Room for saved rides, DataStore for the garage and prefs | Saved rides store the **whole server answer**; see below. |
| Nav handoff | ❓ unresolved | See "Known gotchas". |

## Project layout (sketch)

```
app/src/main/java/net/phfactor/serpentine/
  api/        SerpentineApi, wire DTOs mirroring docs/API.md
  model/      Planner, ReachFinder, RideStore, RideProgress, Garage   (ports of ios/Serpentine/Model/)
  ui/plan/    PlanScreen        — start, mode, budget, twistiness, charging
  ui/ride/    RideScreen        — map, stats, handoff, GPX, charge detail, save, pause
  ui/reach/   ReachScreen       — "find a charger", sorted by share of pack
  ui/rides/   SavedRidesScreen  — rating, notes, last ridden
  ui/garage/  GarageScreen      — bikes, catalogue, adapters
  ui/about/   AboutScreen       — attributions (licence conditions), build stamp
```

## Phasing

Phases 1 and 2 deliver a usable app for a petrol rider and need no energy-model confidence. The
battery work is deliberately later, because the model is unvalidated (Open question #3).

### Phase 0 — Setup

- Repo, Gradle, Compose skeleton, Hilt, CI-less build matching the house style (local Makefile or
  Gradle tasks; **no cloud CI** — see CLAUDE.md conventions).
- Wire DTOs from `docs/API.md`, plus a round-trip test against a **real recorded response**. The iOS
  side keeps fixtures in `ios/SerpentineTests/Fixtures/` — copy them; they are real server answers.
- Settle Open question #1 (map stack) with Paul.

### Phase 1 — Plan and show a ride

- Plan screen: start (current location or search), loop / out-and-back / go-somewhere, distance or
  time budget, twistiness, direction.
- Ride screen: polyline, start/turnaround pins, distance / time / climb, named roads.
- "Another ride" (seed stepping, step by two).
- At the end of this phase a petrol rider has a working app.

### Phase 2 — Getting the ride out of the app

- Navigation handoff (research first — see gotchas).
- GPX share via `FileProvider`.
- Saved rides: explicit save, star rating, notes, last-ridden date; reopens offline.
- Pause and carry on (ADR-035) — note the rejoin-ahead rule and the loop ambiguity.

### Phase 3 — Battery

- Garage: catalogue from `GET /v1/vehicles`, editable numbers, adapters.
- Charge stops on a planned ride; arrival reserve toggle.
- Reach ("find a charger") and the efficient route to one.
- Gate this phase on the energy model being calibrated against a real bike.

### Phase 4 — Polish and release

- Tablet layout, About attributions, Play internal track, store listing, Data safety declaration.

## iOS → Android API mapping

| iOS | Android | Notes |
|---|---|---|
| `MKLocalSearch` | Google Places SDK | ❓ privacy question. Rows show name, address, straight-line distance. |
| `MapKit` / `MapPolyline` | `google-maps-compose` `Polyline`, or MapLibre | ❓ unresolved. |
| `CoreLocation` / `CLLocationManager` | `FusedLocationProviderClient` | Foreground only; `ACCESS_COARSE` is not enough for the reach radius to be meaningful. |
| `UserDefaults` | DataStore | Garage, selected bike, prefs. |
| Application Support JSON files | Room, or app-private files | Saved rides and the paused ride. |
| `ShareLink` | `ACTION_SEND` + `FileProvider` | GPX. |
| `URLSession` (ephemeral) | OkHttp with no disk cache | Nothing about a planned ride should touch disk except what the rider explicitly saved. |
| Apple Maps unified URL | ❓ | The hard one. |
| `@Observable` + `@Environment` | ViewModel + `StateFlow` + Hilt | |

## Known gotchas to flag now

1. **Coordinates are `[lon, lat]`** everywhere on the wire — GraphHopper's convention — *except*
   inside Maps URLs, which want `lat,lon`. This has bitten the iOS side.
2. **The navigation handoff is the real risk.** iOS hands Apple Maps an ordered waypoint list and lets
   it re-route between them, which is how a curvy route survives into voice guidance with ~10 stops.
   Android has no equivalent guarantee; Google Maps URL waypoint limits are lower and poorly
   documented. **Measure what actually works before designing the screen around it.** If no handoff
   preserves the route, GPX export to a navigation app may be the honest answer, and that changes the
   product story on Android.
3. **`charging` needs `"enabled": true`** or the server silently ignores the whole block.
4. **`soc_min_arrival` defaults to 0.15 and the check is `>=`**, so a rider at 15 % is refused unless
   `style` is `efficient`. The iOS side shipped this bug; don't repeat it.
5. **Saved rides store the server's whole answer**, not the request (ADR-033). Replanning on open
   looks cheaper and is wrong: the route can come back different after a graph re-import, and the
   list must work with no signal.
6. **Rate limits are real**: 24 plans in flight server-wide, 20/min per caller. Handle 429 and respect
   `Retry-After` rather than retrying blindly. One reach call costs the server several routes.
7. **Attribution is a licence condition.** OpenStreetMap (ODbL), NASA SRTM, DOE AFDC and Open Charge
   Map (CC BY-SA) must appear in the About screen. Not optional, not a nicety.
8. **No background location, no analytics, no crash SDK.** The privacy position is published and is a
   selling point. Play's Data safety form must be able to say "no data collected" truthfully.
9. **Charging estimates are deliberately cautious and still unvalidated.** Don't present them more
   confidently than iOS does. Being wrong here loses trust faster than a bad route.

## Suggested first actions

1. Read `docs/API.md` end to end, then `curl` the live API — it is public, no auth:
   `curl -s -X POST https://serpentine.phfactor.net/v1/plan -H 'Content-Type: application/json' -d '{"mode":"loop","start":[-116.8681,33.0417],"distance_m":120000,"twistiness":0.6,"seed":3}'`
2. Copy `ios/SerpentineTests/Fixtures/*.json` into the Android repo as decode fixtures. They are real
   responses, which is why the iOS tests catch wire changes.
3. Put the map-stack question (Open question #1) to Paul before writing map code.
4. Skim `ios/Serpentine/Model/` — `Planner`, `ReachFinder`, `RideStore`, `RideProgress` are small and
   are the logic you are actually porting. The comments explain *why*, which is the part worth keeping.
5. Update your column in [`sync.md`](sync.md) as you go. That file is the contract between the two
   sessions; a feature isn't done until its row says so.
