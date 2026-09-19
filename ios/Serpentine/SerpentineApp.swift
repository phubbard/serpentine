import CoreLocation
import SwiftUI

@main
struct SerpentineApp: App {
    @State private var planner = Planner()
    @State private var location = LocationProvider()

    var body: some Scene {
        WindowGroup {
            RootView()
                .environment(planner)
                .environment(location)
        }
    }
}

/// Plan form beside the ride. On iPad the map gets the big column; on iPhone the split view collapses
/// to a stack and a new result pushes the ride on top of the form, as before.
struct RootView: View {
    @Environment(Planner.self) private var planner
    @State private var compactColumn = NavigationSplitViewColumn.sidebar

    var body: some View {
        NavigationSplitView(preferredCompactColumn: $compactColumn) {
            PlanView()
                .navigationSplitViewColumnWidth(min: 320, ideal: 380, max: 440)
        } detail: {
            if let plan = planner.result {
                NavigationStack { ResultView(plan: plan) }
            } else {
                ContentUnavailableView("Plan a ride",
                                       systemImage: "point.topleft.down.to.point.bottomright.curvepath",
                                       description: Text("Pick a start, a distance and a ride type, then tap Plan."))
            }
        }
        .onChange(of: planner.result?.id) { _, id in
            if id != nil { compactColumn = .detail }
        }
        #if DEBUG
        // App Store screenshots: `simctl launch <udid> net.phfactor.serpentine -screenshotPlan` plans the
        // Ramona charging loop without driving the UI.
        .task {
            guard ProcessInfo.processInfo.arguments.contains("-screenshotPlan") else { return }
            planner.start = StartPoint(name: "Ramona, CA",
                                       coordinate: CLLocationCoordinate2D(latitude: 33.0417, longitude: -116.8681))
            planner.charging = true
            if ProcessInfo.processInfo.arguments.contains("-outAndBack") { planner.mode = .outAndBack }
            await planner.plan()
        }
        #endif
    }
}
