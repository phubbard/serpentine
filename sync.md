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
| Charge stops with arrival %, dwell, backup | ✅ | ❌ | Rendering only; all computed server-side. |
| Charger reliability notes (Open Charge Map) | ✅ | ❌ | `reliability` on each charger (ADR-022). Absent means "not listed there", which is not the same as "fine" — say so. |

Δ **Google Maps handoff, measured 2026-10-05 (server commit `e08bf8a`, not yet deployed as of this note):**
given ten waypoints the Google Maps app keeps the first nine without a word, so the last road
loses its pin; `google_maps_url` now carries at most nine, dropping the waypoint for the shortest
road and never a forced stop. And `avoid=highways` on a ride that uses two miles of I-8 made Google
ride seven miles round to dodge it (100 mi against our 93), so Google is told to avoid highways only
when our route touches no motorway or trunk road. **The Apple URL has the same trap and is
unmeasured** — worth checking the next time a ride with a freeway hop is handed to Apple Maps.

### Battery features

| Feature | Apple | Android | Notes |
|---|---|---|---|
| Plan charge stops along a ride | ✅ | ❌ | `charging` block in the request. Needs `"enabled": true` or it is silently ignored. |
| Starting charge, arrival reserve toggle | ✅ | ❌ | `soc_start`, `reserve_for_backup` (ADR-023). |
| The garage: multiple bikes | ✅ | ❌ | Catalogue from `GET /v1/vehicles`, hand-edited numbers, adapters kept per-rider not per-bike (ADR-020, ADR-025). Local storage. |
| **Find a charger** (low battery) | ✅ | ❌ | `POST /v1/reach` (ADR-034). Sorted by share of pack needed, not distance. Charge slider is the rider's own reading; everything else follows from it. |
| Minimum-energy route to a charger | ✅ | ❌ | `style: "efficient"`. Comes back as an ordinary plan, so it reuses the ride screen. |
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
| Tablet / large-screen layout | ✅ (iPad) | ❌ | iOS uses `NavigationSplitView`. |
| Desktop | ✅ (Mac Catalyst) | ⛔ | No Android desktop target planned. |

### Server (shared — no per-platform work)

| Capability | Where | Notes |
|---|---|---|
| Curvy routing, loop scoring, out-and-back | `serpentine-api` + GraphHopper on axiom | ADR-009/010/015. Whole-US graph. |
| Energy model, charge stops, reach | `serpentine-api` | ADR-008/014/025/034. **Never validated against a real bike** — see Open question #3. |
| Charger data (NREL daily bulk, Open Charge Map) | `serpentine-api` | Keys live only on axiom and must never reach either client. |
| Rate limits | `serpentine-api` | 24 in flight, 20/min per caller, 429 + `Retry-After`. **Both clients must handle 429 gracefully** and respect `Retry-After`. |
| Health + ops dashboard | axiom, Pi watch | ADR-037. LAN-only. |

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

---

## Open questions

1. **Resolved 2026-10-05 — Google Maps + Places on Android.** See [Resolved](#resolved). The number
   is kept so references to "open question 2" and onward still point at the right thing.
2. **❓ What is the Android voice-navigation handoff?** The iOS answer (Apple Maps unified URL, ~10
   waypoints) is load-bearing — it is the whole "voice nav" story and it is why the minimum is iOS
   18.4. Android needs an equivalent that preserves the *route we chose* rather than letting the nav
   app pick its own. Candidates: Google Maps `dir` URL (waypoint limits unclear), OsmAnd intents,
   Calimoto/Kurviger handoff, or a GPX export workflow. **Research first, design second.**
3. **❓ Does Android wait for energy-model calibration?** The model has never been checked against a
   real bike; the first real ride is pending (bike due early-to-mid Oct 2026). Reach and charge
   planning are the features where being optimistic strands someone. Android could build everything
   else first and gate the battery features, or ship in step once calibration lands.
4. **❓ Distribution bar.** mapbook-android uses the Play internal testing track as the ✅ bar,
   mirroring TestFlight. Assume the same here unless Paul says otherwise. His Play Console account is
   already set up (see `/projects/mapbook-android.md` in Memento).
5. **❓ Gas bikes and the app name.** An open product question on the Apple side that Android inherits:
   both current testers ride petrol bikes, the listing is "Serpentine EV", and the server refuses any
   vehicle that isn't electric. If that changes, it changes both clients.

### Resolved

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
