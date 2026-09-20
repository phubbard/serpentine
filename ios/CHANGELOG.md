# Changelog

User-facing notes; `make testflight-notes` copies the `## [X.Y.Z]` section for the current
`MARKETING_VERSION` into TestFlight's What to Test (markdown is stripped). Write bullets for riders.

## [Unreleased]

## [0.1.0]

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
