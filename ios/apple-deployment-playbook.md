# Apple Deployment Playbook

Portable distillation of MapMyFriends' (`phubbard/mapbook`) release architecture:
local Makefile-driven CI, signing, TestFlight uploads for iOS **and** Mac Catalyst
under one listing, changelog-driven What-to-Test notes, and automatic beta-group
distribution via the App Store Connect API. Written to be imported into a new
project **under the same developer account** (Team `NSR65JVW9F`) with a
**different bundle ID and app record**.

**How to use this doc:** drop it in the new repo, hand it to your agent/session,
and work top to bottom. Sections 1–2 are already done (account-level, shared).
Section 3 is the per-app setup. Sections 4–6 are the files and config to copy.
Section 8 is the accumulated scar tissue — read it before your first release.

**Token table** — replace throughout:

| Token | mapbook value | New app |
|---|---|---|
| `APP_NAME` (product/display, `.app`/`.ipa` filename) | `MapMyFriends` | *yours* |
| `APP_BUNDLE_ID` | `net.phfactor.Mapbook` | *yours — permanent, see §8.1* |
| `SCHEME` (Xcode scheme / internal target name) | `Mapbook` | *yours* |
| `TEAM_ID` | `NSR65JVW9F` | same |
| Local config file | `Mapbook.local.xcconfig` | `<YourApp>.local.xcconfig` |

---

## 1. Architecture overview

No cloud CI. Everything runs on the dev Mac, deterministic and scriptable:

```
project.yml ──xcodegen──▶ .xcodeproj (generated, gitignored)
     │
Makefile release targets
     ├── verify-testflight        # preflight: key id, issuer, .p8 present
     ├── archive-testflight       # xcodebuild archive (generic/platform=iOS)
     ├── upload-testflight        # exportArchive (.ipa) → xcrun altool --type ios
     ├── archive-testflight-mac   # xcodebuild archive (Catalyst)
     ├── upload-testflight-mac    # exportArchive (.pkg) → xcrun altool --type osx
     └── testflight-notes         # ruby tools/set-testflight-notes.rb:
                                  #   CHANGELOG section → What-to-Test on BOTH builds,
                                  #   then attach builds to beta group(s) +
                                  #   submit external groups for Beta App Review
```

Auth everywhere is one **App Store Connect API key** (no Apple ID passwords, no
app-specific passwords, headless-safe). Version = `MARKETING_VERSION` in
`project.yml`; build number = `git rev-list --count HEAD` (monotonic, no state
file). One ASC listing serves both platforms: TestFlight installs the `.ipa` on
iPhone/iPad and the `.pkg` on Mac automatically.

Why ship a separate Catalyst binary at all: the iOS build running on a Mac
("Designed for iPad" compat layer) has a known TCC bug where
`CNContactStore.requestAccess` silently no-ops. Native Catalyst doesn't. If your
new app touches Contacts/Calendar/etc. on Mac, ship the Catalyst variant.

## 2. Account-level prerequisites (SHARED — already done, verify only)

- **Apple Developer Program** membership, Team `NSR65JVW9F`.
- **ASC API key with the App Manager role** at
  `~/.appstoreconnect/private_keys/AuthKey_<KEY_ID>.p8`.
  ⚠️ Role matters: a **Developer**-role key can upload builds and set notes but
  gets `403 FORBIDDEN` on beta-group management and review submission — which
  silently kills auto-distribution. Roles are fixed at key creation; if you only
  have a Developer key, mint a new App Manager one (ASC → Users and Access →
  Integrations). Current working key: `NLZ9N5G82D` (issuer
  `69a6de8c-1416-47e3-e053-5b8c7c11a4d1`).
- Distribution certificates (iOS Distribution + Mac App Distribution) in the
  login keychain — `signingStyle: automatic` + `-allowProvisioningUpdates`
  handles profiles; you rarely think about these again.
- *(Optional Mac sideload channel only)* Developer ID Application cert +
  a notarytool keychain profile (`xcrun notarytool store-credentials`).

## 3. Per-app one-time setup (NEW for each app)

1. **Register the bundle ID** (developer.apple.com → Identifiers) and create the
   **app record** in ASC (My Apps → +). One record; both iOS and Mac Catalyst
   builds upload under it. Note the app name must be globally unique on the App
   Store — check before falling in love (MapMyFriends exists because "Mapbook"
   was taken; see §8.1 for why we couldn't just rename everything).
2. **Info.plist keys that pre-answer ASC's questions** (do this before first
   upload; saves a manual prompt per build):
   - `ITSAppUsesNonExemptEncryption` = `false`
   - `LSApplicationCategoryType` = e.g. `public.app-category.productivity`
3. **`PrivacyInfo.xcprivacy`** declaring required-reason API usage (UserDefaults
   etc.). Copy mapbook's as a starting point; trim to the APIs you use.
4. **App Privacy label** (ASC → App Privacy): fill before external testing.
   If the app is local-only with no third-party calls, "Data Not Collected" —
   and defend that posture in code review (it's a feature).
5. **Privacy policy URL** — required for external TestFlight. (mapbook uses
   `https://www.phfactor.net/privacy.html`.)
6. **Beta groups** (ASC → TestFlight):
   - Internal group: toggle **"Automatic distribution"** once — internal testers
     then get every build forever with zero API calls.
   - External group (e.g. "Public beta"): create it, then let the tooling
     auto-attach builds (§5). First build of each *marketing version* needs Beta
     App Review (~24 h); later builds of the same version clear instantly.
7. **Local config** — create `<YourApp>.local.xcconfig` (gitignored):
   ```
   DEVELOPMENT_TEAM = NSR65JVW9F
   APP_STORE_KEY_ID = NLZ9N5G82D
   APP_STORE_ISSUER_ID = 69a6de8c-1416-47e3-e053-5b8c7c11a4d1
   // Comma-separated beta groups auto-distributed on `make testflight-notes`.
   TESTFLIGHT_GROUPS = Public beta
   ```
   ⚠️ xcconfig comments are `//`, **not** `#`. A stray `#` line breaks every
   build with an inscrutable error (learned the hard way).

## 4. Files to copy from `phubbard/mapbook`

| File | Portability |
|---|---|
| `tools/set-testflight-notes.rb` | **Copy verbatim.** Pure Ruby stdlib (hand-rolled ES256 JWT — no gems). Fully parameterized by flags; nothing app-specific inside. Resolves the app by `--bundle-id`, so no hardcoded app id. |
| `tools/ExportOptions-TestFlight.plist` | Copy; it only contains `method: app-store-connect`, `signingStyle: automatic`, `teamID`, `uploadSymbols: true`. Same team → likely zero edits. |
| `tools/ExportOptions-TestFlight-Mac.plist` | Same (Catalyst sibling; identical content, different archive feeds it). |
| `Makefile` | Copy the variable block + these targets: `verify-testflight`, `archive-testflight`, `upload-testflight`, `archive-testflight-mac`, `upload-testflight-mac`, `testflight-notes`, `testflight-groups`, `clean`, `version`. Update the §0 token table values at the top. Skip the `.dmg`/notarize/publish-github targets unless you want the sideload channel (§7). |
| `CHANGELOG.md` | Copy the header + `## [Unreleased]` convention. The notes script extracts the `## [X.Y.Z]` section verbatim and de-markdowns it (bold/backticks/links stripped, bullets → •, 4000-char cap). Write bullets user-facing from day one. |
| `testflight-setup.md` | Copy as reference doc; it's the long-form version of §2–3. |

### What the notes script does (so you trust it)

`tools/set-testflight-notes.rb --key-id K --issuer I --bundle-id B --version V
--build N --min-builds 2 --groups "Public beta" --changelog CHANGELOG.md`:

1. Mints an ES256 JWT from the `.p8` (20-min expiry, re-minted after polling).
2. Finds the app by bundle id, then **polls** (30 s interval, 25 min timeout)
   until `min-builds` builds with that version+build number register — the Mac
   `.pkg` processes slower than the `.ipa`, hence `MIN_BUILDS ?= 2` when
   shipping both. Pass `MIN_BUILDS=1` for a single-platform release.
3. PATCHes (or POSTs) the `en-US` `betaBuildLocalization.whatsNew` on every
   matching build with the de-markdowned changelog section.
4. For each `--groups` name (case-insensitive match, `--list-groups` to
   enumerate): `POST /v1/builds/{id}/relationships/betaGroups` (204 No
   Content on success), and for external groups
   `POST /v1/betaAppReviewSubmissions` — a 409 there means "already
   submitted/approved" and is treated as fine, so re-runs are idempotent.
5. Distribution failures **warn but don't fail the make** — notes are the
   critical path; a mis-permissioned key degrades gracefully with a printed fix.

## 5. Project configuration to replicate

- **xcodegen** (`project.yml` → generated, gitignored `.xcodeproj`). Key settings
  that took iteration:
  - `GENERATE_INFOPLIST_FILE: NO` + a maintained `Info.plist` — Apple's
    `INFOPLIST_KEY_*` auto-injection drops custom keys silently.
  - `ENABLE_USER_SCRIPT_SANDBOXING: NO` — the build-number pre-action reads
    `.git/`, which the script sandbox denies (too many files to enumerate as
    declared inputs).
  - `ENABLE_HARDENED_RUNTIME: YES` — required for notarization on the Mac
    sideload channel; harmless for App Store paths.
  - `SUPPORTS_MACCATALYST: YES`, `DERIVE_MACCATALYST_PRODUCT_BUNDLE_IDENTIFIER: NO`.
- **The xcconfig dance**: committed `<App>.xcconfig` does
  `#include? "<App>.local.xcconfig"` (gitignored; holds team + ASC key ids +
  TESTFLIGHT_GROUPS). Survives `xcodegen generate` wiping Xcode-UI signing
  settings. The Makefile awk-parses the local file so make and Xcode share one
  source of truth.
- **Build number from git** — scheme pre-action writes
  `CURRENT_PROJECT_VERSION = $(git rev-list --count HEAD)` to a gitignored
  `Build.gen.xcconfig` (bumped +1 when the tree is dirty so dev builds preview
  the next number). Monotonic across the repo's life; never collides as long as
  you don't upload from two divergent branches at the same count.

## 6. The per-release ritual

```bash
# 1. Promote CHANGELOG [Unreleased] → [X.Y.Z] – date; bump MARKETING_VERSION
#    in project.yml; update README status line. Commit (via your PR flow).
# 2. From the release commit:
make upload-testflight        # ~3-5 min: archive + export + altool
make upload-testflight-mac    # ~3-5 min: run AFTER iOS completes (§8.2)
make testflight-notes         # polls until both builds register (~1-3 min),
                              # sets notes, distributes to TESTFLIGHT_GROUPS,
                              # submits beta review. Zero clicks.
```

Timings from real runs: uploads return in seconds after export; ASC processing
10–30 min; both builds usually registered within 1–2 poll cycles. The whole
ritual is ~10 minutes of wall clock, zero ASC web-UI visits.

`make testflight-groups` lists the app's beta groups (name, internal/external,
auto-distribution flag) — use it to discover the exact strings
`TESTFLIGHT_GROUPS` expects.

## 7. Optional second channel: Mac Developer ID `.dmg` (sideload)

mapbook also ships a notarized `.dmg` outside the App Store (targets `archive` →
`sign-release` → `dmg` → `notarize-dmg` → `publish-github`). Copy only if you
need it. The two non-obvious pieces worth stealing:

- **Team-ID-pinned designated requirement** at signing time — future signed
  updates then inherit TCC permission grants (Contacts/Calendar/Location), so
  testers aren't re-prompted every update. Without this, each update looks like
  a new app to TCC.
- **Public releases repo** (`<app>-releases`) separate from the private source
  repo: testers download the `.dmg` without GitHub auth; the Makefile tags the
  source repo but uploads artifacts + `SHA256SUMS` to the public one. An
  in-app `UpdateChecker` polls that repo's `releases/latest`.

## 8. Scar tissue — read before your first release

1. **The bundle ID is forever.** It's the App Store identity AND the TCC anchor:
   change it after first install and users lose Contacts/Calendar/Location
   grants (and TestFlight continuity). Display name, product name, repo name
   can all change; `PRODUCT_BUNDLE_IDENTIFIER` cannot. Pick it like a tattoo.
2. **Never run the two uploads in parallel.** They share DerivedData/build dirs;
   a parallel run corrupted an export with a disk I/O error. Serial only.
3. **`VERSION` is parsed from `project.yml` on the current branch.** Running
   `make testflight-notes` from a branch that hasn't got the version bump polls
   for a build that will never exist (25-minute hang, then failure). Ship from
   the release branch/commit.
4. **App Manager role or no distribution** (§2). The 403 says "the API key in
   use does not allow this request" — that's role, not a bug.
5. **First build of each marketing version → Beta App Review** for external
   groups (~24 h, historically approved same-day). Subsequent builds of the
   same version are instant. Internal groups never wait.
6. **xcconfig comments are `//`.** A `#` comment breaks every build.
7. **iOS Simulator availability is irrelevant to this pipeline** — archives use
   `generic/platform=iOS` and Catalyst. When a macOS/Xcode update version-skews
   CoreSimulator (happens!), releases still ship; use the Catalyst destination
   as the compile-check.
8. **altool still works fine** with API-key auth for both `--type ios` and
   `--type osx`. If Apple finally removes it, the drop-in replacement is
   `xcrun notarytool`-era `xcrun altool` → `xcrun appstoreconnect` /
   Transporter; the Makefile isolates the invocation to two lines per target.
9. **TestFlight notes render plain text** — the script de-markdowns, but write
   changelog bullets knowing `**bold**` and links will be stripped.
10. **Catalyst + Contacts on Mac requires the real Catalyst build** (§1). Don't
    let "iOS app runs on Apple Silicon anyway" tempt you into skipping the
    second upload if you use TCC-gated frameworks.
11. **App Privacy label ≠ set-and-forget.** Adding/removing a third-party API
    (we removed Yelp) changes your label; there's no API for it — calendar a
    manual ASC visit as part of any privacy-affecting release.

---

*Source of truth: `phubbard/mapbook` @ v0.6.1 (2026-07-02) — Makefile,
`tools/set-testflight-notes.rb`, `tools/ExportOptions-TestFlight*.plist`,
`testflight-setup.md`. This doc is the map, those files are the territory.*
