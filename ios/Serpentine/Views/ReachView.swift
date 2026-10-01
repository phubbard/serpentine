import CoreLocation
import SwiftUI

/// "I'm low — where can I get to?" (ADR-034). Sorted by the share of the pack each ride would take,
/// not by distance: a charger over a ridge can cost more than one twice as far on the flat, and the
/// rider deciding between them is doing it on a battery that won't take a second guess.
struct ReachView: View {
    @Environment(ReachFinder.self) private var finder
    @Environment(Garage.self) private var garage
    @Environment(LocationProvider.self) private var location
    @Environment(Planner.self) private var planner
    @Environment(\.dismiss) private var dismiss

    @State private var routed: PlanResult?
    @State private var searchTask: Task<Void, Never>?

    /// Where to search from: wherever the rider is, falling back to the start they already chose.
    private var origin: CLLocationCoordinate2D? {
        location.coordinate ?? planner.start?.coordinate
    }

    var body: some View {
        @Bindable var finder = finder
        NavigationStack {
            List {
                Section {
                    LabeledContent("Charge left", value: "\(Int(finder.socPercent)) %")
                        .font(.headline)
                    Slider(value: $finder.socPercent, in: 1...100, step: 1)
                        .tint(finder.socPercent <= 20 ? .orange : .accentColor)
                } footer: {
                    Text("Read it off the bike. Everything below is worked out from this number, so a guess here is a guess everywhere.")
                }

                if let error = finder.errorMessage {
                    Section { Text(error).foregroundStyle(.red) }
                }
                if let result = finder.result {
                    if let warning = result.warning {
                        Section {
                            Label(warning, systemImage: "exclamationmark.triangle.fill")
                                .foregroundStyle(.orange)
                        }
                    }
                    Section {
                        ForEach(result.options) { option in
                            Button { open(option) } label: { row(option) }
                                .buttonStyle(.plain)
                                .disabled(finder.routing != nil)
                        }
                    } header: {
                        Text(result.options.isEmpty ? "Nothing in reach" : "Cheapest to reach first")
                    } footer: {
                        Text(footnote(result))
                    }
                }
            }
            .navigationTitle("Find a charger")
            #if !targetEnvironment(macCatalyst)
            .navigationBarTitleDisplayMode(.inline)
            #endif
            .toolbar {
                ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } }
            }
            .overlay {
                if finder.isSearching && finder.result == nil {
                    ProgressView("Working out what you can reach")
                } else if origin == nil && !finder.isSearching {
                    ContentUnavailableView("Where are you?", systemImage: "location.slash",
                                           description: Text("This needs your location to work out what's in reach. Allow location access, or pick a start on the plan screen."))
                }
            }
            .navigationDestination(item: $routed) { plan in
                ResultView(plan: plan, canReplan: false)
            }
            // Keyed on the charge *and* where we are: the first fix usually lands after this view
            // appears, and keying on the slider alone would leave the list empty until it moved.
            // Moving the slider re-queries, but not on every tick — each search is a fan-out of
            // routes on the server.
            .task(id: searchKey) {
                guard let origin else { return }
                try? await Task.sleep(for: .milliseconds(400))
                await finder.search(from: origin, bike: garage.selected)
            }
            .task {
                if location.coordinate == nil { await location.locate() }
            }
        }
    }

    /// Changes whenever the answer would change: the rider's charge, or roughly where they are.
    private var searchKey: String {
        guard let origin else { return "waiting" }
        return "\(Int(finder.socPercent)):\(Int(origin.latitude * 1000)):\(Int(origin.longitude * 1000))"
    }

    private func open(_ option: ReachOption) {
        guard let origin else { return }
        searchTask?.cancel()
        searchTask = Task {
            if let plan = await finder.route(to: option, from: origin, bike: garage.selected) {
                routed = plan
            }
        }
    }

    private func row(_ option: ReachOption) -> some View {
        HStack(alignment: .top, spacing: 12) {
            VStack(alignment: .leading, spacing: 3) {
                Text(option.name).font(.headline)
                Text(hardware(option)).font(.subheadline).foregroundStyle(.secondary)
                Text(journey(option)).font(.caption).foregroundStyle(.secondary)
                if let note = option.reliability?.note {
                    Label(note, systemImage: "exclamationmark.bubble")
                        .font(.caption).foregroundStyle(.orange)
                }
            }
            Spacer(minLength: 8)
            VStack(alignment: .trailing, spacing: 2) {
                Text(Format.packShare(option.socNeeded))
                    .font(.title3.bold()).monospacedDigit()
                    .foregroundStyle(option.reachable ? .primary : Color.red)
                Text(option.reachable ? "of your charge" : "more than you have")
                    .font(.caption2).foregroundStyle(.secondary)
                    .multilineTextAlignment(.trailing)
                if option.reachable {
                    Text("arrive \(Format.percent(option.socArrivalEst))")
                        .font(.caption2)
                        .foregroundStyle(option.socArrivalEst < 0.05 ? Color.orange : .secondary)
                }
            }
            .frame(maxWidth: 110, alignment: .trailing)
        }
        .padding(.vertical, 4)
        .contentShape(Rectangle())
        .opacity(option.reachable ? 1 : 0.65)
        .overlay(alignment: .trailing) {
            if finder.routing == option { ProgressView() }
        }
    }

    private func hardware(_ o: ReachOption) -> String {
        var parts = ["\(o.ports) port\(o.ports == 1 ? "" : "s")"]
        if o.powerKw > 0 { parts.append(String(format: "%.1f kW", o.powerKw)) }
        if !o.network.isEmpty { parts.append(o.network) }
        return parts.joined(separator: " · ")
    }

    /// Road distance, not the straight line: the number that decides whether the bike gets there.
    private func journey(_ o: ReachOption) -> String {
        var s = "\(Format.distance(km: o.distanceM / 1000)) by road"
        if o.ascendM >= 30 { s += " · \(Format.climb(meters: o.ascendM)) up" }
        s += " · \(Format.duration(seconds: o.timeS))"
        return s
    }

    private func footnote(_ r: ReachResult) -> String {
        if r.options.isEmpty {
            return "Searched \(Format.distance(km: r.rangeKmEst)) around you, which is as far as \(Int(finder.socPercent)) % could take this bike at best."
        }
        return "\(r.nearby) public charger\(r.nearby == 1 ? "" : "s") your bike can use within \(Format.distance(km: r.rangeKmEst)) — the best of each area is listed. "
            + "Estimates are deliberately cautious and assume the ride there is gentle."
    }
}
