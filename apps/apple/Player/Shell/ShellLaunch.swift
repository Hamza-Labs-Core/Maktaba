import Foundation

/// The launch arguments the UI tests start the Player with. Read only in a Debug build:
/// a TestFlight or App Store build (Release) ignores them.
/// - `-maktaba-sections all` or `home,search`: the sections shown (`PlayerSection.visible`);
/// - `-maktaba-section live`: the section chosen at launch;
/// - `-maktaba-open video/v1`: a route pushed on that section's stack at launch, so a
///   screen's test opens it straight away (`PlayerRoute(path:)`);
/// - `-maktaba-live guide`: the Live TV screen's view at launch (`now` or `guide`), so a test
///   of the guide opens on it without a tap on the Now | Guide switch first;
/// - `-maktaba-profile sessions`: a Profile page shown at launch in the first screen's place
///   (`ProfilePage.id`), so a test of the page itself (the accessibility audit) opens on it
///   without a tap on its row first (the row's tap has tests of its own).
enum ShellLaunch {
    static func value(of flag: String, in arguments: [String]) -> String? {
        guard let i = arguments.firstIndex(of: flag), i + 1 < arguments.count else { return nil }
        return arguments[i + 1]
    }

    static func section(arguments: [String] = CommandLine.arguments) -> PlayerSection? {
        #if DEBUG
        value(of: "-maktaba-section", in: arguments).flatMap(PlayerSection.init(rawValue:))
        #else
        nil
        #endif
    }

    static func route(arguments: [String] = CommandLine.arguments) -> PlayerRoute? {
        #if DEBUG
        value(of: "-maktaba-open", in: arguments).flatMap(PlayerRoute.init(path:))
        #else
        nil
        #endif
    }

    /// The sections' stacks once a launch into Live TV settles (`PlayerShell.settleLaunch`).
    /// The shell may show before the home said it has channels: Live TV has no section yet
    /// and a channel the launch asked for was pushed on the stack of `from`, the section
    /// shown first. With Live TV's section there (`liveShown`) the channel goes to its
    /// stack, since the shell moves there; left where it was, it stayed under Live TV's
    /// first screen and the person saw the list, not the channel. Without the section
    /// (Live TV is off for this viewer) no channel stays open on any stack.
    static func settled(_ paths: [PlayerSection: [PlayerRoute]], from: PlayerSection, liveShown: Bool) -> [PlayerSection: [PlayerRoute]] {
        func isChannel(_ route: PlayerRoute) -> Bool { if case .channel = route { true } else { false } }
        var out = paths
        guard liveShown else {
            out[.liveTV] = nil
            for (section, routes) in out where routes.contains(where: isChannel) {
                out[section] = routes.filter { !isChannel($0) }
            }
            return out
        }
        guard from != .liveTV, let routes = out[from], routes.contains(where: isChannel) else { return out }
        out[from] = routes.filter { !isChannel($0) }
        out[.liveTV, default: []] += routes.filter(isChannel)
        return out
    }

    static func liveMode(arguments: [String] = CommandLine.arguments) -> String? {
        #if DEBUG
        value(of: "-maktaba-live", in: arguments)
        #else
        nil
        #endif
    }

    static func profilePage(arguments: [String] = CommandLine.arguments) -> String? {
        #if DEBUG
        value(of: "-maktaba-profile", in: arguments)
        #else
        nil
        #endif
    }
}
