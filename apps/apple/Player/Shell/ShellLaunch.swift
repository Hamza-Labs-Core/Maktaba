import Foundation

/// The launch arguments the UI tests start the Player with. Read only in a Debug build:
/// a TestFlight or App Store build (Release) ignores them.
/// - `-maktaba-sections all` or `home,search`: the sections shown (`PlayerSection.visible`);
/// - `-maktaba-section live`: the section chosen at launch;
/// - `-maktaba-open video/v1`: a route pushed on that section's stack at launch, so a
///   screen's test opens it straight away (`PlayerRoute(path:)`).
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
}
