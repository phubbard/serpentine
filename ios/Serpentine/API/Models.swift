import CoreLocation
import Foundation

// Wire types for serpentine-api (docs/API.md is the contract; change both together). The JSON is
// snake_case; the coders convert, so property names here are the camelCase of the wire names.
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
    var end: [Double]?
    var distanceM: Double?
    var durationS: Double?
    var maxExtraS: Double?
    var headingDeg: Double?
    var seed: Int?
    var twistiness: Double
    var charging: ChargingOptions?
}

struct ChargingOptions: Encodable, Sendable {
    var enabled = true
    var socStart: Double
}

struct PlanResult: Decodable, Sendable, Identifiable, Hashable {
    let id: String
    let mode: String
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
struct Budget: Decodable, Sendable {
    let targetS: Double
    let totalS: Double
    let fits: Bool
}

/// Present on "go somewhere" plans: what the better roads cost over the quickest way.
struct Detour: Decodable, Sendable {
    let fastestS: Double
    let extraS: Double
    let maxExtraS: Double
    let twistiness: Double
    let fits: Bool
}

struct Road: Decodable, Sendable {
    let name: String
    let km: Double
}

struct Instruction: Decodable, Sendable {
    let text: String
    let distanceM: Double
    let timeS: Double
    let sign: Int
    let i: Int
}

struct Stats: Decodable, Sendable {
    let km: Double
    let curvyKm: Double
    let repeatedKm: Double?
    let roadClassKm: [String: Double]
    let urbanDensityKm: [String: Double]
    let surfaceKm: [String: Double]
}

struct LoopInfo: Decodable, Sendable {
    let targetM: Double
    let headingDeg: Double
    let seed: Int
    let score: Double
    let candidates: Int
    let failed: Int
}

struct OutAndBackInfo: Decodable, Sendable {
    let turnaround: [Double]
    let headingDeg: Double
    let outKm: Double
    let backKm: Double
    let sharedKm: Double
}

struct Energy: Decodable, Sendable {
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

struct Charger: Decodable, Sendable, Identifiable {
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
}

struct Handoff: Decodable, Sendable {
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
