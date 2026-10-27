import Foundation

/// The demo-ride catalogue (ADR-043): roads worth knowing, fetched once and planned in one tap.
///
/// It exists for the first screen. Before a rider grants location or types anything they are
/// looking at an empty form and a disabled button, which is a poor introduction to something whose
/// pitch is that it knows the good roads. This is the answer to "show me".
@MainActor
@Observable
final class DemoRides {
    private(set) var rides: [DemoRide] = []
    private(set) var failed = false

    private let api: SerpentineAPI

    init(api: SerpentineAPI = .production) {
        self.api = api
    }

    /// Fetched once per launch. A failure is silent in the UI — the catalogue is a convenience and
    /// an error about it would be the first thing a new rider ever saw us say.
    func load() async {
        guard rides.isEmpty, !failed else { return }
        do {
            rides = try await api.demoRides().filter { Self.canPlan($0) }
        } catch {
            failed = true
        }
    }

    /// The graph is US-only today. A build that outlives that must not offer a ride it cannot plan,
    /// so the country is checked here rather than trusted from the server.
    nonisolated static func canPlan(_ ride: DemoRide) -> Bool {
        guard ride.country == "US" else { return false }
        switch ride.mode {
        case .pointToPoint: return ride.end != nil
        case .outAndBack: return ride.turnaround != nil
        case .loop: return ride.distanceM != nil
        }
    }

    /// The request that rides this road. Charging and the chosen bike come from the rider's own
    /// settings rather than being forced on, so a demo shows the app as they have it set up.
    nonisolated static func request(for ride: DemoRide, twistiness: Double,
                                    charging: ChargingOptions?, vehicle: VehicleWire?) -> PlanRequest {
        PlanRequest(
            mode: ride.mode,
            start: ride.start,
            via: nil,
            end: ride.end,
            turnaround: ride.turnaround,
            distanceM: ride.distanceM,
            durationS: nil,
            maxExtraS: nil,
            // A famous road is the point; "curvy" is right and a detour budget is not, because the
            // road is already the answer and wandering off it would miss it.
            style: ride.mode == .pointToPoint ? "curvy" : nil,
            headingDeg: nil,
            seed: nil,
            twistiness: twistiness,
            charging: charging,
            vehicle: vehicle
        )
    }
}
