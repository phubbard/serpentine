import CoreLocation
import Foundation

// Wire types for serpentine-api (docs/API.md is the contract; change both together). The JSON is
// snake_case; the coders convert, so property names here are the camelCase of the wire names.
// The result types are Codable rather than Decodable because a saved ride is the server's answer
// written back out verbatim (ADR-033) — it round-trips through the same snake_case coder pair.
// Coordinates on the wire are [lon, lat].

enum RideMode: String, Codable, CaseIterable, Identifiable, Sendable {
    case loop
    case outAndBack = "out_and_back"
    case pointToPoint = "point_to_point"

    var id: String { rawValue }
}

struct PlanRequest: Encodable, Sendable {
    var mode: RideMode
    var start: [Double]
    /// Points the route must pass through, in order (ADR-035). Carrying on with a paused ride is
    /// what this is for: the rest of the original route, sampled.
    var via: [[Double]]?
    var end: [Double]?
    var distanceM: Double?
    var durationS: Double?
    var maxExtraS: Double?
    var style: String?
    var headingDeg: Double?
    var seed: Int?
    var twistiness: Double
    var charging: ChargingOptions?
    var vehicle: VehicleWire?
}

/// The bike, as the server wants it (ADR-025). Wh/km rather than a range, because manufacturers
/// quote ranges at speeds they no longer publish.
struct VehicleWire: Encodable, Sendable {
    var name: String
    var kind: String
    var usableKwh: Double
    var cityWhPerKm: Double
    var highwayWhPerKm: Double
    var acKw: Double
    var dcKw: Double
    var connectors: [String]
}

/// An entry in the catalogue at /v1/vehicles. Nearly everything is optional: manufacturers don't
/// publish the same fields, and the catalogue says so rather than inventing them.
struct CatalogVehicle: Decodable, Sendable, Identifiable {
    let id: String
    let make: String
    let model: String
    let year: Int?
    let kind: String
    let batteryMaxKwh: Double?
    let batteryNominalKwh: Double?
    let rangeCityMi: Double?
    let rangeHighwayMi: Double?
    let rangeHighwayLowMi: Double?
    let acKw: Double?
    let dcKw: Double?
    let connectors: [String]?
    let confidence: String?
    let notes: String?
}

struct CatalogResponse: Decodable, Sendable {
    let version: String
    let vehicles: [CatalogVehicle]
}

struct ChargingOptions: Encodable, Sendable {
    var enabled = true
    var socStart: Double
    var reserveForBackup: Bool
}

struct PlanResult: Codable, Sendable, Identifiable, Hashable {
    let id: String
    let mode: String
    /// "curvy", "direct" or "efficient" on a point-to-point ride; absent on loops and out-and-backs.
    let style: String?
    let distanceM: Double
    let timeS: Double
    let ascendM: Double
    let polyline: [[Double]]
    let roads: [Road]
    let instructions: [Instruction]
    let stats: Stats
    let loop: LoopInfo?
    let budget: Budget?
    let detour: Detour?
    let outAndBack: OutAndBackInfo?
    let energy: Energy?
    let chargers: [Charger]?
    let handoff: Handoff
    let gpxUrl: String

    static func == (a: PlanResult, b: PlanResult) -> Bool { a.id == b.id }
    func hash(into h: inout Hasher) { h.combine(id) }
}

/// Present when the ride was planned against a time budget: what was asked for, what it costs
/// (charging included) and whether it fits.
struct Budget: Codable, Sendable {
    let targetS: Double
    let totalS: Double
    let fits: Bool
}

/// Present on "go somewhere" plans: what the better roads cost over the quickest way.
struct Detour: Codable, Sendable {
    let fastestS: Double
    let extraS: Double
    let maxExtraS: Double
    let twistiness: Double
    let fits: Bool
}

struct Road: Codable, Sendable {
    let name: String
    let km: Double
}

struct Instruction: Codable, Sendable {
    let text: String
    let distanceM: Double
    let timeS: Double
    let sign: Int
    let i: Int
}

struct Stats: Codable, Sendable {
    let km: Double
    let curvyKm: Double
    let repeatedKm: Double?
    let roadClassKm: [String: Double]
    let urbanDensityKm: [String: Double]
    let surfaceKm: [String: Double]
}

struct LoopInfo: Codable, Sendable {
    let targetM: Double
    let headingDeg: Double
    let seed: Int
    let score: Double
    let candidates: Int
    let failed: Int
}

struct OutAndBackInfo: Codable, Sendable {
    let turnaround: [Double]
    let headingDeg: Double
    let outKm: Double
    let backKm: Double
    let sharedKm: Double
}

struct Energy: Codable, Sendable {
    let usableKwh: Double
    let kwhEst: Double
    let socStart: Double
    let socEndEst: Double
    let feasible: Bool
    let stops: Int
    let chargeMin: Double
    let chargersNearby: Int?
    let totalTimeS: Double
    let warning: String?
}

struct Charger: Codable, Sendable, Identifiable {
    let id: String
    let name: String
    let lonlat: [Double]
    let address: String
    let network: String
    let ports: Int
    let powerKw: Double
    let connectors: [String]
    let hours: String?
    let kmFromStart: Double
    let offRouteKm: Double
    let socArrivalEst: Double
    let stop: Bool
    let role: String?
    let dwellMin: Double?
    let reliability: Reliability?
}

/// What Open Charge Map knows about a charger: absent means it has no listing there, which is common
/// in the back country and is not the same as "it's fine".
struct Reliability: Codable, Sendable {
    let operational: Bool
    let status: String?
    let lastConfirmed: String?
    let stale: Bool?
    let recentFailures: Int?
    let note: String?
}

struct Handoff: Codable, Sendable {
    let appleMapsUrl: String
    let googleMapsUrl: String
    let source: [Double]
    let destination: [Double]
    let waypoints: [[Double]]
    let waypointRoads: [String]
}

extension Array where Element == Double {
    /// A wire [lon, lat] (optionally with elevation) as a coordinate.
    var coordinate: CLLocationCoordinate2D { CLLocationCoordinate2D(latitude: self[1], longitude: self[0]) }
}

extension CLLocationCoordinate2D {
    /// This coordinate as a wire [lon, lat].
    var lonLat: [Double] { [longitude, latitude] }
}

// MARK: - Reach: what can I still get to (ADR-034)

/// Asks the server which chargers are within what's left in the pack, priced in battery rather than
/// miles. `soc` is the rider's own reading — the one number the app can't infer.
struct ReachRequest: Encodable, Sendable {
    var start: [Double]
    var soc: Double
    var vehicle: VehicleWire?
}

struct ReachResult: Decodable, Sendable {
    let socStart: Double
    let usableKwh: Double
    /// Best case: the whole remaining pack at city consumption, on the flat. Nothing further away
    /// than this is reachable, so it is also the search radius.
    let rangeKmEst: Double
    let nearby: Int
    let options: [ReachOption]
    let warning: String?
}

struct ReachOption: Decodable, Sendable, Identifiable, Hashable {
    let id: String
    let name: String
    let lonlat: [Double]
    let address: String
    let network: String
    let ports: Int
    let powerKw: Double
    let connectors: [String]
    let hours: String?
    let straightKm: Double
    let distanceM: Double
    let timeS: Double
    let ascendM: Double
    let kwhEst: Double
    /// Fraction of the whole pack this ride would take. The list's sort key, and the number the
    /// rider is actually deciding on.
    let socNeeded: Double
    let socArrivalEst: Double
    let reachable: Bool
    let chargeMin: Double?
    let reliability: Reliability?

    static func == (a: ReachOption, b: ReachOption) -> Bool { a.id == b.id }
    func hash(into h: inout Hasher) { h.combine(id) }
}
