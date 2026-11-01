import Foundation

/// The web's placeholder rule (`useDelayedFlag`, PLACEHOLDER_DELAY_MS): a loading placeholder
/// shows only once a load has taken 0.4 s, so a quick one never flashes it.
enum PlaceholderTiming {
    static let delay: Duration = .milliseconds(400)

    /// Whether a load that has run for `elapsed` shows its placeholder yet.
    static func shows(after elapsed: Duration) -> Bool {
        elapsed >= delay
    }
}
