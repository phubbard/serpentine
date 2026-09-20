import CoreLocation
import SwiftUI

/// Where, what kind of ride, how far, how twisty, and whether to plan charging.
struct PlanView: View {
    @Environment(Planner.self) private var planner
    @Environment(LocationProvider.self) private var location
    @State private var searching = false
    @State private var searchingDestination = false

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
                    LabeledContent("Extra time", value: "+\(Int(planner.maxExtraMin)) min")
                    Slider(value: $planner.maxExtraMin, in: 0...60, step: 5)
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
                    Text("The quickest way, plus up to the extra time you allow spent on better roads.")
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
                Text("Zero SR/S")
            } footer: {
                Text(planner.charging && planner.reserveForBackup
                     ? "Stops are planned at J1772 and Tesla destination chargers, charging at 6.6 kW. You'll arrive at each stop with enough charge to reach another one, in case it's dead or busy."
                     : "Stops are planned at J1772 and Tesla destination chargers, charging at 6.6 kW.")
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
            }
        }
        .navigationTitle("Serpentine")
        .sheet(isPresented: $searching) {
            PlaceSearchView(title: "Start from", near: location.coordinate) { planner.start = $0 }
        }
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

    private var planButtonTitle: String {
        switch planner.mode {
        case .loop: "Plan loop"
        case .outAndBack: "Plan out and back"
        case .pointToPoint: "Plan the way there"
        }
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
            List(results, id: \.name) { place in
                Button(place.name) {
                    choose(place)
                    dismiss()
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
}
