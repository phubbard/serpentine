import CoreLocation
import MapKit
import Observation

/// Where a ride starts: the rider's location or a searched place.
struct StartPoint: Equatable, Sendable, Identifiable {
    var name: String
    var coordinate: CLLocationCoordinate2D
    /// Street, town and state as MapKit knows them. A search for a chain — or for "Speed Addicts" —
    /// returns several hits with the same name, and the name alone doesn't say which one is which.
    var address: String?

    /// Search results aren't unique by name, so identify them by where they are.
    var id: String { "\(name)|\(coordinate.latitude),\(coordinate.longitude)" }

    /// Straight-line metres to another point. Not the riding distance — that would cost a route per
    /// result — but enough to tell the shop across town from the one three states away.
    func metres(from other: CLLocationCoordinate2D) -> CLLocationDistance {
        CLLocation(latitude: coordinate.latitude, longitude: coordinate.longitude)
            .distance(from: CLLocation(latitude: other.latitude, longitude: other.longitude))
    }

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
    /// The bike being ridden, set from the garage. Only sent when charging is on: without it the
    /// server has nothing to model and it would only split the route cache (ADR-025).
    var bike: Bike = .srs
    var start: StartPoint?
    var destination: StartPoint?
    var maxExtraMin: Double = 15
    /// "Go somewhere" comes in two flavours: the ride, and the errand (ADR-031). An errand takes
    /// freeways and asks Apple Maps not to avoid them.
    var directRoute = false

    private(set) var isPlanning = false
    /// True when the ride on screen is the rest of one that was put down earlier (ADR-035). Only
    /// the title depends on it — "The way there" is true of a resumed loop but reads as a new trip.
    private(set) var isResumedRide = false
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
            via: nil,
            end: goingSomewhere ? destination?.coordinate.lonLat : nil,
            distanceM: goingSomewhere || budget == .time ? nil : distanceKm * 1000,
            durationS: goingSomewhere || budget == .distance ? nil : durationMin * 60,
            maxExtraS: goingSomewhere && !directRoute ? maxExtraMin * 60 : nil,
            style: goingSomewhere ? (directRoute ? "direct" : "curvy") : nil,
            headingDeg: goingSomewhere ? nil : heading?.rawValue,
            seed: goingSomewhere ? nil : seed,
            twistiness: twistiness,
            charging: charging ? ChargingOptions(socStart: socPercent / 100,
                                                reserveForBackup: reserveForBackup) : nil,
            vehicle: charging ? bike.wire : nil
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
            isResumedRide = false
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    /// Runs a request built elsewhere — carrying on with a paused ride (ADR-035) is planned from
    /// the rest of its route, not from the plan screen's settings.
    func run(_ request: PlanRequest) async {
        isPlanning = true
        errorMessage = nil
        defer { isPlanning = false }
        do {
            result = try await api.plan(request)
            // Set with the result, not before it, so the label can never describe a different ride.
            isResumedRide = !(request.via ?? []).isEmpty
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    /// Surfaces a problem raised outside the planner itself, so one error line serves the screen.
    func setError(_ message: String) { errorMessage = message }

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
            let name = item.name ?? "Unnamed place"
            return StartPoint(name: name, coordinate: item.placemark.coordinate,
                              address: addressLine(item.placemark, name: name))
        }
    }

    /// One line of address from a placemark: "1234 Main St, Poway, CA". MapKit names a plain address
    /// result after its own street, so the street is dropped when it would only repeat the title.
    private static func addressLine(_ place: MKPlacemark, name: String) -> String? {
        let street = [place.subThoroughfare, place.thoroughfare].compactMap { $0 }.joined(separator: " ")
        var parts = [street, place.locality, place.administrativeArea].compactMap { $0 }.filter { !$0.isEmpty }
        if let first = parts.first, first == name || name.hasPrefix(first) {
            parts.removeFirst()
        }
        let line = parts.joined(separator: ", ")
        return line.isEmpty ? nil : line
    }
}
