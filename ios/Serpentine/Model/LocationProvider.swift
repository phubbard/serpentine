import CoreLocation
import Observation

/// One-shot "where am I" for the start point. iOS 18 service sessions handle the when-in-use prompt.
@MainActor @Observable
final class LocationProvider {
    enum State: Equatable {
        case idle, locating, denied, failed
        case located(CLLocationCoordinate2D)

        static func == (a: State, b: State) -> Bool {
            switch (a, b) {
            case (.idle, .idle), (.locating, .locating), (.denied, .denied), (.failed, .failed): true
            case let (.located(x), .located(y)): x.latitude == y.latitude && x.longitude == y.longitude
            default: false
            }
        }
    }

    private(set) var state: State = .idle

    var coordinate: CLLocationCoordinate2D? {
        if case let .located(c) = state { return c }
        return nil
    }

    func locate() async {
        state = .locating
        let session = CLServiceSession(authorization: .whenInUse)
        defer { session.invalidate() }
        do {
            for try await update in CLLocationUpdate.liveUpdates() {
                if update.authorizationDenied || update.authorizationDeniedGlobally {
                    state = .denied
                    return
                }
                if let loc = update.location, loc.horizontalAccuracy < 200 {
                    state = .located(loc.coordinate)
                    return
                }
            }
        } catch {
            state = .failed
        }
    }
}
