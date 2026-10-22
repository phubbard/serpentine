import Foundation

/// What goes into a shared GPX file. These are the server's `?for=` vocabulary, not a choice we put
/// in front of a rider any more (ADR-041).
///
/// ADR-039 shipped a three-way picker because the Beeline Moto II was believed to pick its import
/// mode from a file's contents, in the order track → route → waypoints, so one file could not serve
/// two intents. Hardware disproved that on 2026-10-21: the device *asks*, offering Turn-by-turn,
/// Breadcrumb and Waypoints only, and it builds that menu from what the file holds. A file with a
/// track and the stops offers all three.
///
/// What the file does decide is how much of our ride survives, and that turned out to be brutal.
/// Measured on one 44 mi curvy loop, then a 96.8 mi one:
///
///	waypoints  3 points      → 28.0 mi, 84 % highways. A different ride that touches the charger.
///	route      ~12 points    → 41.7 mi, 31 % highways. Recognisable, quietly altered.
///	all        ~2,000 points → 96.7 mi against 96.8, no highway warning at all. The ride.
///
/// So the app shares `all` and nothing else. The others stay here because they are a published part
/// of the API that other clients may still ask for.
enum GPXKind: String, Sendable {
    /// Charge stops and the two ends of the ride, no path. Kept for API compatibility.
    case waypoints
    /// A sparse corridor plus the stops. Superseded by `all` in every measurement.
    case route
    /// Every point of the route, and nothing else. The historical default for a bare `.gpx`.
    case track
    /// The track *and* the charge stops: one file, every import mode, the ride intact — and the stops
    /// arrive as waypoints the device counts and navigates to.
    case all
}
