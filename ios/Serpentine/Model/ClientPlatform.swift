import Foundation
#if canImport(UIKit)
import UIKit
#endif

/// Which kind of device this build is running on, sent as `X-Serpentine-Platform` so the ops
/// dashboard can count iPhone against iPad against Mac against Android.
///
/// A header rather than a request field on purpose: the plan cache key is a hash of the whole
/// request body, so putting the platform in there would split the cache four ways for rides that
/// are otherwise identical. It is a count per platform and never per device — the server
/// allowlists the value and keeps only a tally (ADR-026).
enum ClientPlatform {
    /// Resolved once at launch from the main actor, then read from whichever thread is making the
    /// request. `UIDevice` is main-actor isolated, so it cannot be read lazily from the API layer.
    nonisolated(unsafe) static var name = "ios"

    @MainActor static func detect() -> String {
        #if targetEnvironment(macCatalyst)
        return "mac"
        #elseif canImport(UIKit)
        return UIDevice.current.userInterfaceIdiom == .pad ? "ipad" : "ios"
        #else
        return "mac"
        #endif
    }
}
