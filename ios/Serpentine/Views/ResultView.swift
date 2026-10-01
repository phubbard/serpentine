import MapKit
import SwiftUI

/// The planned ride: map, numbers, charge stops, and the hand-off to Apple Maps for voice guidance.
struct ResultView: View {
    let plan: PlanResult
    /// False when the ride wasn't planned from the plan screen — the way to a charger (ADR-034) has
    /// no "another one like this", and offering it would replan whatever the form happens to say.
    var canReplan = true
    @Environment(Planner.self) private var planner
    @Environment(RideStore.self) private var rides
    @Environment(RideProgress.self) private var progress
    @Environment(LocationProvider.self) private var location
    @Environment(\.openURL) private var openURL
    @Environment(\.horizontalSizeClass) private var sizeClass
    @State private var gpxFile: URL?
    /// The saved entry for the ride on screen, when it has been kept.
    @State private var saved: SavedRide?
    @State private var confirmingRemove = false
    @State private var pausedSplit: PausedRide?
    @State private var pauseProblem: String?

    private var coordinates: [CLLocationCoordinate2D] { plan.polyline.map(\.coordinate) }
    private var stops: [Charger] { (plan.chargers ?? []).filter(\.stop) }

    /// The nearest listed backup to a stop, so a rider who finds it dead or full knows where to go
    /// next without opening the app again (ADR-021: "move to the next stall" is the real mitigation).
    private func backup(for stop: Charger) -> Charger? {
        (plan.chargers ?? [])
            .filter { $0.role == "backup" && ($0.reliability?.operational ?? true) }
            .min { abs($0.kmFromStart - stop.kmFromStart) < abs($1.kmFromStart - stop.kmFromStart) }
    }

    var body: some View {
        List {
            Section {
                RouteMap(plan: plan)
                    .frame(height: sizeClass == .regular ? 600 : 340)
                    .listRowInsets(EdgeInsets())
            }

            Section {
                HStack {
                    stat(Format.distance(km: plan.distanceM / 1000), "distance")
                    stat(Format.duration(seconds: plan.energy?.totalTimeS ?? plan.timeS), timeLabel)
                    stat(Format.climb(meters: plan.ascendM), "climb")
                }
                Button {
                    if let url = URL(string: plan.handoff.appleMapsUrl) { openURL(url) }
                } label: {
                    Label("Navigate in Apple Maps", systemImage: "arrow.triangle.turn.up.right.diamond.fill")
                        .frame(maxWidth: .infinity)
                        .bold()
                }
                .buttonStyle(.borderedProminent)
                .listRowSeparator(.hidden)
                if let gpxFile {
                    ShareLink(item: gpxFile, preview: SharePreview("Serpentine ride (GPX)")) {
                        Label("Share GPX", systemImage: "square.and.arrow.up").frame(maxWidth: .infinity)
                    }
                }
                #if targetEnvironment(macCatalyst)
                // On the Mac the toolbar is easy to miss, and "give me a different ride" is the most
                // used control there is. Keep it in the page as well (⌘R does the same).
                if canReplan { anotherButton.frame(maxWidth: .infinity) }
                #endif
            }

            if let saved {
                SavedRideEditor(ride: saved).id(saved.id)
            }

            if let e = plan.energy {
                Section("Charging") {
                    LabeledContent("Energy", value: String(format: "%.1f of %.1f kWh", e.kwhEst, e.usableKwh))
                    LabeledContent("Charge", value: "\(Format.percent(e.socStart)) → \(Format.percent(e.socEndEst))")
                    if let warning = e.warning {
                        Label(warning, systemImage: "exclamationmark.triangle.fill").foregroundStyle(.orange)
                    }
                    ForEach(stops) { c in
                        VStack(alignment: .leading, spacing: 2) {
                            Text(c.name).bold()
                            Text("\(Format.distance(km: c.kmFromStart)) in · arrive \(Format.percent(c.socArrivalEst)) · charge \(Int(c.dwellMin ?? 0)) min")
                                .font(.subheadline)
                            Text("\(c.ports) ports · \(c.network)").font(.caption).foregroundStyle(.secondary)
                            if let note = c.reliability?.note {
                                Label(note, systemImage: "exclamationmark.bubble")
                                    .font(.caption).foregroundStyle(.orange)
                            }
                            if let backup = backup(for: c) {
                                Label("Backup: \(backup.name), \(Format.distance(km: backup.kmFromStart)) in",
                                      systemImage: "arrow.triangle.branch")
                                    .font(.caption).foregroundStyle(.secondary)
                            }
                        }
                    }
                }
            }

            Section("Roads") {
                ForEach(Array(plan.roads.filter { $0.km >= 2 && !$0.name.isEmpty }.enumerated()), id: \.offset) { _, road in
                    LabeledContent(road.name, value: Format.distance(km: road.km))
                }
            }

            Section {
                Text(footnote).font(.footnote).foregroundStyle(.secondary)
            }
        }
        .navigationTitle(title)
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            // Explicit placement: a bare toolbar button doesn't reliably reach the window toolbar on
            // Mac Catalyst, where there is no navigation bar to fall back on.
            if canReplan {
                ToolbarItem(placement: .primaryAction) {
                    anotherButton
                        .keyboardShortcut("r", modifiers: .command)
                }
            }
            ToolbarItem(placement: .primaryAction) { saveButton }
            if canReplan {
                ToolbarItem(placement: .primaryAction) {
                    Button("Pause here", systemImage: "pause.circle") { pauseHere() }
                }
            }
        }
        // The split is shown rather than assumed: on a loop, the start and the finish are the same
        // place, so a rider standing near either gets an answer the app cannot verify. Let them see
        // it and undo it on the spot.
        .alert("Put the ride down here?", isPresented: .constant(pausedSplit != nil)) {
            Button("That's right") { pausedSplit = nil }
            Button("Not right — forget it", role: .destructive) {
                progress.discard()
                pausedSplit = nil
            }
        } message: {
            if let p = pausedSplit {
                Text("\(Format.distance(km: p.riddenDistanceM / 1000)) ridden, "
                     + "\(Format.distance(km: p.remainingDistanceM / 1000)) to go. "
                     + "Carry on from the plan screen when you're ready — you'll pick up the rest "
                     + "from wherever you are then.")
            }
        }
        .alert("Can't pause", isPresented: .constant(pauseProblem != nil)) {
            Button("OK") { pauseProblem = nil }
        } message: {
            Text(pauseProblem ?? "")
        }
        .task(id: plan.id) { gpxFile = await planner.gpxFile(for: plan) }
        // Keyed on the plan so opening a saved ride, or planning another, re-reads the entry.
        .onChange(of: plan.id, initial: true) { _, _ in saved = rides.ride(forPlan: plan.id) }
        .confirmationDialog("Remove this ride from your saved list?",
                            isPresented: $confirmingRemove, titleVisibility: .visible) {
            Button("Remove", role: .destructive) {
                if let saved { rides.remove(saved) }
                saved = nil
            }
        } message: {
            Text("The rating and notes go with it. The ride itself stays on screen.")
        }
    }

    /// Save, then un-save. A bookmark that can't be undone in the same place it was set is a trap,
    /// but losing typed notes to a stray tap is worse — hence the confirmation on the way out.
    @ViewBuilder private var saveButton: some View {
        if saved == nil {
            Button {
                saved = rides.save(plan, from: planner.start?.name, to: planner.destination?.name)
            } label: {
                Label("Save", systemImage: "bookmark")
            }
            .keyboardShortcut("s", modifiers: .command)
        } else {
            Button {
                confirmingRemove = true
            } label: {
                Label("Saved", systemImage: "bookmark.fill")
            }
        }
    }

    /// Splits the ride at wherever the rider actually is. Needs a location: the whole idea is "the
    /// part I haven't ridden", and without a fix there is nothing to split on.
    private func pauseHere() {
        Task {
            if location.coordinate == nil { await location.locate() }
            guard let here = location.coordinate else {
                pauseProblem = "This needs your location to know how much of the ride is left."
                return
            }
            let title = RideStore.title(for: plan, from: planner.start?.name, to: planner.destination?.name)
            guard let ride = progress.pause(plan, at: here, title: title,
                                            style: plan.style,
                                            twistiness: planner.twistiness) else {
                pauseProblem = "There's no ride left to come back to from here."
                return
            }
            pausedSplit = ride
        }
    }

    private var anotherButton: some View {
        Button {
            Task { await planner.plan(another: true) }
        } label: {
            if planner.isPlanning {
                ProgressView()
            } else {
                Label("Another", systemImage: "arrow.triangle.2.circlepath")
            }
        }
        .disabled(planner.isPlanning)
    }

    private func stat(_ value: String, _ label: String) -> some View {
        VStack {
            Text(value).font(.title3.bold()).monospacedDigit()
            Text(label).font(.caption).foregroundStyle(.secondary)
        }
        .frame(maxWidth: .infinity)
    }

    private var title: String {
        if planner.isResumedRide { return "Rest of the ride" }
        switch plan.mode {
        case "loop": return "Loop"
        case "out_and_back": return "Out and back"
        default: return "The way there"
        }
    }

    /// Under a time budget the second number is what matters, so label it against the budget.
    private var timeLabel: String {
        if let b = plan.budget {
            return "of \(Format.duration(seconds: b.targetS))"
        }
        return (plan.energy?.stops ?? 0) > 0 ? "incl. charging" : "riding"
    }

    private var footnote: String {
        let curvy = Format.distance(km: plan.stats.curvyKm)
        var s = "\(curvy) of properly twisty road. Apple Maps gets \(plan.handoff.waypoints.count) stops so it follows this route."
        if let d = plan.detour {
            s += d.extraS < 60
                ? " No better roads were worth a detour here: this is the quick way."
                : " \(Format.duration(seconds: d.extraS)) longer than the quick way, spent on better roads."
        }
        if let b = plan.budget, !b.fits {
            s += " This is the shortest ride we could find here; it runs over your \(Format.duration(seconds: b.targetS))."
        }
        if let o = plan.outAndBack {
            s += " Out \(Format.distance(km: o.outKm)), back \(Format.distance(km: o.backKm)) on different roads."
        }
        return s
    }
}

/// MapKit display of the route, start, charge stops and turnaround.
struct RouteMap: View {
    let plan: PlanResult

    var body: some View {
        Map(initialPosition: .automatic) {
            MapPolyline(coordinates: plan.polyline.map(\.coordinate))
                // Map content doesn't inherit the app's accent: .tint and Color.accentColor both draw system
                // blue here, so name the asset colour explicitly.
                .stroke(Color("AccentColor"), style: StrokeStyle(lineWidth: 5, lineCap: .round, lineJoin: .round))
            Marker("Start", systemImage: "flag.checkered", coordinate: plan.handoff.source.coordinate)
                .tint(.primary)
            ForEach(Array(zip(plan.handoff.waypoints, plan.handoff.waypointRoads).enumerated()), id: \.offset) { _, pair in
                let (point, name) = pair
                if name == "Turnaround" {
                    Marker("Turnaround", systemImage: "arrow.uturn.backward", coordinate: point.coordinate).tint(.orange)
                }
            }
            ForEach((plan.chargers ?? []).filter(\.stop)) { c in
                Marker(c.name, systemImage: "bolt.fill", coordinate: c.lonlat.coordinate).tint(.orange)
            }
        }
        .mapStyle(.standard(elevation: .realistic, pointsOfInterest: .excludingAll))
    }
}
