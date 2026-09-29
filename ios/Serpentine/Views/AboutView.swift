import SwiftUI

/// About Serpentine (ADR-032). More than a version number: the routing comes from OpenStreetMap and
/// the charging data from the DOE and Open Charge Map, and those licences ask for attribution. This
/// is where a rider — or a reviewer — can see what the app is built on and where their data goes.
struct AboutView: View {
    @Environment(\.dismiss) private var dismiss
    @Environment(\.openURL) private var openURL

    private var version: String {
        let info = Bundle.main.infoDictionary
        let short = info?["CFBundleShortVersionString"] as? String ?? "?"
        let build = info?["CFBundleVersion"] as? String ?? "?"
        return "Version \(short) (\(build))"
    }

    /// When this build was made and from which commit — stamped at build time, not read from file
    /// dates, which Apple's re-signing would rewrite. Shown so a tester reporting a bug can say
    /// exactly what they are running.
    private var built: String? {
        let info = Bundle.main.infoDictionary
        let stamp = info?["SerpentineBuildDate"] as? String ?? ""
        let sha = info?["SerpentineGitSHA"] as? String ?? ""
        guard !stamp.isEmpty, stamp != "unknown" else { return nil }

        var when = stamp
        let iso = DateFormatter()
        iso.dateFormat = "yyyy-MM-dd'T'HH:mm'Z'"
        iso.timeZone = TimeZone(identifier: "UTC")
        if let date = iso.date(from: stamp) {
            when = date.formatted(date: .abbreviated, time: .shortened) // the rider's own clock
        }
        return sha.isEmpty || sha == "unknown" ? "Built \(when)" : "Built \(when) · \(sha)"
    }

    var body: some View {
        NavigationStack {
            List {
                Section {
                    VStack(alignment: .leading, spacing: 6) {
                        Text("Serpentine").font(.largeTitle.bold())
                        Text("Curvy rides, planned.").foregroundStyle(.secondary)
                        Text(version).font(.footnote).foregroundStyle(.secondary).monospacedDigit()
                        if let built {
                            Text(built).font(.footnote).foregroundStyle(.secondary).monospacedDigit()
                                .textSelection(.enabled) // so it can be pasted into a bug report
                        }
                    }
                    .padding(.vertical, 4)
                }

                Section("What it does") {
                    Text("""
                    Finds loops and out-and-backs on curvy, quiet roads, plans charge stops for \
                    electric bikes, and hands the ride to Apple Maps for voice guidance. When it \
                    isn't a ride, "just get me there" takes the fast way and still plans the charging.
                    """)
                }

                Section {
                    Text("""
                    Planning happens on serpentine.phfactor.net and nowhere else. No account, no \
                    tracking, no ads. Your start point is used to plan the ride and isn't kept.
                    """)
                    link("Privacy policy", "https://serpentine.phfactor.net/v1/privacy")
                    link("Support", "https://serpentine.phfactor.net/v1/support")
                    link("serpentine.phfactor.net", "https://serpentine.phfactor.net/")
                } header: {
                    Text("Privacy")
                }

                // Attribution is a licence condition, not a courtesy: OpenStreetMap is ODbL and Open
                // Charge Map is CC BY-SA. Keep these accurate if a data source ever changes.
                Section {
                    credit("Maps, search and navigation", "Apple Maps")
                    credit("Roads and routing", "© OpenStreetMap contributors, ODbL")
                    credit("Elevation", "NASA SRTM")
                    credit("Charger locations", "U.S. Department of Energy, Alternative Fuels Data Center")
                    credit("Charger status", "Open Charge Map contributors, CC BY-SA 4.0")
                    link("openstreetmap.org/copyright", "https://www.openstreetmap.org/copyright")
                    link("openchargemap.org", "https://openchargemap.org/")
                } header: {
                    Text("Built on")
                } footer: {
                    Text("Charging information can be out of date. Check a charger before you depend on it.")
                }

                Section {
                    Text("© 2026 Paul Hubbard").foregroundStyle(.secondary)
                    link("pfh@phfactor.net", "mailto:pfh@phfactor.net")
                }
            }
            .navigationTitle("About")
            #if !targetEnvironment(macCatalyst)
            .navigationBarTitleDisplayMode(.inline)
            #endif
            .toolbar {
                ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } }
            }
        }
    }

    private func credit(_ what: String, _ who: String) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(what).font(.subheadline)
            Text(who).font(.caption).foregroundStyle(.secondary)
        }
    }

    private func link(_ label: String, _ url: String) -> some View {
        Button(label) {
            if let u = URL(string: url) { openURL(u) }
        }
    }
}
