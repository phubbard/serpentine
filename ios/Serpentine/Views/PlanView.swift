import CoreLocation
import SwiftUI

/// Where, what kind of ride, how far, how twisty, and whether to plan charging.
struct PlanView: View {
    @Environment(Planner.self) private var planner
    @Environment(LocationProvider.self) private var location
    @State private var searching = false

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

            Section("Ride") {
                Picker("Ride", selection: $planner.mode) {
                    Text("Loop").tag(RideMode.loop)
                    Text("Out and back").tag(RideMode.outAndBack)
                }
                .pickerStyle(.segmented)
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
                LabeledContent("Twistiness", value: twistLabel)
                Slider(value: $planner.twistiness, in: 0...1, step: 0.1)
                Picker("Head", selection: $planner.heading) {
                    Text("Any direction").tag(Heading?.none)
                    ForEach(Heading.allCases) { Text($0.label).tag(Heading?.some($0)) }
                }
            }

            Section {
                Toggle("Plan charging", isOn: $planner.charging).tint(.accentColor)
                if planner.charging {
                    LabeledContent("Starting charge", value: "\(Int(planner.socPercent)) %")
                    Slider(value: $planner.socPercent, in: 20...100, step: 5)
                }
            } header: {
                Text("Zero SR/S")
            } footer: {
                Text("Stops are planned at J1772 and Tesla destination chargers, charging at 6.6 kW.")
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
                .disabled(planner.start == nil || planner.isPlanning)
                if let error = planner.errorMessage {
                    Text(error).foregroundStyle(.red)
                }
            }
        }
        .navigationTitle("Serpentine")
        .sheet(isPresented: $searching) {
            PlaceSearchView(near: location.coordinate) { planner.start = $0 }
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

    private var planButtonTitle: String {
        planner.mode == .loop ? "Plan loop" : "Plan out and back"
    }

    private var twistLabel: String {
        switch planner.twistiness {
        case ..<0.25: "Relaxed"
        case ..<0.65: "Twisty"
        default: "Very twisty"
        }
    }
}

/// MapKit place search for the start point.
struct PlaceSearchView: View {
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
            .navigationTitle("Start from")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { Button("Cancel") { dismiss() } }
        }
    }
}
