import CoreLocation
import SwiftUI

@main
struct SerpentineApp: App {
    @State private var planner = Planner()
    @State private var location = LocationProvider()
    @State private var garage = Garage()
    @State private var rides = RideStore()
    @State private var reach = ReachFinder()
    @State private var progress = RideProgress()
    @State private var showingAbout = false

    var body: some Scene {
        WindowGroup {
            RootView(showingAbout: $showingAbout)
                .environment(planner)
                .environment(location)
                .environment(garage)
                .environment(rides)
                .environment(reach)
                .environment(progress)
        }
        .commands {
            // Replace the stock panel: the default one is a version number, and the licences behind
            // the routing and charging data ask for more than that (ADR-032).
            CommandGroup(replacing: .appInfo) {
                Button("About Serpentine") { showingAbout = true }
            }
        }
    }
}

/// Plan form beside the ride. On iPad the map gets the big column; on iPhone the split view collapses
/// to a stack and a new result pushes the ride on top of the form, as before.
struct RootView: View {
    @Binding var showingAbout: Bool
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
        .sheet(isPresented: $showingAbout) { AboutView() }
        #if DEBUG
        // App Store screenshots: `simctl launch <udid> net.phfactor.serpentine -screenshotPlan` plans the
        // Ramona charging loop without driving the UI.
        .task {
            guard ProcessInfo.processInfo.arguments.contains("-screenshotPlan") else { return }
            planner.start = StartPoint(name: "Ramona, CA",
                                       coordinate: CLLocationCoordinate2D(latitude: 33.0417, longitude: -116.8681))
            planner.charging = true
            if ProcessInfo.processInfo.arguments.contains("-outAndBack") { planner.mode = .outAndBack }
            if ProcessInfo.processInfo.arguments.contains("-timeBudget") { planner.budget = .time }
            await planner.plan()
        }
        #endif
    }
}
