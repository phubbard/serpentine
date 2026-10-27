import SwiftUI

/// Roads worth knowing (ADR-043). Pick one and it plans — no start, no distance, no settings.
///
/// This is the answer to "show me what it does". The product page names these roads; before this
/// existed a rider could read about US 129 and then find no way to ask for it.
struct DemoRidesView: View {
    let rides: [DemoRide]
    /// Builds and runs the plan. Owned by the caller so the work outlives this sheet — the ride is
    /// planned while the sheet is closing, and the result screen is what you land on.
    let plan: (DemoRide) -> Void

    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            List {
                Section {
                    ForEach(rides) { ride in
                        Button {
                            plan(ride)
                            dismiss()
                        } label: {
                            VStack(alignment: .leading, spacing: 3) {
                                Text(ride.name).font(.headline)
                                Text(ride.blurb).font(.subheadline).foregroundStyle(.secondary)
                                Text([ride.road, ride.region].compactMap { $0 }.joined(separator: " · "))
                                    .font(.caption).foregroundStyle(.tertiary)
                            }
                            .padding(.vertical, 3)
                        }
                        .buttonStyle(.plain)
                    }
                } footer: {
                    Text("These are real roads, planned the same way as any other ride — so you can "
                         + "see what Serpentine does before you point it at your own. The ride "
                         + "starts where the road does, not where you are.")
                }
            }
            .navigationTitle("Roads worth knowing")
            #if !targetEnvironment(macCatalyst)
            .navigationBarTitleDisplayMode(.inline)
            #endif
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
            }
        }
    }
}
