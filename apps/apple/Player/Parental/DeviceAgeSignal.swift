import Foundation
import MaktabaKit
#if os(iOS)
import UIKit
#if canImport(DeclaredAgeRange)
import DeclaredAgeRange
#endif
#if canImport(ManagedSettings)
import ManagedSettings
#endif
#endif

/// Where the Player reads what the device says about its user's age (Epic 57 story 57.04).
///
/// - **Declared Age Range** (`AgeRangeService`, iOS 26): asked with the gates 9, 13 and 18.
///   It needs the capability `com.apple.developer.declared-age-range`, which this project
///   does not have yet: until the App IDs get it the system answers "not available" and
///   nothing is sent, and the app builds and runs the same.
/// - **Screen Time's film and TV limits** (`ManagedSettingsStore.effectiveMaximumMovieRating`
///   and `effectiveMaximumTVShowRating`): read without asking for anything. Watching them
///   change needs Family Controls, which this app does not ask for, so they are read at
///   sign-in and each time the app comes to the front.
///
/// The Mac Player reads nothing (story 57.04 is the iPhone's and the iPad's).
@MainActor
enum DeviceAgeSignal {
    /// The launch's source: a Debug build's `-maktaba-age-signal 9-12:guardian_declared:400:400`
    /// (the UI tests' double: a simulator cannot be a Family Sharing child); a Debug build
    /// under the UI tests otherwise says nothing, so no system sheet comes up in a test that
    /// does not expect one; else the device.
    static func source(arguments: [String] = CommandLine.arguments) -> any AgeSignalSource {
        #if DEBUG
        if let spec = ShellLaunch.value(of: "-maktaba-age-signal", in: arguments), let fixed = FixedAgeSignal(spec: spec) {
            return fixed
        }
        if arguments.contains("-maktaba-secrets-in-file") { return FixedAgeSignal(.notAvailable) }
        #endif
        return AppleAgeSignal()
    }
}

/// Apple's two signals.
@MainActor
final class AppleAgeSignal: AgeSignalSource {
    var region: String? { Locale.current.region?.identifier }

    func ratingCeilings() -> (movie: Int?, tv: Int?) {
        #if os(iOS) && canImport(ManagedSettings)
        let store = ManagedSettingsStore()
        return (store.effectiveMaximumMovieRating, store.effectiveMaximumTVShowRating)
        #else
        return (nil, nil)
        #endif
    }

    func requestAgeRange() async -> AgeRangeAnswer {
        #if os(iOS) && canImport(DeclaredAgeRange)
        guard let presenter = Self.presenter() else { return .notAvailable }
        do {
            switch try await AgeRangeService.shared.requestAgeRange(ageGates: 9, 13, 18, in: presenter) {
            case .declinedSharing:
                return .declined
            case let .sharing(range):
                return .shared(lower: range.lowerBound, upper: range.upperBound,
                               declaration: range.ageRangeDeclaration.map(Self.name))
            @unknown default:
                return .notAvailable
            }
        } catch {
            // no capability in this build, no Apple Account, a region where it is not
            // offered: nothing to send. The account's own limit stands.
            Log.session.info("age range: not available (\(String(describing: error), privacy: .public))")
            return .notAvailable
        }
        #else
        return .notAvailable
        #endif
    }

    #if os(iOS) && canImport(DeclaredAgeRange)
    /// How the range was declared, in the snake case the home keeps. Apple's older cases
    /// that say how an age was checked all read as `confirmed`.
    static func name(_ d: AgeRangeService.AgeRangeDeclaration) -> String {
        switch d {
        case .selfDeclared: "self_declared"
        case .guardianDeclared: "guardian_declared"
        default: "confirmed"
        }
    }

    /// The view controller in front, for the system's sheet.
    static func presenter() -> UIViewController? {
        let scene = UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }
            .first { $0.activationState == .foregroundActive } ?? UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }.first
        var top = scene?.keyWindow?.rootViewController
        while let next = top?.presentedViewController { top = next }
        return top
    }
    #endif
}
