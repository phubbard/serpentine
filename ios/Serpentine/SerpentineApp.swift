import SwiftUI

@main
struct SerpentineApp: App {
    @State private var planner = Planner()
    @State private var location = LocationProvider()

    var body: some Scene {
        WindowGroup {
            NavigationStack {
                PlanView()
                    .navigationDestination(item: $planner.result) { plan in
                        ResultView(plan: plan)
                    }
            }
            .environment(planner)
            .environment(location)
        }
    }
}
