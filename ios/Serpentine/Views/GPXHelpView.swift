import SwiftUI

/// What to answer when a GPS asks how to import the file, and the things about riding a planned
/// route on a separate device that will otherwise catch a rider out mid-ride (ADR-039, ADR-041).
struct GPXHelpView: View {
    /// How many charge stops this ride has. Decides whether the charging warnings apply at all.
    let stops: Int

    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            List {
                Section {
                    Label {
                        VStack(alignment: .leading, spacing: 3) {
                            Text("Turn-by-turn").font(.headline)
                            Text("Follows the roads in the ride and gives you directions. This is "
                                 + "the one you want, and it's already selected.")
                                .font(.subheadline).foregroundStyle(.secondary)
                        }
                    } icon: {
                        Image(systemName: "checkmark.circle.fill").foregroundStyle(.green)
                    }
                    .padding(.vertical, 2)

                    VStack(alignment: .leading, spacing: 3) {
                        Text("Breadcrumb").font(.subheadline.bold())
                        Text("Draws the line to follow, with no directions. For trails and dirt.")
                            .font(.footnote).foregroundStyle(.secondary)
                    }
                    VStack(alignment: .leading, spacing: 3) {
                        Text("Waypoints only").font(.subheadline.bold())
                        Text("Throws the roads away and keeps the stops, for building your own ride "
                             + "on the device.")
                            .font(.footnote).foregroundStyle(.secondary)
                    }
                } header: {
                    Text("A Beeline will ask. Say Turn-by-turn")
                } footer: {
                    Text("One file covers all three — the device works out what it can offer from "
                         + "what's inside, so there's nothing to choose before you send it.")
                }

                if stops > 0 {
                    Section {
                        Label {
                            Text("Your charge stops come across as waypoints, so the device counts "
                                 + "them down and routes you to each one. How much of the detail "
                                 + "it shows varies, so keep the plan on your phone as well.")
                        } icon: {
                            Image(systemName: "bolt.fill").foregroundStyle(.green)
                        }
                        Label {
                            Text("The device's arrival time counts the riding and not the "
                                 + "charging, so it reads earlier than ours. The time on the ride "
                                 + "screen includes every charge stop.")
                        } icon: {
                            Image(systemName: "clock.badge.exclamationmark").foregroundStyle(.orange)
                        }
                        Label {
                            Text("If you leave the route the device re-plans on its own and knows "
                                 + "nothing about charging. Pause the ride here, then carry on, "
                                 + "and you'll get a fresh plan from wherever you are.")
                        } icon: {
                            Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.orange)
                        }
                    } header: {
                        Text("Charging on a separate device")
                    }
                }

                Section {
                    Text("A GPS may say turn directions are unavailable for a short stretch. That's "
                         + "usually a car park or a private road it can't route through — the line "
                         + "is still drawn and the ride is unaffected.")
                } header: {
                    Text("If it warns about missing directions")
                } footer: {
                    Text("Older Beeline apps don't offer the import choice at all — you need iOS "
                         + "3.4.2295 or Android 4.2.5277 and later. Kurviger, Calimoto and Garmin "
                         + "read the same file and need no special handling.")
                }
            }
            .navigationTitle("Sending a ride")
            #if !targetEnvironment(macCatalyst)
            .navigationBarTitleDisplayMode(.inline)
            #endif
            .toolbar {
                ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } }
            }
        }
    }
}
