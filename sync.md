# Serpentine — cross-platform sync

**Purpose:** living state-of-the-world between the Apple build (this repo, `phubbard/serpentine`) and
the Android port (`phubbard/serpentine-android`, separate repo, not yet created). Anything either side
ships, fixes, or decides goes here so the other side can mirror or diverge with eyes open.

**Audience:** the two Claude instances actively working on Serpentine — one on Apple platforms
(iOS / iPadOS / Mac Catalyst), one on Android (Kotlin + Compose).

**The thing that makes this port different from mapbook's:** Serpentine is **client–server**. Every
hard problem — curvy routing, loop generation and scoring, the energy model, charge-stop selection,
charger reliability, Apple/Google Maps handoff URL construction, GPX — lives in `serpentine-api` on
axiom and is already platform-agnostic. `docs/API.md` is the contract both clients code against. The
Android port is **a second client**, not a reimplementation. Expect the matrix below to be mostly UI
and local persistence, and expect the server rows to say "n/a — shared".

**Status (2026-10-05):** Apple side is 0.1.0 build 76 on TestFlight (iOS + iPad + Mac Catalyst),
internal group only, preparing a public beta. Android side started 2026-10-05 in
`phubbard/serpentine-android` (private): planning and the ride screen are on `main`, nothing is on
Play yet, so the Android column has no ✅.

**Status conventions:**

- ✅ shipped (and in a release the user can install — Play internal track counts, mirroring TestFlight)
- 🟡 on `main` but not in a public release yet
- 🚧 in progress
- ❌ not yet started
- ⛔ deliberately won't do on this platform (with reason)
- ❓ open decision
- n/a — shared: lives in `serpentine-api`, no per-platform work

**How to update this doc:** when you ship, change the column for *your* platform. Add a one-line `Δ`
(delta) note at the bottom of the relevant section if the change has UX implications the other
platform should know about. If a decision needs the other side's input, mark it ❓ and add the
question to [Open questions](#open-questions).

**Companion docs:**
- [`android-port-brief.md`](android-port-brief.md) — Android scoping doc. **Read this first if you're the Android instance.**
- [`docs/API.md`](docs/API.md) — the server contract. The real spec for both clients.
- [`CLAUDE.md`](CLAUDE.md) — architecture, principles, conventions, scar tissue. Principle 1 is load-bearing and Android complicates it; see Open question #1.
- [`docs/DECISIONS.md`](docs/DECISIONS.md) — ADR log. ADR-034/035 (reach, pause) are the newest user-facing behaviour.

---

## Feature matrix

### Planning a ride

| Feature | Apple | Android | Notes |
|---|---|---|---|
| Loop from a start point | ✅ | 🟡 | `POST /v1/plan` mode `loop`. Server generates and scores 16 candidates (ADR-010); the client just asks. |
| Out-and-back, different return road | ✅ | 🟡 | mode `out_and_back` (ADR-015). |
| Go somewhere (A→B) with a detour budget | ✅ | 🟡 | mode `point_to_point`, `max_extra_s`. "Quickest way plus up to N minutes on better roads." |
| "Just get me there" errand ride | ✅ | 🟡 | `style: "direct"` (ADR-031). Freeways allowed, fastest, charging still planned. |
| Plan by distance | ✅ | 🟡 | `distance_m`, 20–500 km. |
| Plan by time ("two hours on Saturday") | ✅ | 🟡 | `duration_s` (ADR-018). Server does the speed guessing and rescaling. |
| Twistiness slider | ✅ | 🟡 | 0..1 → per-request custom model server-side. |
| Direction ("head north") | ✅ | 🟡 | `heading_deg`, or omit for "any". |
| "Another ride like this" | ✅ | 🟡 | Seed stepping; server plans seeds n and n+1, so the client steps by two. |
| Start from current location | ✅ | 🟡 | iOS CoreLocation; Android the platform `LocationManager` (not Google's fused-location SDK), asking for high accuracy by name. |
| Start/destination place search | ✅ | 🟡 | iOS `MKLocalSearch`. Android: Google Places SDK (decided 2026-10-05, see Resolved). Rows show name, address and straight-line distance. |

### The ride

| Feature | Apple | Android | Notes |
|---|---|---|---|
| Route drawn on a map | ✅ | 🟡 | iOS MapKit `MapPolyline`. Android: `google-maps-compose` `Polyline`. Server returns `polyline` as `[lon, lat]` pairs — note the order. |
| Distance / time / climb summary | ✅ | 🟡 | Straight from the response. Time shows "incl. charging" only when stops exist. |
| Start, turnaround and charge-stop pins | ✅ | 🟡 | Android: start and turnaround seen on a device; charge-stop pins are coded but wait on the battery work. |
| Named roads with distances | ✅ | 🟡 | `roads[]`, filtered to ≥ 2 km and non-empty names. |
| Hand off to voice navigation | ✅ | 🟡 | iOS: Apple Maps unified URL, ~10 stops, `avoid=tolls,highways` on curvy rides only (ADR-013, ADR-031). Android: the server's `google_maps_url` (now ≤ 9 waypoints, see Δ below) with the origin removed and `dir_action=navigate` added, which starts guidance from wherever the rider is. Measured 2026-10-05 on Maps 26.39. |
| Share GPX | ✅ | 🟡 | `GET /v1/plan/{id}.gpx`. Android: `FileProvider` + `ACTION_SEND`. |
| Share one GPX that carries everything | ✅ | 🟡 | `?for=all` — route + charge stops in one file (ADR-041). Android: done 2026-10-22, fetched when the ride appears, with the iOS help sheet's wording under the button. |
| Charge stops with arrival %, dwell, backup | ✅ | 🟡 | Rendering only; all computed server-side. |
| Charger reliability notes (Open Charge Map) | ✅ | 🟡 | `reliability` on each charger (ADR-022). Absent means "not listed there", which is not the same as "fine" — say so. |

Δ **Google Maps handoff, measured 2026-10-05 (server commit `e08bf8a`, not yet deployed as of this note):**
given ten waypoints the Google Maps app keeps the first nine without a word, so the last road
loses its pin; `google_maps_url` now carries at most nine, dropping the waypoint for the shortest
road and never a forced stop. And `avoid=highways` on a ride that uses two miles of I-8 made Google
ride seven miles round to dodge it (100 mi against our 93), so Google is told to avoid highways only
when our route touches no motorway or trunk road. **Measured on Apple 2026-10-06 and fixed.** Alpine →
El Cajon on I-8: 16 min / 15 mi became 26 min / 14 mi with `avoid=tolls,highways`, a 63 % longer
ride. Apple is the gentler of the two — it keeps the fast route as a second option and warns "a
faster route is available with highways" — but the detour is the one it selects, so it is the ride a
rider would start. Both URLs now share one `avoidList`, so neither asks a maps app to dodge a road we
chose on purpose. Caveat on the measurement: it was a bare A→B with no waypoints, which gives the
avoid parameter maximum freedom; our real handoff pins the route with up to ten waypoints, so the
damage on a genuine ride is bounded by what it can do between consecutive stops.

### Battery features

| Feature | Apple | Android | Notes |
|---|---|---|---|
| Plan charge stops along a ride | ✅ | 🟡 | `charging` block in the request. Needs `"enabled": true` or it is silently ignored. |
| Starting charge, arrival reserve toggle | ✅ | 🟡 | `soc_start`, `reserve_for_backup` (ADR-023). |
| The garage: multiple bikes | ✅ | 🟡 | Catalogue from `GET /v1/vehicles`, hand-edited numbers, adapters kept per-rider not per-bike (ADR-020, ADR-025). Local storage. |
| **Find a charger** (low battery) | ✅ | 🟡 | `POST /v1/reach` (ADR-034). Sorted by share of pack needed, not distance. Charge slider is the rider's own reading; everything else follows from it. |
| Minimum-energy route to a charger | ✅ | 🟡 | `style: "efficient"`. Comes back as an ordinary plan, so it reuses the ride screen. |
| Pause a ride / carry on afterwards | ✅ | 🟡 | ADR-035. Stores the *remaining polyline* locally, resumes via `via` points from wherever the rider now is. Loop start/finish ambiguity is real — show the split and allow undo. |

### Keeping rides

| Feature | Apple | Android | Notes |
|---|---|---|---|
| Save a ride | ✅ | 🟡 | Explicit, not automatic history (ADR-033) — "Another" is the most-used control and would otherwise flood the list. |
| Star rating, notes, last-ridden date | ✅ | 🟡 | Android: "I rode this today" or not ridden; no picker for an arbitrary date yet. |
| Saved ride reopens offline | ✅ | 🟡 | iOS stores the whole server answer: `index.json` + one plan file each under Application Support. ~108 KB for an 87-mile charging loop. Android: Room or files — **storing the request and replanning is the wrong answer**, see ADR-033. |

### Shell

| Feature | Apple | Android | Notes |
|---|---|---|---|
| About panel with data attributions | ✅ | 🟡 | OpenStreetMap ODbL, NASA SRTM, DOE AFDC, Open Charge Map CC BY-SA are **licence conditions, not courtesy** (ADR-032). Apple's own attribution is MapKit-specific; Android will need Google's equivalent instead. |
| Build stamp (date + commit) in About | ✅ | 🟡 | So a tester's bug report says exactly what they are running. |
| Tablet / large-screen layout | ✅ (iPad) | 🟡 | iOS uses `NavigationSplitView`. |
| Desktop | ✅ (Mac Catalyst) | ⛔ | No Android desktop target planned. |

### Server (shared — no per-platform work)

| Capability | Where | Notes |
|---|---|---|
| Curvy routing, loop scoring, out-and-back | `serpentine-api` + GraphHopper on axiom | ADR-009/010/015. Whole-US graph. |
| Energy model, charge stops, reach | `serpentine-api` | ADR-008/014/025/034. **Never validated against a real bike** — see Open question #3. |
| Charger data (NREL daily bulk, Open Charge Map) | `serpentine-api` | Keys live only on axiom and must never reach either client. |
| Rate limits | `serpentine-api` | 24 in flight, 20/min per caller, 429 + `Retry-After`. **Both clients must handle 429 gracefully** and respect `Retry-After`. |
| Health + ops dashboard | axiom, Pi watch | ADR-037. LAN-only. |
| Platform counts | `serpentine-api` | ADR-038, server from 2026-10-07. Tallies which kind of device asked, per hour. Android sends `android` on every request from 2026-10-22. |

---

## Shared data model

The wire types in [`docs/API.md`](docs/API.md) are the contract. Both clients mirror them; neither
invents its own. Notes that bit the iOS side and will bite Android:

- **Coordinates are `[lon, lat]` everywhere** except inside Maps URLs, which want `lat,lon`.
- `PlanResult` carries `style` (point-to-point only), echoed so a client knows what it got back.
- Charging is opt-in via `charging.enabled`; omitting it means no `energy` and no `chargers`.
- `soc_min_arrival` defaults to 0.15 and the check is `>=`, so **a rider at 15 % is refused** unless
  the style is `efficient` (which defaults it to 0). This caught the iOS side in exactly the case the
  feature exists for.
- iOS stores saved rides and the paused ride as the server's own JSON, round-tripped through the same
  snake_case coder pair. That makes the wire format the storage format: **removing an API field stops
  old saved rides loading.** Android should make the same trade knowingly, or store a translated copy.

---

## Platform-specific bits

### Apple-only (won't port)

- Mac Catalyst build and its menu-bar About panel.
- Apple Maps unified-URL handoff (Android needs its own answer — see the matrix).
- `-screenshotPlan` launch argument for App Store screenshots without tapping.

### Android-only

- A build with no Maps key still runs: the ride screen draws a bare sketch of the route instead of
  a map, and place search says it isn't set up.
- The map is fixed at the top of the ride screen and the numbers scroll under it, rather than the
  map scrolling with the list as on iOS.
- Google's legal notices and Maps terms are linked from About (a condition of its Maps terms).
- The saved-ride editor has "I rode this today" but no picker for an arbitrary last-ridden date.

---

## For the Android side

*Everything below was picked up on 2026-10-22 (`?for=all`, the help sheet, the platform header).
Items stay here as the record of why; add new ones at the bottom.*

- **GPX now takes `?for=waypoints|route|track`** (ADR-039, server-side, so Android gets it free;
  deployed 2026-10-14). Three files, one import mode each, for the Beeline Moto II. Omitting `for`
  still returns the track exactly as before, so the existing Share GPX keeps working untouched.

  The app-side half shipped on iOS the same day and is worth copying rather than redesigning:

  - **Share GPX is a menu, not a button**: Charge stops / Route outline / Exact path, then a
    separator and "Which one do I want?". Charge stops leads because it is the right answer for a
    Beeline; Exact path is the old behaviour and the one Kurviger or a Garmin wants.
  - **All three are fetched when the ride appears**, not on tap. A real charging loop measured
    413 B of waypoints and 1.5 KB of route against 136 KB of track we were fetching anyway, so the
    extra two are free, and a share sheet that stalls on a download is worse than a slower plan.
  - **Three distinct filenames** (`serpentine-<id>-<kind>.gpx`), because all three can end up in the
    same folder and the name is what the rider reads while importing.
  - **The help sheet carries two warnings** that only matter once the rider has left: a Beeline's
    arrival time excludes charging and so always reads earlier than ours, and a device reroute
    silently discards the charge plan — pause the ride and carry on to get a fresh one. Above 25
    charge stops it says the count is untested territory rather than implying it works.

  None of this is confirmed against hardware yet; the acceptance checklist lives in ADR-039.

- **A Beeline shows waypoints as "Waypoint 1 / 1", not by name** (ADR-042). Tested with a waypoint
  named plainly `Park`, then confirmed by dumping the Mac app's whole accessibility tree for the
  route preview: five elements, none of them the name. Keep sending the rich names — other GPX apps
  do show them — but don't build any UI that promises the rider will read the charge plan off the
  device.

- **Don't test with a short route.** A 1.11 mi out-and-back imported as 1.9 mi — 71 % long — because
  Beeline matches to "the closest suitable roads" and 33 points along one street won't stop it going
  round the block instead of making a U-turn. The same exporter was within 0.1 mi over 96.8 mi. Test
  with a real ride or you will chase a bug that isn't there.

- **Demo rides: `GET /v1/rides`** (ADR-043, deployed 2026-10-26). Eight roads worth knowing — the
  Dragon, the Million Dollar Highway, the Beartooth — that plan in one tap. On iOS it's a "Ride a
  famous road" row in the Start section, which is the first useful thing a rider with no location
  can do; two taps from a cold launch to a planned ride.

  Three things to copy rather than rediscover:

  - **Entries are mostly `point_to_point`, not `out_and_back`.** A famous road is usually the only
    road, and out-and-back's corridor penalty forbids retracing, so it refuses them. Every entry
    failed the first time for this reason.
  - **Filter on `country`.** All eight are `US` because the graph is; an Android build still
    installed when that changes must not offer a ride it cannot plan.
  - **Verify against the graph, not the JSON.** `tools/check_rides.py` plans each entry and asserts
    the named road is in the result. First run: 3 of 8 — two wouldn't plan and three rode town
    streets instead of the skyway, which no unit test can see.

- **Use `?for=all`, and delete any mode picker you were about to build** (ADR-041, server-side,
  deployed 2026-10-21). Tested against a real Beeline Moto II, and the ADR-039 premise the picker
  existed for is simply false: the device *asks* on import — Turn-by-turn / Breadcrumb / Waypoints
  only — and builds that menu from what the file holds. One file offers all three.

  What the file decides is how much of the route survives, and it is savage. Same rides,
  Turn-by-turn each time:

  | file | points | result |
  |---|---|---|
  | charge stops | 3 | 44 mi → **28.0 mi, 84 % highways** |
  | route outline | ~12 | 43 mi → 41.7 mi, 31 % highways |
  | `all` | 2,255 | 96.8 mi → **96.7 mi**, no highway warning |

  iOS has dropped its three-way picker for a single Share GPX button plus a help sheet that says
  "say Turn-by-turn". Charge stops arrive as waypoints the device counts down ("Waypoint 1 / 1"
  on screen), so the plan does travel with the ride.

- **The waypoints export now carries the ride, not just the stops** (ADR-040, server-side, so
  Android gets it free; deployed 2026-10-21). First Beeline hardware test imported a one-stop ride
  as **0.0 miles**, because the file held a single `<wpt>` and one point is not a route. It is now
  start → stops in route order → finish, with an out-and-back's turnaround included. No client
  change needed, but two things to copy:

  - **Don't offer Charge stops when the ride has none.** The server 400s that export (the file would
    be empty) and the iOS menu simply omits it. Whatever explains the modes must be built from the
    list the menu is actually showing, or it will describe a choice that isn't there — that shipped
    in iOS build 99 and came back as TestFlight feedback within a day.
  - **Route mode re-routes more than expected and is still under test.** A 43 mi curvy ride imported
    as 41.7 mi and 31 % highways. Don't harden any advice about which mode to recommend until the
    three-way comparison in ADR-040 is done.

- **Send `X-Serpentine-Platform: android`** on `/v1/plan` and `/v1/reach` (ADR-038, live on the
  server from 2026-10-07). One header, no body change, no new field — it is a header precisely so it
  stays out of the plan cache key, which hashes the request body. The server allowlists
  `ios | ipad | mac | android | web` and counts anything else as `other`, so until Android sends it
  the Android rides are tallied as `other` rather than counted wrongly. Apple sends `ios`, `ipad` or
  `mac`, resolved once at launch.

---

## Open questions

1. **Resolved 2026-10-05 — Google Maps + Places on Android.** See [Resolved](#resolved). The number
   is kept so references to "open question 2" and onward still point at the right thing.
2. **❓ What is the Android voice-navigation handoff?** The iOS answer (Apple Maps unified URL, ~10
   waypoints) is load-bearing — it is the whole "voice nav" story and it is why the minimum is iOS
   18.4. Android needs an equivalent that preserves the *route we chose* rather than letting the nav
   app pick its own. Candidates: Google Maps `dir` URL (waypoint limits unclear), OsmAnd intents,
   Calimoto/Kurviger handoff, or a GPX export workflow. **Research first, design second.**
3. **Resolved 2026-10-05 — Paul: build the battery features now.** See [Resolved](#resolved).
4. **❓ Distribution bar.** mapbook-android uses the Play internal testing track as the ✅ bar,
   mirroring TestFlight. Assume the same here unless Paul says otherwise. His Play Console account is
   already set up (see `/projects/mapbook-android.md` in Memento).
5. **❓ Gas bikes and the app name.** An open product question on the Apple side that Android inherits:
   both current testers ride petrol bikes, the listing is "Serpentine EV", and the server refuses any
   vehicle that isn't electric. If that changes, it changes both clients.

### Resolved

- **Pricing — Paul, 2026-10-27: free, donation-supported (ADR-044).** No price, no subscription, no
  IAP, no paid tier, and **no payment or billing code in either client**. The only ask is a Buy Me a
  Coffee link on the web pages, which both apps already reach through their About panel. Android
  should mirror this: nothing to add to the app, and nothing to declare to Play about purchases.
  The consequence that matters for both clients is positional — with no revenue to defend, privacy
  is the product's distinguishing claim, so the shop window leads with it and neither client should
  acquire a third-party SDK that would make the claim smaller.

- **Battery features on Android (was open question 3) — Paul, 2026-10-05: built without waiting
  for the calibration ride.** Garage, charge stops, reserve toggle and find-a-charger are on
  Android's `main`, rendering the same server answers iOS does. The gate on the public beta is
  unchanged: nothing ships to riders on either platform until the model has met a bike.
- **Map stack on Android (was open question 1) — Paul, 2026-10-05: Google Maps SDK + Places SDK.**
  Principle 1 now reads, for Android, "one host plus Google's map and search", the way it reads
  "one host plus Apple MapKit" on iOS. Consequences both sides should know:
  - `server/web/privacy.html` carries an Android paragraph. It is blunter than the Apple one because
    Google's SDKs report on their own use (IP address, device model, a pseudonymous SDK identifier,
    map pan/zoom events, their own crash reports — per Google's Play data disclosures for the two
    SDKs). Re-read those disclosures when either SDK is updated.
  - **Play's Data safety form can no longer say "no data collected"** the way mapbook-android's
    does without checking: the brief's gotcha 8 needs re-deciding against Google's disclosure pages
    before the first Play upload.
  - Places text search is billed per request beyond a monthly free allowance, which principle 6
    ("no per-request third-party billing") did not anticipate. The Android app debounces and needs
    three characters before searching; a daily quota cap on the key keeps the cost bounded.
  - Still no analytics, crash SDK, ad network or font CDN. Location stays the platform
    `LocationManager`, not Google's fused-location SDK.
  - CLAUDE.md principle 1 and `android-port-brief.md` still describe this as unresolved; the Apple
    session should fold the decision in when it next touches them.

---

## Glossary (shared vocabulary)

- **Reach** — the "I'm low, what can I get to" feature. Chargers ranked by the fraction of the pack
  the ride there would cost. Not distance (ADR-034).
- **Efficient style** — minimum-energy routing, used for the ride to a charger. Not fastest, not curvy.
- **Direct style** — the errand ride. Freeways fine, fastest route (ADR-031).
- **Paused ride** — a ride put down part-way, stored as the remaining geometry, resumed by rejoining
  *ahead* of where you stopped so a charging detour isn't ridden twice (ADR-035).
- **Twistiness** — 0..1 rider preference, mapped server-side to a per-request custom model. Under LM
  it may only ever *tighten* the baked-in profile (ADR-009).
- **SoC** — state of charge, 0..1 on the wire, shown as a percentage.
- **Arrival reserve** — arrive at a charge stop with enough left to reach another one (ADR-023).
