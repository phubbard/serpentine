import Foundation

/// The three shapes a ride can be exported in (ADR-039), which exist because the Beeline Moto II
/// picks its import mode from what a file contains rather than from anything the rider chooses.
///
/// It reads a file in the order track → route → waypoints, so one file cannot serve two intents: a
/// `<trk>` wins over anything beside it. Hence three files and a picker.
enum GPXKind: String, CaseIterable, Identifiable, Sendable {
    /// Charge stops only. The device routes between them and gives full turn-by-turn.
    case waypoints
    /// A sparse corridor plus the stops. The device map-matches it to its own network, so our road
    /// choices may be quietly altered.
    case route
    /// The exact path, no re-routing — and no turn prompts at all.
    case track

    var id: String { rawValue }

    var title: String {
        switch self {
        case .waypoints: "Charge stops"
        case .route: "Route outline"
        case .track: "Exact path"
        }
    }

    /// What the device will actually do with it — the thing that decides which one a rider wants.
    var effect: String {
        switch self {
        case .waypoints: "Turn-by-turn between your charge stops. Best for a Beeline."
        case .route: "Turn-by-turn, but the device may pick its own roads between points."
        case .track: "Follows the exact route. No turn prompts at all."
        }
    }

    var icon: String {
        switch self {
        case .waypoints: "bolt.fill"
        case .route: "arrow.triangle.turn.up.right.diamond"
        case .track: "point.topleft.down.to.point.bottomright.curvepath"
        }
    }
}
