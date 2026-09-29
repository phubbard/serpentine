import Foundation
import Observation

/// A ride the rider kept: the plan as the server returned it, plus what only the rider knows — how
/// good it was, what to remember about it, and when they last rode it.
///
/// This struct is the *summary*. The plan itself lives in its own file (see `RideStore`), because a
/// 100-mile polyline is a few hundred kilobytes and the list has no business loading one per row.
struct SavedRide: Codable, Identifiable, Equatable, Sendable {
    var id = UUID()
    var title: String
    var savedAt: Date
    /// Nil until the rider says they rode it. Saving a ride to do later is the other half of this
    /// feature, so "saved" and "ridden" are deliberately not the same thing.
    var lastRiddenAt: Date?
    /// 0 means unrated, 1–5 stars. Unrated and one star are very different opinions.
    var rating: Int = 0
    var notes: String = ""

    /// The server's id for the plan. Used to tell whether the ride on screen is already saved; the
    /// file on disk is named by `id`, so saving the same route twice makes two independent entries.
    var planID: String

    // Enough of the plan to draw a row without opening the plan file.
    var mode: String
    var distanceM: Double
    var timeS: Double
    var ascendM: Double
    var startName: String?
    var topRoads: [String]
    var hasCharging: Bool

    /// Most recent activity first: a ride you rode last week outranks one you saved last year.
    var sortDate: Date { lastRiddenAt ?? savedAt }

    var modeLabel: String {
        switch mode {
        case "loop": "Loop"
        case "out_and_back": "Out and back"
        default: "The way there"
        }
    }
}

/// The rides kept on this phone. Local only, no account, no sync (ROADMAP phase 2; iCloud is
/// explicitly out for v1) — which also means nothing here ever reaches the server.
///
/// Layout under Application Support:
/// ```
/// SavedRides/index.json          the summaries, read at launch
/// SavedRides/plans/<uuid>.json   one plan each, read only when a ride is opened
/// ```
@MainActor @Observable
final class RideStore {
    private(set) var rides: [SavedRide] = []
    /// Set when the disk let us down. Surfaced in the UI rather than swallowed: a save that silently
    /// didn't happen is worse than an error.
    private(set) var errorMessage: String?

    private let root: URL
    private var plansDir: URL { root.appending(path: "plans") }
    private var indexFile: URL { root.appending(path: "index.json") }

    /// `root` is injectable so tests get a temporary directory instead of the real one.
    init(root: URL? = nil) {
        if let root {
            self.root = root
        } else {
            let base = (try? FileManager.default.url(for: .applicationSupportDirectory, in: .userDomainMask,
                                                     appropriateFor: nil, create: true))
                ?? URL.documentsDirectory
            self.root = base.appending(path: "SavedRides")
        }
        load()
    }

    /// Dismisses a reported failure once the rider has seen it.
    func clearError() { errorMessage = nil }

    // MARK: - Reading

    /// The saved ride matching a plan on screen, if it was kept.
    func ride(forPlan planID: String) -> SavedRide? {
        rides.first { $0.planID == planID }
    }

    /// The full plan for a saved ride. Nil when the file is gone — a ride whose plan we can't read is
    /// reported rather than silently opened as an empty map.
    func plan(for ride: SavedRide) -> PlanResult? {
        do {
            let data = try Data(contentsOf: planFile(ride.id))
            return try SerpentineAPI.decoder().decode(PlanResult.self, from: data)
        } catch {
            errorMessage = "Couldn't open \(ride.title): \(error.localizedDescription)"
            return nil
        }
    }

    // MARK: - Writing

    /// Keeps a planned ride. `from` and `to` name the ends, which the plan itself doesn't carry.
    @discardableResult
    func save(_ plan: PlanResult, from: String?, to: String? = nil) -> SavedRide? {
        let ride = SavedRide(title: Self.title(for: plan, from: from, to: to),
                             savedAt: .now,
                             planID: plan.id,
                             mode: plan.mode,
                             distanceM: plan.distanceM,
                             timeS: plan.energy?.totalTimeS ?? plan.timeS,
                             ascendM: plan.ascendM,
                             startName: from,
                             topRoads: Self.topRoads(plan),
                             hasCharging: (plan.energy?.stops ?? 0) > 0)
        do {
            try FileManager.default.createDirectory(at: plansDir, withIntermediateDirectories: true)
            // The plan goes down first: an index entry pointing at a file that doesn't exist is the
            // one failure that would look like data loss rather than a failed save.
            try SerpentineAPI.encoder().encode(plan).write(to: planFile(ride.id), options: .atomic)
            rides.append(ride)
            sortAndWriteIndex()
            errorMessage = nil
            return ride
        } catch {
            errorMessage = "Couldn't save this ride: \(error.localizedDescription)"
            return nil
        }
    }

    /// Writes back an edited summary — rating, notes, title, when it was last ridden.
    func update(_ ride: SavedRide) {
        guard let i = rides.firstIndex(where: { $0.id == ride.id }) else { return }
        rides[i] = ride
        sortAndWriteIndex()
    }

    func remove(_ ride: SavedRide) {
        rides.removeAll { $0.id == ride.id }
        try? FileManager.default.removeItem(at: planFile(ride.id))
        sortAndWriteIndex()
    }

    func remove(atOffsets offsets: IndexSet) {
        offsets.map { rides[$0] }.forEach(remove)
    }

    // MARK: - Disk

    private func planFile(_ id: UUID) -> URL {
        plansDir.appending(path: "\(id.uuidString).json")
    }

    private func load() {
        guard let data = try? Data(contentsOf: indexFile) else { return } // first run
        do {
            rides = try JSONDecoder().decode([SavedRide].self, from: data)
                .sorted { $0.sortDate > $1.sortDate }
        } catch {
            // A corrupt index would otherwise take the app down on launch. The plan files stay on
            // disk, so nothing is destroyed by starting empty.
            errorMessage = "Saved rides couldn't be read: \(error.localizedDescription)"
        }
    }

    private func sortAndWriteIndex() {
        rides.sort { $0.sortDate > $1.sortDate }
        do {
            try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
            try JSONEncoder().encode(rides).write(to: indexFile, options: .atomic)
        } catch {
            errorMessage = "Couldn't update saved rides: \(error.localizedDescription)"
        }
    }

    // MARK: - Naming

    /// A name the rider will recognise in a list six months from now: how far, what kind, and from
    /// where. They can rename it, but the default shouldn't need renaming.
    nonisolated static func title(for plan: PlanResult, from: String?, to: String?) -> String {
        let distance = Format.distance(km: plan.distanceM / 1000)
        let start = from.flatMap { $0 == "My location" ? nil : $0 }
        switch plan.mode {
        case "loop":
            return start.map { "\(distance) loop from \($0)" } ?? "\(distance) loop"
        case "out_and_back":
            return start.map { "\(distance) out and back from \($0)" } ?? "\(distance) out and back"
        default:
            if let to { return start.map { "\($0) → \(to)" } ?? "To \(to)" }
            return "\(distance) ride"
        }
    }

    /// The three longest named roads — what actually distinguishes one loop from another.
    nonisolated static func topRoads(_ plan: PlanResult) -> [String] {
        plan.roads
            .filter { !$0.name.isEmpty && $0.km >= 2 }
            .sorted { $0.km > $1.km }
            .prefix(3)
            .map(\.name)
    }
}
