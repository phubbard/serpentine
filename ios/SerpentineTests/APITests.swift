import Foundation
import Testing
@testable import Serpentine

// Fixtures are real serpentine-api responses (2026-09-18): a 150 km Ramona loop with charging from
// 60 %, and a 120 km out-and-back. If these stop decoding, docs/API.md and the app have drifted.

private final class BundleToken {}

private func fixture(_ name: String) throws -> PlanResult {
    let url = try #require(Bundle(for: BundleToken.self).url(forResource: name, withExtension: "json"))
    return try SerpentineAPI.decoder().decode(PlanResult.self, from: Data(contentsOf: url))
}

@Test func decodesLoopWithCharging() throws {
    let plan = try fixture("plan_loop_ev")
    #expect(plan.mode == "loop")
    #expect(plan.distanceM > 100_000)
    #expect(plan.polyline.count > 100)
    #expect(plan.polyline[0].count == 3, "polyline carries elevation")
    #expect(plan.loop != nil)
    let energy = try #require(plan.energy)
    #expect(energy.stops >= 1)
    #expect(energy.totalTimeS > plan.timeS, "charging adds time")
    let chargers = try #require(plan.chargers)
    #expect(chargers.contains { $0.stop && $0.role == "stop" && ($0.dwellMin ?? 0) > 0 })
    #expect(plan.handoff.appleMapsUrl.hasPrefix("https://maps.apple.com/directions?"))
    #expect(plan.handoff.waypoints.count == plan.handoff.waypointRoads.count)
    #expect(plan.handoff.waypointRoads.contains { $0.hasPrefix("Charge: ") })
    #expect(plan.gpxUrl.hasSuffix(".gpx"))
}

@Test func decodesOutAndBack() throws {
    let plan = try fixture("plan_outback")
    let ob = try #require(plan.outAndBack)
    #expect(ob.outKm > 0 && ob.backKm > 0)
    #expect(plan.handoff.waypointRoads.contains("Turnaround"))
    #expect(plan.energy == nil && plan.chargers == nil, "no charging asked for, none returned")
}

@Test func encodesRequestInWireFormat() throws {
    let req = PlanRequest(mode: .outAndBack, start: [-116.868, 33.042], distanceM: 150_000,
                          headingDeg: nil, seed: 3, twistiness: 0.5,
                          charging: ChargingOptions(socStart: 0.6))
    let json = try #require(try JSONSerialization.jsonObject(with: SerpentineAPI.encoder().encode(req)) as? [String: Any])
    #expect(json["mode"] as? String == "out_and_back")
    #expect(json["distance_m"] as? Double == 150_000)
    #expect(json["heading_deg"] == nil, "nil options are omitted, not null")
    #expect((json["start"] as? [Double])?.first == -116.868, "[lon, lat] order")
    let charging = try #require(json["charging"] as? [String: Any])
    #expect(charging["soc_start"] as? Double == 0.6)
    #expect(charging["enabled"] as? Bool == true)
}

@Test func coordinatesAreLonLatOnTheWire() {
    let c = [-116.868, 33.042].coordinate
    #expect(c.latitude == 33.042 && c.longitude == -116.868)
    #expect(c.lonLat == [-116.868, 33.042])
}

@MainActor @Test func plannerBuildsRequests() {
    let p = Planner()
    #expect(p.request(seed: 1) == nil, "no start, no request")
    p.start = StartPoint(name: "Ramona", coordinate: [-116.868, 33.042].coordinate)
    p.distanceKm = 200
    p.heading = .east
    let r = p.request(seed: 5)
    #expect(r?.distanceM == 200_000 && r?.headingDeg == 90 && r?.seed == 5 && r?.charging == nil)
    p.charging = true
    p.socPercent = 70
    #expect(p.request(seed: 1)?.charging?.socStart == 0.7)
}

@MainActor @Test func timeBudgetReplacesDistance() throws {
    let p = Planner()
    p.start = StartPoint(name: "Ramona", coordinate: [-116.868, 33.042].coordinate)
    p.budget = .time
    p.durationMin = 120
    let r = try #require(p.request(seed: 1))
    #expect(r.durationS == 7200 && r.distanceM == nil, "the server rejects both together")
    // The wire must omit distance_m entirely, not send null.
    let json = String(data: try SerpentineAPI.encoder().encode(r), encoding: .utf8) ?? ""
    #expect(json.contains("\"duration_s\":7200") && !json.contains("distance_m"))
}

@Test func climbUsesFeetOrMetres() {
    #expect(Format.climb(meters: 1000, locale: Locale(identifier: "en_US")) == "3,281 ft")
    #expect(Format.climb(meters: 1000, locale: Locale(identifier: "en_GB")).hasSuffix("m"))
    #expect(Format.climb(meters: 1000, locale: Locale(identifier: "de_DE")).hasSuffix("m"))
    #expect(!Format.climb(meters: 3000, locale: Locale(identifier: "en_US")).contains("mi"))
}

