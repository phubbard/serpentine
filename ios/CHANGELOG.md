# Changelog

User-facing notes; `make testflight-notes` copies the `## [X.Y.Z]` section for the current
`MARKETING_VERSION` into TestFlight's What to Test (markdown is stripped). Write bullets for riders.

## [Unreleased]

## [0.1.0]

- Stopped part-way? Tap pause on a ride and Serpentine remembers the part you haven't ridden. Go and
  charge — or eat, or go home and come back tomorrow — then "Carry on with this ride" appears at the
  top of the plan screen. It picks up from wherever you are then, not from the spot you stopped at,
  so the miles you rode to reach a charger aren't ridden a second time. The rest follows the roads of
  the ride you were on.

  On a loop the start and the finish are the same place, so pausing right next to either leaves
  Serpentine guessing. It shows you how it split the ride — tap "Not right" if it got it wrong.

- Low on battery? Tap the bolt at the top of the plan screen, tell it what charge you have left, and
  Serpentine lists the public chargers it thinks you can still reach — sorted by how much of your
  battery the ride there would take, not by how close they are. Those are different lists: a charger
  over a hill can cost more than one twice as far on the flat. Each one shows what it needs and what
  you'd arrive with; tap it for the route that spends the least getting there. Anything out of reach
  is marked rather than hidden, and if nothing is in range you still get the nearest.

  The estimates lean cautious and come from a charge figure you type in. They have not yet been
  checked against a real ride — please tell me how close they land.

- Keep the rides worth keeping. Tap Save on any ride and it goes to your saved list — the bookmark at
  the top of the plan screen. Give it stars, write down what you found (gravel on a turn, the charger
  that was busy), and record when you last rode it. Rides you haven't ridden yet sit in the same list,
  so it works as a list of roads to try as well as a record of the ones you've done. A saved ride keeps
  the route exactly as planned, opens the same way every time, and works with no signal. Saved rides
  stay on your device and are never uploaded.

- Search results tell you which one you want: each match now shows its address and how far away it is,
  so two shops with the same name are no longer a coin toss. That distance is straight-line, not
  riding distance.

- An About panel worth reading: what the app is, where your data goes, and credit to the people whose
  data makes it work — OpenStreetMap, the Department of Energy and Open Charge Map. On the Mac it's in
  the Serpentine menu; elsewhere it's at the bottom of the plan screen. It also shows when this build
  was made and which commit it came from — please paste that line into any bug report.

- "Just get me there": in Go somewhere, take the fastest way including freeways, with charge stops
  still planned. For the helmet shop and the dealer, not the Sunday ride. Measured Ramona to Orange
  County: 90 miles and 101 minutes, against 123 miles and 173 the scenic way.

- Rides anywhere in the United States, Alaska and Hawaii included — no app update needed, the
  planner already knows. Try somewhere you have never ridden.
- Charge stops are found from a local copy of every public US charger, so planning is quicker and
  doesn't depend on someone else's API being awake.
- A garage: keep more than one bike, pick from a catalogue of Zero, LiveWire and Can-Am models, or
  type your own numbers when the spec sheet doesn't match what you ride. Charge stops are planned for
  the bike you picked, at its own charging rate and plugs — including any adapters you carry.

- Runs on the Mac: the plan form sits in a sidebar beside a full-window map. ⌘R plans another ride.

- Charge stops keep enough in the battery to reach another charger, so a dead or busy one isn't a
  rescue. Turn it off under your bike's section if you'd rather ride the longest legs possible.
- Charge stops now say what riders report about them: out of service, trouble charging, or not
  confirmed working for years. A stop reported dead is replaced before you ever see it.
- Each charge stop shows its nearest backup, so a dead or occupied charger doesn't need a replan.
- Plan by time instead of distance, and a "Go somewhere" mode: the quick way there plus however long
  you'll spend on better roads.

- iPad: the plan form sits beside a large map, in portrait or landscape.
- Plan a loop or an out-and-back from your location or any searched place: pick a distance, how
  twisty, and optionally a direction to head first.
- Routes favour curvy, rural roads with moderate speed limits and avoid dirt.
- Zero SR/S charging: set your starting charge and Serpentine plans stops at J1772 and Tesla
  destination chargers, with arrival charge and time at each stop.
- Navigate in Apple Maps: one tap hands the ride to Apple Maps for voice guidance, with stops that
  keep it on the planned roads.
- Share the ride as GPX for Kurviger, Garmin and friends.
- "Another" gives you a different ride with the same settings.
