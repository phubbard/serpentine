import CoreLocation
import Foundation
import Testing
@testable import Serpentine

private final class ProgressBundleToken {}

private func loopFixture() throws -> PlanResult {
    let url = try #require(Bundle(for: ProgressBundleToken.self).url(forResource: "plan_loop_ev", withExtension: "json"))
    return try SerpentineAPI.decoder().decode(PlanResult.self, from: Data(contentsOf: url))
}

@MainActor
private func store() -> RideProgress {
    RideProgress(directory: URL.temporaryDirectory.appending(path: "RideProgress-\(UUID().uuidString)"))
}

@MainActor
@Test func pausingSplitsTheRideWhereTheRiderActuallyIs() throws {
    let plan = try loopFixture()
    let p = store()
    // Stand on a vertex about a third of the way round.
    let third = plan.polyline[plan.polyline.count / 3].coordinate

    let paused = try #require(p.pause(plan, at: third, title: "Test loop", style: nil, twistiness: 0.5))
    #expect(paused.riddenDistanceM > 0)
    #expect(paused.remainingDistanceM > 0)
    // The two halves should account for the ride, give or take the vertex we snapped to.
    let total = paused.riddenDistanceM + paused.remainingDistanceM
    #expect(abs(total - plan.distanceM) / plan.distanceM < 0.02,
            "ridden \(paused.riddenDistanceM) + left \(paused.remainingDistanceM) should be about \(plan.distanceM)")
    #expect(paused.remaining.last.map { $0 == plan.polyline.last } == true,
            "what's left has to end where the ride ended")
}

@MainActor
@Test func aPausedRideSurvivesTheAppBeingKilled() throws {
    let dir = URL.temporaryDirectory.appending(path: "RideProgress-\(UUID().uuidString)")
    defer { try? FileManager.default.removeItem(at: dir) }
    let plan = try loopFixture()
    let p = RideProgress(directory: dir)
    p.pause(plan, at: plan.polyline[plan.polyline.count / 2].coordinate, title: "Julian loop",
            style: "curvy", twistiness: 0.7)

    // The phone dying is the normal way this ends — that is why the rider is at a charger.
    let reopened = RideProgress(directory: dir)
    let paused = try #require(reopened.paused)
    #expect(paused.title == "Julian loop")
    #expect(paused.twistiness == 0.7)
    #expect(paused.remaining.count > 1)
}

@MainActor
@Test func finishingAnOpenRouteLeavesNothingToResume() throws {
    let plan = try loopFixture()
    let p = store()
    // An open route, where the finish is somewhere the rider has never been: standing on it means
    // the ride is over and offering it back is noise.
    let line = (0..<200).map { [-117.0 + Double($0) * 0.002, 33.0] }
    let open = plan.replacingPolyline(line)
    let end = try #require(line.last).coordinate
    #expect(p.pause(open, at: end, title: "Done", style: "direct", twistiness: 0) == nil)
    #expect(p.paused == nil)
}

// On a loop the start and the finish are the same place, so standing there is genuinely
// undecidable — the app has no odometer and no track of where the rider has been. It resolves the
// tie towards "you have the whole ride left", because offering a ride that was already done is a
// smaller harm than telling someone half-way round that they have finished. The ride view shows the
// split so a rider can see it is wrong and undo it.
@MainActor
@Test func aLoopPausedAtItsOwnStartOffersTheWholeRide() throws {
    let plan = try loopFixture()
    let p = store()
    let startLine = try #require(plan.polyline.first).coordinate
    let paused = try #require(p.pause(plan, at: startLine, title: "Loop", style: nil, twistiness: 0.5))
    #expect(paused.riddenDistanceM < plan.distanceM * 0.05)
    #expect(paused.remainingDistanceM > plan.distanceM * 0.9,
            "the safe reading of an ambiguous position is that the ride is still ahead")
}

// The point of the whole feature: after a detour, carry on from where you are rather than riding
// back to the exact spot you stopped at.
@MainActor
@Test func resumingRejoinsAheadRatherThanBacktracking() throws {
    let plan = try loopFixture()
    let p = store()
    let coords = plan.polyline
    let pauseIdx = coords.count / 3
    p.pause(plan, at: coords[pauseIdx].coordinate, title: "Test", style: "curvy", twistiness: 0.5)

    // The rider carries on from near a point well past where they stopped.
    let laterIdx = pauseIdx + (coords.count - pauseIdx) / 2
    let later = coords[laterIdx].coordinate
    let req = try #require(p.resumeRequest(from: later, charging: nil, vehicle: nil))

    #expect(req.mode == .pointToPoint)
    #expect(req.end == coords.last, "the rest of the ride still finishes where it finished")
    let via = try #require(req.via)
    #expect(!via.isEmpty, "without via points the route stops being the ride they were on")

    // No via point should sit behind them: that is the backtrack this exists to avoid.
    let distanceFromLater = via.map { $0.coordinate.metres(to: later) }
    let backToPause = coords[pauseIdx].coordinate.metres(to: later)
    #expect(distanceFromLater.allSatisfy { $0 < backToPause * 2 },
            "a via point near the old pause position means the detour gets ridden twice")
}

@MainActor
@Test func resumingKeepsTheKindOfRideItWas() throws {
    let plan = try loopFixture()
    let p = store()
    p.pause(plan, at: plan.polyline[10].coordinate, title: "Errand", style: "direct", twistiness: 0.2)
    let req = try #require(p.resumeRequest(from: plan.polyline[20].coordinate, charging: nil, vehicle: nil))
    #expect(req.style == "direct", "a ride that was taking the fast way should carry on doing that")
    #expect(req.twistiness == 0.2)
}

@Test func viaSamplingStaysWithinWhatTheServerAccepts() {
    // The server caps via points; sending more is a 400 in the one moment a rider needs this.
    let long = (0..<4000).map { [Double($0) * 0.001 - 117.0, 33.0] }
    let via = RideProgress.sampleVia(long)
    #expect(via.count <= 24, "the server refuses more than 24")
    #expect(via.count >= 8, "too few and the route wanders off the original roads")
    #expect(!via.contains { $0 == long.last }, "the finish is the destination, not a via point")

    // Degenerate routes must not crash or produce nonsense.
    #expect(RideProgress.sampleVia([]).isEmpty)
    #expect(RideProgress.sampleVia([[-117, 33]]).isEmpty)
    #expect(RideProgress.sampleVia([[-117, 33], [-117.1, 33]]).isEmpty)
}

@MainActor
@Test func forgettingAPausedRideRemovesIt() throws {
    let dir = URL.temporaryDirectory.appending(path: "RideProgress-\(UUID().uuidString)")
    defer { try? FileManager.default.removeItem(at: dir) }
    let p = RideProgress(directory: dir)
    p.pause(try loopFixture(), at: try loopFixture().polyline[5].coordinate, title: "x",
            style: nil, twistiness: 0.5)
    #expect(p.paused != nil)
    p.discard()
    #expect(p.paused == nil)
    #expect(RideProgress(directory: dir).paused == nil, "and it stays gone after a relaunch")
}

extension PlanResult {
    /// A copy of this plan following a different line — for exercising route shapes the fixtures
    /// don't have, like a route whose finish isn't also its start.
    func replacingPolyline(_ line: [[Double]]) -> PlanResult {
        var dict = try! JSONSerialization.jsonObject(with: try! SerpentineAPI.encoder().encode(self)) as! [String: Any]
        dict["polyline"] = line
        let data = try! JSONSerialization.data(withJSONObject: dict)
        return try! SerpentineAPI.decoder().decode(PlanResult.self, from: data)
    }
}
