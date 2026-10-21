import SwiftUI

/// What the three GPX exports do, and the two things about a Beeline that will otherwise surprise
/// a rider mid-ride (ADR-039).
struct GPXHelpView: View {
    /// Exactly what the share menu is offering, in menu order. Passed in rather than assumed,
    /// because a ride the battery covers outright has no charge stops and so no waypoints file —
    /// an explainer listing three choices beside a menu of two reads as a bug, and was reported as
    /// one from TestFlight build 99.
    let kinds: [GPXKind]
    /// How many charge stops this ride has, for the untested-territory warning.
    let stops: Int

    @Environment(\.dismiss) private var dismiss

    /// There is no published waypoint cap on the device. Past about this many we say so rather
    /// than imply we know it works.
    private let untestedAbove = 25

    /// Small counts read better as words in a heading, and the count is whatever the menu offers.
    private var sectionTitle: String {
        guard kinds.count != 1 else { return "One file" }
        let words = ["no", "one", "two", "three"]
        let n: String = kinds.count < words.count ? words[kinds.count] : String(kinds.count)
        return n + " files, " + n + " behaviours"
    }

    /// Charge stops is the pick when the ride has any; otherwise it isn't on screen to recommend.
    private var recommended: GPXKind { kinds.contains(.waypoints) ? .waypoints : .track }

    private var pickingAdvice: String {
        "A device picks what to do from what the file contains, not from what you meant, so each "
            + "one holds a single kind of thing. If you have a Beeline, start with "
            + recommended.title + "."
    }

    private var noWaypointsReason: String {
        "There's no " + GPXKind.waypoints.title + " file for this ride: the battery covers it "
            + "without stopping, so there would be nothing in it."
    }

    var body: some View {
        NavigationStack {
            List {
                Section {
                    ForEach(kinds) { kind in
                        VStack(alignment: .leading, spacing: 3) {
                            Label(kind.title, systemImage: kind.icon).font(.headline)
                            Text(kind.effect).font(.subheadline).foregroundStyle(.secondary)
                        }
                        .padding(.vertical, 2)
                    }
                    if !kinds.contains(.waypoints) {
                        Label {
                            Text(noWaypointsReason)
                        } icon: {
                            Image(systemName: GPXKind.waypoints.icon).foregroundStyle(.secondary)
                        }
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                    }
                } header: {
                    Text(sectionTitle)
                } footer: {
                    Text(pickingAdvice)
                }

                if stops > 0 {
                    Section {
                        Label {
                            Text("A Beeline's arrival time leaves charging out, so it will always "
                                 + "read earlier than ours. The time on the ride screen includes "
                                 + "every charge stop; each stop also carries its own wait.")
                        } icon: {
                            Image(systemName: "clock.badge.exclamationmark").foregroundStyle(.orange)
                        }
                        Label {
                            Text("If you leave the route, the device re-plans on its own and knows "
                                 + "nothing about charging — the charge plan is gone. Pause the "
                                 + "ride here, then carry on, and you'll get a fresh one from "
                                 + "wherever you are.")
                        } icon: {
                            Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.orange)
                        }
                    } header: {
                        Text("Two things that will catch you out")
                    }
                }

                if stops > untestedAbove {
                    Section {
                        Label("This ride has \(stops) charge stops. Devices aren't documented to "
                              + "take that many, and we haven't tested it. If the import looks "
                              + "wrong, try Route outline instead.",
                              systemImage: "questionmark.circle")
                            .foregroundStyle(.orange)
                    }
                }

                Section {
                    Text("Charge stops carry their name, wait and arrival charge, so the plan is "
                         + "readable on the device itself. Addresses come along too.")
                } header: {
                    Text("What's in the file")
                } footer: {
                    Text("Older Beeline apps show no import mode picker at all — you need iOS "
                         + "3.4.2295 or Android 4.2.5277 and later. Other apps that read GPX, like "
                         + "Kurviger or a Garmin, generally want Exact path.")
                }
            }
            .navigationTitle("Sharing a ride")
            #if !targetEnvironment(macCatalyst)
            .navigationBarTitleDisplayMode(.inline)
            #endif
            .toolbar {
                ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } }
            }
        }
    }
}
