import Foundation
import MaktabaKit
#if canImport(ManagedSettings)
import ManagedSettings
#endif

/// Where the Apple TV reads what the device says about its user's age (Epic 57 stories 57.04
/// and 57.11): Screen Time's film and TV limits, which tvOS 26 lets an app read without asking
/// for anything (`ManagedSettingsStore.effectiveMaximumMovieRating` and
/// `effectiveMaximumTVShowRating`). tvOS has no Declared Age Range, so no age range is ever
/// sent from a TV. Watching the limits change needs Family Controls, which this app does not
/// ask for: they are read at sign-in and each time the app comes to the front. The home uses
/// what is sent only to tighten.
@MainActor
enum TVAgeSignal {
    /// The launch's source: a Debug build's `-maktaba-age-signal none::1000:400` (the UI
    /// tests' double: range, declaration, film ceiling, TV ceiling), or
    /// `-maktaba-age-signal device` for the system's own; a Debug build under the UI tests
    /// otherwise says nothing; else the device.
    static func source(arguments: [String] = CommandLine.arguments) -> any AgeSignalSource {
        #if DEBUG
        if let i = arguments.firstIndex(of: "-maktaba-age-signal"), i + 1 < arguments.count {
            let spec = arguments[i + 1]
            if spec == "device" { return AppleTVAgeSignal() }
            if let fixed = FixedAgeSignal(spec: spec) { return fixed }
        }
        if arguments.contains("-uitest-reset") { return FixedAgeSignal(.notAvailable) }
        #endif
        return AppleTVAgeSignal()
    }
}

/// The Apple TV's one signal: the Screen Time limits.
@MainActor
final class AppleTVAgeSignal: AgeSignalSource {
    var region: String? { Locale.current.region?.identifier }

    /// tvOS has no Declared Age Range.
    func requestAgeRange() async -> AgeRangeAnswer { .notAvailable }

    func ratingCeilings() -> (movie: Int?, tv: Int?) {
        #if canImport(ManagedSettings)
        let store = ManagedSettingsStore()
        return (store.effectiveMaximumMovieRating, store.effectiveMaximumTVShowRating)
        #else
        return (nil, nil)
        #endif
    }
}
