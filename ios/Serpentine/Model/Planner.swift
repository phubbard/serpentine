import CoreLocation
import MapKit
import Observation

/// Where a ride starts: the rider's location or a searched place.
struct StartPoint: Equatable, Sendable {
    var name: String
    var coordinate: CLLocationCoordinate2D

    static func == (a: StartPoint, b: StartPoint) -> Bool {
        a.name == b.name && a.coordinate.latitude == b.coordinate.latitude && a.coordinate.longitude == b.coordinate.longitude
    }
}

/// How the rider says how big a ride they want: a distance, or the time they have.
enum RideBudget: String, CaseIterable, Identifiable, Sendable {
    case distance, time
    var id: String { rawValue }
    var label: String { self == .distance ? "Distance" : "Time" }
}

/// Compass directions offered for "head this way first"; nil = let the server try all eight.
enum Heading: Double, CaseIterable, Identifiable {
    case north = 0, northeast = 45, east = 90, southeast = 135, south = 180, southwest = 225, west = 270, northwest = 315
    var id: Double { rawValue }
    var label: String {
        switch self {
        case .north: "North"
        case .northeast: "Northeast"
        case .east: "East"
        case .southeast: "Southeast"
        case .south: "South"
        case .southwest: "Southwest"
        case .west: "West"
        case .northwest: "Northwest"
        }
    }
}

/// Plan-screen state and the call to the API.
@MainActor @Observable
final class Planner {
    var mode: RideMode = .loop
    var budget: RideBudget = .distance
    var distanceKm: Double = 150
    var durationMin: Double = 120
    var twistiness: Double = 0.5
    var heading: Heading?
    var charging = false
    var socPercent: Double = 100
    var reserveForBackup = true
    var start: StartPoint?
    var destination: StartPoint?
    var maxExtraMin: Double = 15

    private(set) var isPlanning = false
    private(set) var errorMessage: String?
    var result: PlanResult?
    private var seed = 1

    private let api: SerpentineAPI

    init(api: SerpentineAPI = .production) {
        self.api = api
    }

    /// Everything a plan needs is chosen: a start, and for "go somewhere" a destination too.
    var canPlan: Bool { start != nil && (mode != .pointToPoint || destination != nil) }

    func request(seed: Int) -> PlanRequest? {
        guard let start, canPlan else { return nil }
        let goingSomewhere = mode == .pointToPoint
        return PlanRequest(
            mode: mode,
            start: start.coordinate.lonLat,
            end: goingSomewhere ? destination?.coordinate.lonLat : nil,
            distanceM: goingSomewhere || budget == .time ? nil : distanceKm * 1000,
            durationS: goingSomewhere || budget == .distance ? nil : durationMin * 60,
            maxExtraS: goingSomewhere ? maxExtraMin * 60 : nil,
            headingDeg: goingSomewhere ? nil : heading?.rawValue,
            seed: goingSomewhere ? nil : seed,
            twistiness: twistiness,
            charging: charging ? ChargingOptions(socStart: socPercent / 100,
                                                reserveForBackup: reserveForBackup) : nil
        )
    }

    /// Plans a new ride; `another` asks for a different one with the same settings. Each server
    /// plan already tries seeds n and n+1, so "another" steps by two.
    func plan(another: Bool = false) async {
        seed = another ? seed + 2 : 1
        guard let req = request(seed: seed) else {
            errorMessage = "Choose a start point first."
            return
        }
        isPlanning = true
        errorMessage = nil
        defer { isPlanning = false }
        do {
            result = try await api.plan(req)
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    func gpxFile(for plan: PlanResult) async -> URL? {
        try? await api.downloadGPX(for: plan)
    }

    /// Place search through MapKit (Apple's service, like the map itself), biased to near `near`.
    static func search(_ query: String, near: CLLocationCoordinate2D?) async -> [StartPoint] {
        let req = MKLocalSearch.Request()
        req.naturalLanguageQuery = query
        req.resultTypes = [.address, .pointOfInterest]
        let center = near ?? CLLocationCoordinate2D(latitude: 33.0, longitude: -116.9) // San Diego County
        req.region = MKCoordinateRegion(center: center, latitudinalMeters: 300_000, longitudinalMeters: 300_000)
        guard let resp = try? await MKLocalSearch(request: req).start() else { return [] }
        return resp.mapItems.prefix(12).map { item in
            StartPoint(name: item.name ?? "Unnamed place", coordinate: item.placemark.coordinate)
        }
    }
}
