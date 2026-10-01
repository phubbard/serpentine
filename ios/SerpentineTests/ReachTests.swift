import CoreLocation
import Foundation
import Testing
@testable import Serpentine

private final class ReachBundleToken {}

private func reachFixture(_ name: String = "reach_ramona") throws -> ReachResult {
    let url = try #require(Bundle(for: ReachBundleToken.self).url(forResource: name, withExtension: "json"))
    return try SerpentineAPI.decoder().decode(ReachResult.self, from: Data(contentsOf: url))
}

@Test func decodesARealReachAnswer() throws {
    let r = try reachFixture()
    #expect(!r.options.isEmpty)
    #expect(r.rangeKmEst > 0)
    #expect(r.nearby >= r.options.count, "nearby counts everything in range, options are the shortlist")

    let first = try #require(r.options.first)
    #expect(!first.name.isEmpty)
    #expect(first.ports > 0, "a site with no ports the bike can use has no business in this list")
    #expect(first.distanceM > 0)
}

// The whole reason the endpoint exists. If this ever comes back sorted by distance the feature is
// gone, and nothing on screen would say so.
@Test func optionsArriveSortedByBatteryNotDistance() throws {
    let r = try reachFixture()
    let needed = r.options.map(\.socNeeded)
    #expect(needed == needed.sorted(), "the list must arrive cheapest-to-reach first: \(needed)")

    // And the fixture should actually prove the two orders differ, or the test is vacuous.
    let byDistance = r.options.sorted { $0.distanceM < $1.distanceM }.map(\.id)
    #expect(byDistance != r.options.map(\.id),
            "this fixture no longer demonstrates the difference between nearest and cheapest")
}

@Test func arrivalChargeFollowsFromWhatTheRideCosts() throws {
    let r = try reachFixture()
    for o in r.options where o.reachable {
        let expected = r.socStart - o.socNeeded
        #expect(abs(o.socArrivalEst - expected) < 0.02,
                "\(o.name): arrives at \(o.socArrivalEst) but \(r.socStart) - \(o.socNeeded) is \(expected)")
    }
}

@Test func anythingOverTheRemainingChargeIsMarked() throws {
    let r = try reachFixture()
    for o in r.options {
        #expect(o.reachable == (o.socNeeded <= r.socStart),
                "\(o.name) needs \(o.socNeeded) of \(r.socStart) and says reachable=\(o.reachable)")
    }
}

// "0 %" for a two-kilometre hop reads as a broken estimate rather than as "that one's nearly free".
@Test func tinyShareReadsAsLessThanOnePercent() {
    #expect(Format.packShare(0.003) == "<1 %")
    #expect(Format.packShare(0) == "0 %", "a genuine zero is not the same as almost nothing")
    #expect(Format.packShare(0.08) == "8 %")
    #expect(Format.packShare(0.235) == "24 %")
}

// The ride to a charger must ask for the efficient route and must not hold back a reserve — the
// reserve rule is what refuses to plan for the rider who most needs it.
@Test func theRideToAChargerAsksForTheRightThing() throws {
    let option = try #require(try reachFixture().options.first)
    let req = ReachFinder.request(to: option,
                                  from: CLLocationCoordinate2D(latitude: 33.0417, longitude: -116.8681),
                                  bike: .srs, soc: 0.12)
    #expect(req.style == "efficient", "anything else routes for time and burns charge this rider hasn't got")
    #expect(req.mode == .pointToPoint)
    #expect(req.end == option.lonlat)
    #expect(req.charging?.socStart == 0.12)
    #expect(req.charging?.reserveForBackup == false, "a ride to a charger has no backup to hold charge for")
    #expect(req.vehicle != nil, "the server scores candidates by energy and needs the bike to do it")
    #expect(req.maxExtraS == nil, "a detour budget is a scenic-route idea and has no meaning here")
}

/// Answers every request from a canned body, and counts them. The only way to exercise the client
/// without a network — `SerpentineAPI` takes protocol classes for exactly this.
final class StubProtocol: URLProtocol, @unchecked Sendable {
    nonisolated(unsafe) static var body = Data()
    nonisolated(unsafe) static var calls = 0

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        Self.calls += 1
        let resp = HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil,
                                   headerFields: ["Content-Type": "application/json"])!
        client?.urlProtocol(self, didReceive: resp, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Self.body)
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {}
}

// Each search costs the server a fan-out of routes, so standing still must not re-ask.
@MainActor
@Test func repeatSearchesFromTheSameSpotAreSkipped() async throws {
    let url = try #require(Bundle(for: ReachBundleToken.self).url(forResource: "reach_ramona", withExtension: "json"))
    StubProtocol.body = try Data(contentsOf: url)
    StubProtocol.calls = 0
    let api = SerpentineAPI(base: URL(string: "https://example.invalid/v1/")!, protocolClasses: [StubProtocol.self])
    let finder = ReachFinder(api: api)
    finder.socPercent = 20
    let here = CLLocationCoordinate2D(latitude: 33.0417, longitude: -116.8681)

    await finder.search(from: here, bike: .srs)
    #expect(finder.result != nil, "the stubbed answer should have decoded")
    await finder.search(from: here, bike: .srs)
    #expect(StubProtocol.calls == 1, "the same charge from the same place should not be asked twice")

    finder.socPercent = 25
    await finder.search(from: here, bike: .srs)
    #expect(StubProtocol.calls == 2, "a different charge is a different answer")

    // A rider who has moved a few streets gets the cached answer; one who has moved a town does not.
    await finder.search(from: CLLocationCoordinate2D(latitude: 33.0419, longitude: -116.8683), bike: .srs)
    #expect(StubProtocol.calls == 2, "a few hundred metres is the same answer")
    await finder.search(from: CLLocationCoordinate2D(latitude: 32.7157, longitude: -117.1611), bike: .srs)
    #expect(StubProtocol.calls == 3, "a different town is not")
}
