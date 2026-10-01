import CoreLocation
import SwiftUI

/// Where, what kind of ride, how far, how twisty, and whether to plan charging.
struct PlanView: View {
    @Environment(Planner.self) private var planner
    @Environment(LocationProvider.self) private var location
    @State private var searching = false
    @State private var searchingDestination = false
    @State private var showingGarage = false
    @State private var showingAbout = false
    @State private var showingRides = false
    @State private var showingReach = false
    @Environment(Garage.self) private var garage
    @Environment(ReachFinder.self) private var reach

    var body: some View {
        @Bindable var planner = planner
        Form {
            Section("Start") {
                startRow
                Button("Use my location", systemImage: "location") {
                    Task {
                        await location.locate()
                        if let c = location.coordinate {
                            planner.start = StartPoint(name: "My location", coordinate: c)
                        }
                    }
                }
                Button("Search for a place", systemImage: "magnifyingglass") { searching = true }
            }

            Section {
                Picker("Ride", selection: $planner.mode) {
                    Text("Loop").tag(RideMode.loop)
                    Text("Out and back").tag(RideMode.outAndBack)
                    Text("Go somewhere").tag(RideMode.pointToPoint)
                }
                .pickerStyle(.segmented)
                if planner.mode == .pointToPoint {
                    destinationRow
                    Button("Choose destination", systemImage: "magnifyingglass") { searchingDestination = true }
                    Toggle("Just get me there", isOn: $planner.directRoute).tint(.accentColor)
                    if !planner.directRoute {
                        LabeledContent("Extra time", value: "+\(Int(planner.maxExtraMin)) min")
                        Slider(value: $planner.maxExtraMin, in: 0...60, step: 5)
                    }
                } else {
                    Picker("Plan by", selection: $planner.budget) {
                        ForEach(RideBudget.allCases) { Text($0.label).tag($0) }
                    }
                    .pickerStyle(.segmented)
                    switch planner.budget {
                    case .distance:
                        LabeledContent("Distance", value: Format.distance(km: planner.distanceKm))
                        Slider(value: $planner.distanceKm, in: 40...400, step: 10)
                    case .time:
                        LabeledContent("Time", value: Format.duration(seconds: planner.durationMin * 60))
                        Slider(value: $planner.durationMin, in: 30...480, step: 15)
                    }
                }
                LabeledContent("Twistiness", value: twistLabel)
                Slider(value: $planner.twistiness, in: 0...1, step: 0.1)
                if planner.mode != .pointToPoint {
                    Picker("Head", selection: $planner.heading) {
                        Text("Any direction").tag(Heading?.none)
                        ForEach(Heading.allCases) { Text($0.label).tag(Heading?.some($0)) }
                    }
                }
            } header: {
                Text("Ride")
            } footer: {
                if planner.mode == .pointToPoint {
                    Text(planner.directRoute
                         ? "Fastest way, freeways and all — for the errand, not the ride. Charge stops are still planned."
                         : "The quickest way, plus up to the extra time you allow spent on better roads.")
                }
            }

            Section {
                Toggle("Plan charging", isOn: $planner.charging).tint(.accentColor)
                if planner.charging {
                    LabeledContent("Starting charge", value: "\(Int(planner.socPercent)) %")
                    Slider(value: $planner.socPercent, in: 20...100, step: 5)
                    Toggle("Keep enough for a backup", isOn: $planner.reserveForBackup).tint(.accentColor)
                }
            } header: {
                HStack {
                    Text(garage.selected.name)
                    Spacer()
                    Button("Garage") { showingGarage = true }.font(.caption).textCase(nil)
                }
            } footer: {
                Text(chargingFootnote)
            }

            Section {
                Button {
                    Task { await planner.plan() }
                } label: {
                    HStack {
                        Spacer()
                        if planner.isPlanning { ProgressView() } else { Text(planButtonTitle).bold() }
                        Spacer()
                    }
                }
                .disabled(!planner.canPlan || planner.isPlanning)
                if let error = planner.errorMessage {
                    Text(error).foregroundStyle(.red)
                }
                #if !targetEnvironment(macCatalyst)
                // On the Mac this lives in the app menu; everywhere else it needs a way in.
                Button("About Serpentine") { showingAbout = true }
                    .font(.footnote)
                #endif
            }
        }
        .navigationTitle("Serpentine")
        .toolbar {
            // Explicit placement, as in ResultView: Mac Catalyst has no navigation bar to fall back on.
            ToolbarItem(placement: .primaryAction) {
                Button("Saved rides", systemImage: "bookmark") { showingRides = true }
            }
            // Wanted in a hurry and usually when something has gone wrong, so it lives in the bar
            // rather than three taps down in the charging section.
            ToolbarItem(placement: .primaryAction) {
                Button("Find a charger", systemImage: "bolt.badge.clock") { openReach() }
            }
        }
        .sheet(isPresented: $showingRides) { RidesView() }
        .sheet(isPresented: $showingReach) { ReachView() }
        .onChange(of: garage.selected) { _, bike in planner.bike = bike }
        .task { planner.bike = garage.selected }
        .sheet(isPresented: $searching) {
            PlaceSearchView(title: "Start from", near: location.coordinate) { planner.start = $0 }
        }
        .sheet(isPresented: $showingGarage) { GarageView() }
        .sheet(isPresented: $showingAbout) { AboutView() }
        .sheet(isPresented: $searchingDestination) {
            PlaceSearchView(title: "Go to", near: planner.start?.coordinate ?? location.coordinate) {
                planner.destination = $0
            }
        }
    }

    @ViewBuilder private var startRow: some View {
        switch (planner.start, location.state) {
        case let (start?, _):
            Label(start.name, systemImage: "mappin.circle.fill")
        case (nil, .locating):
            Label("Finding you…", systemImage: "location.circle")
        case (nil, .denied):
            Label("Location is off for Serpentine; search for a start instead.", systemImage: "location.slash")
                .foregroundStyle(.secondary)
        default:
            Label("No start chosen", systemImage: "mappin.slash").foregroundStyle(.secondary)
        }
    }

    @ViewBuilder private var destinationRow: some View {
        if let destination = planner.destination {
            Label(destination.name, systemImage: "flag.circle.fill")
        } else {
            Label("No destination chosen", systemImage: "flag.slash").foregroundStyle(.secondary)
        }
    }

    /// Says what was assumed, in the rider's terms: a steady rate at the plugs we'll look for.
    private var chargingFootnote: String {
        let bike = garage.selected
        let plugs = Set(bike.connectors + bike.adapters)
        var names: [String] = []
        if plugs.contains("J1772") { names.append("J1772") }
        if plugs.contains("TESLA") { names.append("Tesla destination") }
        if names.isEmpty { names = ["Level 2"] }
        var s = "Stops are planned at \(names.joined(separator: " and ")) chargers, assuming a steady "
            + String(format: "%.1f", bike.acKW) + " kW — real charging slows as the battery fills."
        if planner.reserveForBackup {
            s += " You'll arrive at each stop with enough charge to reach another one, in case it's dead or busy."
        }
        return s
    }

    private var planButtonTitle: String {
        switch planner.mode {
        case .loop: "Plan loop"
        case .outAndBack: "Plan out and back"
        case .pointToPoint: planner.directRoute ? "Get me there" : "Plan the way there"
        }
    }

    /// Carries the charge from the plan screen across, so a rider who already set it isn't asked
    /// twice — but it stays editable there, because this is the number everything else rests on.
    private func openReach() {
        reach.socPercent = planner.charging ? planner.socPercent : min(reach.socPercent, 20)
        showingReach = true
    }

    private var twistLabel: String {
        switch planner.twistiness {
        case ..<0.25: "Relaxed"
        case ..<0.65: "Twisty"
        default: "Very twisty"
        }
    }
}

/// MapKit place search for a start or destination.
struct PlaceSearchView: View {
    var title = "Start from"
    let near: CLLocationCoordinate2D?
    let choose: (StartPoint) -> Void
    @Environment(\.dismiss) private var dismiss
    @State private var query = ""
    @State private var results: [StartPoint] = []

    var body: some View {
        NavigationStack {
            List {
                Section {
                    ForEach(results) { place in
                        row(place)
                    }
                } footer: {
                    if near != nil && !results.isEmpty {
                        Text("Distances are straight-line, not riding distance.")
                    }
                }
            }
            .searchable(text: $query, placement: .navigationBarDrawer(displayMode: .always), prompt: "Town, address or place")
            .task(id: query) {
                guard query.count >= 3 else { results = []; return }
                try? await Task.sleep(for: .milliseconds(300)) // debounce typing
                results = await Planner.search(query, near: near)
            }
            .navigationTitle(title)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { Button("Cancel") { dismiss() } }
        }
    }

    @ViewBuilder private func row(_ place: StartPoint) -> some View {
        Button {
            choose(place)
            dismiss()
        } label: {
            HStack(alignment: .firstTextBaseline) {
                VStack(alignment: .leading, spacing: 2) {
                    Text(place.name)
                    if let address = place.address {
                        Text(address).font(.caption).foregroundStyle(.secondary)
                    }
                }
                Spacer(minLength: 12)
                if let near {
                    // As the crow flies, not as the road runs — say so, or a rider will read
                    // it as the ride length.
                    Text(Format.nearby(meters: place.metres(from: near)))
                        .font(.caption).foregroundStyle(.secondary).monospacedDigit()
                }
            }
        }
        .buttonStyle(.plain)
    }
}
