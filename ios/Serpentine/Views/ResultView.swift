import MapKit
import SwiftUI

/// The planned ride: map, numbers, charge stops, and the hand-off to Apple Maps for voice guidance.
struct ResultView: View {
    let plan: PlanResult
    @Environment(Planner.self) private var planner
    @Environment(\.openURL) private var openURL
    @Environment(\.horizontalSizeClass) private var sizeClass
    @State private var gpxFile: URL?

    private var coordinates: [CLLocationCoordinate2D] { plan.polyline.map(\.coordinate) }
    private var stops: [Charger] { (plan.chargers ?? []).filter(\.stop) }

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
                    stat(Format.duration(seconds: plan.energy?.totalTimeS ?? plan.timeS),
                         (plan.energy?.stops ?? 0) > 0 ? "incl. charging" : "riding")
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
        .navigationTitle(plan.mode == "loop" ? "Loop" : "Out and back")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            Button {
                Task { await planner.plan(another: true) }
            } label: {
                if planner.isPlanning { ProgressView() } else { Label("Another", systemImage: "arrow.triangle.2.circlepath") }
            }
            .disabled(planner.isPlanning)
        }
        .task(id: plan.id) { gpxFile = await planner.gpxFile(for: plan) }
    }

    private func stat(_ value: String, _ label: String) -> some View {
        VStack {
            Text(value).font(.title3.bold()).monospacedDigit()
            Text(label).font(.caption).foregroundStyle(.secondary)
        }
        .frame(maxWidth: .infinity)
    }

    private var footnote: String {
        let curvy = Format.distance(km: plan.stats.curvyKm)
        var s = "\(curvy) of properly twisty road. Apple Maps gets \(plan.handoff.waypoints.count) stops so it follows this route."
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
