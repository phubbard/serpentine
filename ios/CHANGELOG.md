# Changelog

User-facing notes; `make testflight-notes` copies the `## [X.Y.Z]` section for the current
`MARKETING_VERSION` into TestFlight's What to Test (markdown is stripped). Write bullets for riders.

## [Unreleased]

## [0.1.0]

- Share GPX now asks what the file should mean, because a Beeline or a GPS decides what to do from
  what's in the file. **Charge stops** hands the device your stops and lets it navigate between them —
  the right answer for a Beeline, and each stop carries its name, its wait and what you arrive with.
  **Route outline** pins more of the roads we picked. **Exact path** is what you got before: the whole
  route, no turn prompts, and what Kurviger or a Garmin wants. "Which one do I want?" explains it on
  the spot. Two things it warns about: the device's arrival time leaves charging out, so it reads
  earlier than ours, and if you leave the route the device re-plans and the charge plan is gone —
  pause the ride here and carry on for a fresh one.

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

- Search results show each match's address and straight-line distance, so two shops with the same
  name are no longer a coin toss.

- An About panel: what the app is, where your data goes, and credit to OpenStreetMap, the Department
  of Energy and Open Charge Map. It's at the bottom of the plan screen, or the Serpentine menu on a
  Mac. It shows when this build was made and which commit it came from — please paste that line into
  any bug report.

- "Just get me there": in Go somewhere, take the fastest way including freeways, with charge stops
  still planned. For the helmet shop and the dealer, not the Sunday ride.

- A garage: keep more than one bike, pick from a catalogue of Zero, LiveWire and Can-Am models, or
  type your own numbers when the spec sheet doesn't match what you ride. Charge stops are planned for
  the bike you picked, at its own charging rate and plugs — including any adapters you carry.

- Charge stops keep enough in the battery to reach another charger, so a dead or busy one isn't a
  rescue. Turn it off under your bike's section if you'd rather ride the longest legs possible.
- Charge stops now say what riders report about them: out of service, trouble charging, or not
  confirmed working for years. A stop reported dead is replaced before you ever see it.
