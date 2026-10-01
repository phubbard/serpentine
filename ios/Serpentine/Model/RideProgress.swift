import CoreLocation
import Foundation
import Observation

/// A ride put down part-way through, so it can be picked up again (ADR-035). The case this exists
/// for: half-way round a loop, low on charge, detour to a charger, then carry on with the rest.
///
/// What's kept is the *geometry* of the part not yet ridden. It isn't a plan — resuming has to be
/// routed fresh from wherever the rider ends up, which is rarely where they stopped.
struct PausedRide: Codable, Equatable, Sendable {
    var title: String
    var pausedAt: Date
    /// The original ride's settings, so the rest of it rides like the first half did.
    var style: String?
    var twistiness: Double
    var mode: String
    /// Remaining route vertices as [lon, lat], from where the rider stopped to the finish.
    var remaining: [[Double]]
    /// What the whole ride was, for "43 of 88 miles done".
    var totalDistanceM: Double
    var riddenDistanceM: Double

    var remainingDistanceM: Double {
        guard remaining.count > 1 else { return 0 }
        var m = 0.0
        for i in 1..<remaining.count {
            m += remaining[i - 1].coordinate.metres(to: remaining[i].coordinate)
        }
        return m
    }

    /// Below this there is no ride left worth resuming, and offering it would just be noise.
    var isWorthResuming: Bool { remainingDistanceM > 1000 }

    var endPoint: [Double]? { remaining.last }
}

/// Where a ride was put down, kept on this device. One at a time: a rider has one bike and is on one
/// ride, and a list of half-finished rides would be clutter rather than a feature.
@MainActor @Observable
final class RideProgress {
    private(set) var paused: PausedRide?

    private let file: URL

    init(directory: URL? = nil) {
        let base = directory
            ?? (try? FileManager.default.url(for: .applicationSupportDirectory, in: .userDomainMask,
                                             appropriateFor: nil, create: true))
            ?? URL.documentsDirectory
        file = base.appending(path: "PausedRide.json")
        if let data = try? Data(contentsOf: file) {
            paused = try? JSONDecoder().decode(PausedRide.self, from: data)
        }
    }

    /// Puts a ride down at the rider's current position. The split is by nearest vertex, which is
    /// honest everywhere except within a few hundred metres of the start of a loop, where the start
    /// and the finish are the same place — so the ride view shows the rider the split before they
    /// rely on it.
    @discardableResult
    func pause(_ plan: PlanResult, at here: CLLocationCoordinate2D, title: String,
               style: String?, twistiness: Double) -> PausedRide? {
        let coords = plan.polyline
        guard coords.count > 1, let i = nearestIndex(in: coords, to: here) else { return nil }
        var ridden = 0.0
        if i > 0 {
            for k in 1...i { ridden += coords[k - 1].coordinate.metres(to: coords[k].coordinate) }
        }
        let ride = PausedRide(title: title, pausedAt: .now, style: style, twistiness: twistiness,
                              mode: plan.mode, remaining: Array(coords[i...]),
                              totalDistanceM: plan.distanceM, riddenDistanceM: ridden)
        guard ride.isWorthResuming else { return nil }
        paused = ride
        save()
        return ride
    }

    func discard() {
        paused = nil
        try? FileManager.default.removeItem(at: file)
    }

    /// The request that carries on with the ride from wherever the rider is now.
    ///
    /// The rejoin point is the nearest vertex of what's *left*, not the spot they stopped at: after
    /// a detour to a charger, routing back to the exact pause point would ride the detour twice.
    /// The rest is pinned with via points, because left to itself the curvy profile finds its own
    /// bends between them and stops being the ride the rider was on — measured 2026-10-01, 16 points
    /// held 11 of 14 original roads against 8 of 14 with seven.
    func resumeRequest(from here: CLLocationCoordinate2D, charging: ChargingOptions?,
                       vehicle: VehicleWire?) -> PlanRequest? {
        guard let ride = paused, ride.isWorthResuming,
              let j = nearestIndex(in: ride.remaining, to: here) else { return nil }
        let ahead = Array(ride.remaining[j...])
        guard let end = ahead.last, ahead.count > 1 else { return nil }
        return PlanRequest(
            mode: .pointToPoint,
            start: here.lonLat,
            via: Self.sampleVia(ahead),
            end: end,
            distanceM: nil, durationS: nil, maxExtraS: nil,
            style: ride.style ?? "curvy",
            headingDeg: nil, seed: nil, twistiness: ride.twistiness,
            charging: charging, vehicle: vehicle)
    }

    /// Evenly spaced points along what's left, excluding the finish (which is the destination).
    nonisolated static func sampleVia(_ coords: [[Double]], count: Int = 16) -> [[Double]] {
        guard coords.count > 2, count > 0 else { return [] }
        let inner = coords.count - 1 // the last one is the destination
        guard inner > 1 else { return [] }
        let step = max(1, inner / (count + 1))
        var out: [[Double]] = []
        var i = step
        while i < inner && out.count < count {
            out.append(coords[i])
            i += step
        }
        return out
    }

    private func save() {
        guard let paused else { return }
        try? FileManager.default.createDirectory(at: file.deletingLastPathComponent(),
                                                 withIntermediateDirectories: true)
        try? JSONEncoder().encode(paused).write(to: file, options: .atomic)
    }
}

/// The vertex closest to a point, or nil for an empty route.
func nearestIndex(in coords: [[Double]], to here: CLLocationCoordinate2D) -> Int? {
    var best: (i: Int, m: Double)?
    for (i, c) in coords.enumerated() where c.count >= 2 {
        let d = c.coordinate.metres(to: here)
        if best == nil || d < best!.m { best = (i, d) }
    }
    return best?.i
}

extension CLLocationCoordinate2D {
    /// Straight-line metres between two coordinates.
    func metres(to other: CLLocationCoordinate2D) -> CLLocationDistance {
        CLLocation(latitude: latitude, longitude: longitude)
            .distance(from: CLLocation(latitude: other.latitude, longitude: other.longitude))
    }
}
