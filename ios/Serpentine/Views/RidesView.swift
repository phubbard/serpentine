import SwiftUI

/// The rides this phone has kept: ones already ridden, and ones saved to ride later. Opening one
/// puts it back on the map exactly as it was planned — no network needed (ADR-033).
struct RidesView: View {
    @Environment(RideStore.self) private var store
    @Environment(Planner.self) private var planner
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            List {
                ForEach(store.rides) { ride in
                    Button { open(ride) } label: { row(ride) }
                        .buttonStyle(.plain)
                }
                .onDelete { store.remove(atOffsets: $0) }
            }
            .overlay {
                if store.rides.isEmpty {
                    ContentUnavailableView("No saved rides", systemImage: "bookmark",
                                           description: Text("Plan a ride, then tap Save to keep it here — to rate afterwards, or to ride another day."))
                }
            }
            .navigationTitle("Saved rides")
            #if !targetEnvironment(macCatalyst)
            .navigationBarTitleDisplayMode(.inline)
            #endif
            .toolbar {
                ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } }
            }
            .alert("Saved rides", isPresented: .constant(store.errorMessage != nil)) {
                Button("OK") { store.clearError() }
            } message: {
                Text(store.errorMessage ?? "")
            }
        }
    }

    private func open(_ ride: SavedRide) {
        guard let plan = store.plan(for: ride) else { return } // store reports why
        planner.result = plan
        dismiss()
    }

    private func row(_ ride: SavedRide) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(alignment: .firstTextBaseline) {
                Text(ride.title)
                Spacer(minLength: 12)
                if ride.rating > 0 {
                    Text(String(repeating: "★", count: ride.rating))
                        .font(.caption).foregroundStyle(.tint)
                }
            }
            Text(summary(ride)).font(.caption).foregroundStyle(.secondary)
            if !ride.topRoads.isEmpty {
                Text(ride.topRoads.joined(separator: " · "))
                    .font(.caption).foregroundStyle(.secondary).lineLimit(1)
            }
            if !ride.notes.isEmpty {
                Text(ride.notes).font(.caption).italic().foregroundStyle(.secondary).lineLimit(2)
            }
        }
        .padding(.vertical, 2)
        .contentShape(Rectangle())
    }

    /// The line under the title: what the ride is, and when it last mattered.
    private func summary(_ ride: SavedRide) -> String {
        var parts = [ride.modeLabel,
                     Format.distance(km: ride.distanceM / 1000),
                     Format.duration(seconds: ride.timeS)]
        if ride.hasCharging { parts.append("charging") }
        if let ridden = ride.lastRiddenAt {
            parts.append("ridden \(ridden.formatted(date: .abbreviated, time: .omitted))")
        } else {
            parts.append("not yet ridden")
        }
        return parts.joined(separator: " · ")
    }
}

/// Rating, notes and when it was last ridden — shown on a ride that has been saved. Edits are
/// written back as they happen; there is no Save button to forget to press.
struct SavedRideEditor: View {
    @Environment(RideStore.self) private var store
    @State private var ride: SavedRide

    init(ride: SavedRide) {
        _ride = State(initialValue: ride)
    }

    var body: some View {
        Section {
            StarRating(rating: $ride.rating)
            TextField("Notes — road surface, where to stop, who to bring",
                      text: $ride.notes, axis: .vertical)
                .lineLimit(2...6)
            if let ridden = ride.lastRiddenAt {
                DatePicker("Last ridden",
                           selection: Binding(get: { ridden }, set: { ride.lastRiddenAt = $0 }),
                           in: ...Date.now,
                           displayedComponents: .date)
                Button("Not ridden yet", systemImage: "arrow.uturn.backward") {
                    ride.lastRiddenAt = nil
                }
                .font(.footnote)
            } else {
                Button("I rode this today", systemImage: "checkmark.circle") {
                    ride.lastRiddenAt = .now
                }
            }
        } header: {
            Text("Your notes")
        } footer: {
            Text("Kept on this phone only. Saved rides are never sent anywhere.")
        }
        // Every edit is a write, but only of the small index file — the plan itself never changes.
        .onChange(of: ride) { _, new in store.update(new) }
    }
}

/// Five taps, no dependencies. Tapping the star you already chose clears the rating, because a
/// mis-tap otherwise leaves a permanent opinion.
struct StarRating: View {
    @Binding var rating: Int

    var body: some View {
        HStack(spacing: 8) {
            ForEach(1...5, id: \.self) { n in
                Image(systemName: n <= rating ? "star.fill" : "star")
                    .font(.title3)
                    .foregroundStyle(n <= rating ? AnyShapeStyle(.tint) : AnyShapeStyle(.secondary))
                    .contentShape(Rectangle())
                    .onTapGesture { rating = (rating == n) ? 0 : n }
                    .accessibilityLabel("\(n) star\(n == 1 ? "" : "s")")
                    .accessibilityAddTraits(n == rating ? [.isSelected, .isButton] : .isButton)
            }
            Spacer()
            if rating == 0 {
                Text("Not rated").font(.caption).foregroundStyle(.secondary)
            }
        }
        .accessibilityElement(children: .contain)
        .accessibilityLabel("Rating")
    }
}
