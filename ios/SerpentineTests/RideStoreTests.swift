import Foundation
import Testing
@testable import Serpentine

private final class RideBundleToken {}

private func planFixture(_ name: String = "plan_loop_ev") throws -> PlanResult {
    let url = try #require(Bundle(for: RideBundleToken.self).url(forResource: name, withExtension: "json"))
    return try SerpentineAPI.decoder().decode(PlanResult.self, from: Data(contentsOf: url))
}

/// Each test gets its own directory, so nothing leaks between them or onto the dev machine's real
/// saved rides.
@MainActor
private func tempStore() throws -> (RideStore, URL) {
    let dir = URL.temporaryDirectory.appending(path: "RideStoreTests-\(UUID().uuidString)")
    return (RideStore(root: dir), dir)
}

@MainActor
@Test func aSavedRideSurvivesARelaunch() throws {
    let (store, dir) = try tempStore()
    defer { try? FileManager.default.removeItem(at: dir) }
    let plan = try planFixture()

    let ride = try #require(store.save(plan, from: "Ramona, CA"))
    #expect(store.rides.count == 1)
    #expect(ride.planID == plan.id)
    #expect(ride.lastRiddenAt == nil, "saving is not the same as having ridden it")

    // A second store over the same directory is what the next launch sees.
    let reopened = RideStore(root: dir)
    #expect(reopened.rides.count == 1)
    #expect(reopened.rides[0].id == ride.id)
    #expect(reopened.rides[0].title == ride.title)
}

@MainActor
@Test func theStoredPlanIsTheServersAnswerUnchanged() throws {
    let (store, dir) = try tempStore()
    defer { try? FileManager.default.removeItem(at: dir) }
    let plan = try planFixture()
    let ride = try #require(store.save(plan, from: "Ramona, CA"))

    // The point of storing the whole plan rather than the request: what comes back is the same ride,
    // down to the geometry Apple Maps is handed.
    let loaded = try #require(RideStore(root: dir).plan(for: ride))
    #expect(loaded.id == plan.id)
    #expect(loaded.distanceM == plan.distanceM)
    #expect(loaded.polyline.count == plan.polyline.count)
    #expect(loaded.handoff.appleMapsUrl == plan.handoff.appleMapsUrl)
    #expect(loaded.roads.count == plan.roads.count)
    #expect(loaded.energy?.kwhEst == plan.energy?.kwhEst)
    #expect(loaded.chargers?.count == plan.chargers?.count)
}

@MainActor
@Test func ratingAndNotesAreKept() throws {
    let (store, dir) = try tempStore()
    defer { try? FileManager.default.removeItem(at: dir) }
    var ride = try #require(store.save(try planFixture(), from: "Ramona, CA"))

    ride.rating = 5
    ride.notes = "Gravel at the Mesa Grande turn."
    ride.lastRiddenAt = Date(timeIntervalSince1970: 1_790_000_000)
    store.update(ride)

    let reopened = RideStore(root: dir)
    #expect(reopened.rides[0].rating == 5)
    #expect(reopened.rides[0].notes == "Gravel at the Mesa Grande turn.")
    #expect(reopened.rides[0].lastRiddenAt != nil)
}

@MainActor
@Test func mostRecentlyRiddenComesFirst() throws {
    let (store, dir) = try tempStore()
    defer { try? FileManager.default.removeItem(at: dir) }
    let plan = try planFixture()

    let older = try #require(store.save(plan, from: "Ramona, CA"))
    var newer = try #require(store.save(plan, from: "Julian, CA"))
    // A ride saved second but ridden long ago should fall behind one saved first.
    newer.lastRiddenAt = Date(timeIntervalSince1970: 1_000_000)
    store.update(newer)

    #expect(store.rides.first?.id == older.id, "an old ride ridden long ago doesn't lead the list")
    #expect(store.rides.count == 2, "the same route saved twice is two entries, not one")
}

@MainActor
@Test func removingARideTakesItsPlanFileWithIt() throws {
    let (store, dir) = try tempStore()
    defer { try? FileManager.default.removeItem(at: dir) }
    let ride = try #require(store.save(try planFixture(), from: "Ramona, CA"))
    let planFile = dir.appending(path: "plans/\(ride.id.uuidString).json")
    #expect(FileManager.default.fileExists(atPath: planFile.path))

    store.remove(ride)
    #expect(store.rides.isEmpty)
    #expect(!FileManager.default.fileExists(atPath: planFile.path), "no orphaned megabytes on disk")
    #expect(RideStore(root: dir).rides.isEmpty)
}

@MainActor
@Test func aMissingPlanFileIsReportedNotIgnored() throws {
    let (store, dir) = try tempStore()
    defer { try? FileManager.default.removeItem(at: dir) }
    let ride = try #require(store.save(try planFixture(), from: "Ramona, CA"))
    try FileManager.default.removeItem(at: dir.appending(path: "plans/\(ride.id.uuidString).json"))

    let reopened = RideStore(root: dir)
    #expect(reopened.plan(for: ride) == nil)
    #expect(reopened.errorMessage != nil, "a ride that can't be opened must say so")
}

@MainActor
@Test func theRideOnScreenKnowsWhetherItIsSaved() throws {
    let (store, dir) = try tempStore()
    defer { try? FileManager.default.removeItem(at: dir) }
    let plan = try planFixture()
    #expect(store.ride(forPlan: plan.id) == nil)
    store.save(plan, from: "Ramona, CA")
    #expect(store.ride(forPlan: plan.id) != nil)
}

@Test func defaultTitlesReadLikeSomethingARiderWouldWrite() throws {
    let plan = try planFixture()
    // "My location" is a label, not a place — it would age badly in a list.
    let anonymous = RideStore.title(for: plan, from: "My location", to: nil)
    #expect(!anonymous.contains("My location"))
    #expect(anonymous.contains("loop"))
    #expect(RideStore.title(for: plan, from: "Ramona, CA", to: nil).contains("from Ramona, CA"))
}

@Test func topRoadsAreTheLongestNamedOnes() throws {
    let plan = try planFixture()
    let roads = RideStore.topRoads(plan)
    #expect(roads.count <= 3)
    #expect(!roads.contains(""))
    if let longest = plan.roads.filter({ !$0.name.isEmpty && $0.km >= 2 }).max(by: { $0.km < $1.km }) {
        #expect(roads.first == longest.name)
    }
}
