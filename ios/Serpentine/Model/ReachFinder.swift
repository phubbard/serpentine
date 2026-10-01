import CoreLocation
import Observation

/// "I'm low — where can I actually get to?" (ADR-034). Separate from `Planner` because it answers a
/// different question with different state: not what ride to take, but what is still within reach.
@MainActor @Observable
final class ReachFinder {
    /// The rider's own charge reading. Nothing on the phone knows it, and on a low battery guessing
    /// it wrong is the difference between arriving and not, so it starts where the plan screen left
    /// it and stays under the rider's thumb.
    var socPercent: Double = 20

    private(set) var result: ReachResult?
    private(set) var isSearching = false
    private(set) var errorMessage: String?

    /// The ride to a chosen charger, once one is tapped.
    private(set) var routing: ReachOption?

    private let api: SerpentineAPI
    private var lastQuery: (coord: CLLocationCoordinate2D, soc: Int)?

    init(api: SerpentineAPI = .production) {
        self.api = api
    }

    var soc: Double { socPercent / 100 }

    /// Looks up what is reachable. Repeats with the same charge from the same spot are skipped —
    /// each call costs the server a routing fan-out.
    func search(from start: CLLocationCoordinate2D, bike: Bike, force: Bool = false) async {
        let rounded = Int(socPercent.rounded())
        if !force, let last = lastQuery, last.soc == rounded,
           abs(last.coord.latitude - start.latitude) < 0.001,
           abs(last.coord.longitude - start.longitude) < 0.001 {
            return
        }
        isSearching = true
        errorMessage = nil
        defer { isSearching = false }
        do {
            result = try await api.reach(ReachRequest(start: start.lonLat, soc: soc, vehicle: bike.wire))
            lastQuery = (start, rounded)
        } catch {
            errorMessage = error.localizedDescription
            lastQuery = nil
        }
    }

    /// What to ask the server for. Pulled out whole because it is the part worth being sure of:
    /// the wrong style gives a fast route instead of a frugal one, and holding back a backup reserve
    /// is what refuses to plan at all for the rider who most needs this.
    nonisolated static func request(to option: ReachOption, from start: CLLocationCoordinate2D,
                                    bike: Bike, soc: Double) -> PlanRequest {
        PlanRequest(
            mode: .pointToPoint,
            start: start.lonLat,
            via: nil,
            end: option.lonlat,
            distanceM: nil, durationS: nil, maxExtraS: nil,
            style: "efficient",
            headingDeg: nil, seed: nil, twistiness: 0,
            charging: ChargingOptions(socStart: soc, reserveForBackup: false),
            vehicle: bike.wire)
    }

    /// The minimum-energy ride to one of them. Comes back as an ordinary plan, so the ride view
    /// draws it with no special cases — map, charge detail, Apple Maps handoff and GPX included.
    func route(to option: ReachOption, from start: CLLocationCoordinate2D, bike: Bike) async -> PlanResult? {
        routing = option
        defer { routing = nil }
        do {
            return try await api.plan(Self.request(to: option, from: start, bike: bike, soc: soc))
        } catch {
            errorMessage = error.localizedDescription
            return nil
        }
    }
}
