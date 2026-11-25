import Foundation

/// `X-Maktaba-Client` (Epic 44 story 44.2): every request says which app sent it,
/// `<app>/<version> (<platform>; <device>)`, e.g. `player/1.3.0 (ios; phone)`. The home
/// reads it as a label (analytics, its use per app), never for rights; any app but the
/// web's still signs in for tokens, as `native` did.
public enum ClientHeader {
    public static let name = "X-Maktaba-Client"

    /// This app's value, from its bundle and the device it runs on.
    public static let value: String = make(
        bundleID: Bundle.main.bundleIdentifier ?? "",
        version: Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "",
        pad: runsOnPad)

    /// The value for a bundle: the app from its id (`…remote…` the Remote, `…tv` the Apple TV
    /// app, else the Player), the platform this was built for and, on iOS, whether the device
    /// is an iPad: the Player and the Remote are each one universal app for iPhone and iPad, so
    /// the bundle id no longer says which (the retired iPad-only apps' ids ended `.tablet`).
    public static func make(bundleID: String, version: String, pad: Bool = false) -> String {
        let parts = bundleID.lowercased().split(separator: ".")
        let app = parts.contains("remote") ? "remote" : parts.last == "tv" ? "tv" : "player"
        #if os(tvOS)
        let (platform, device) = ("tvos", "tv")
        #elseif os(macOS)
        let (platform, device) = ("macos", "desktop")
        #else
        let (platform, device) = pad ? ("ipados", "tablet") : ("ios", "phone")
        #endif
        return "\(app)/\(version.isEmpty ? "dev" : version) (\(platform); \(device))"
    }

    /// Whether this iOS device is an iPad, from its model identifier ("iPad16,3"; a simulator
    /// names the device it simulates in SIMULATOR_MODEL_IDENTIFIER). Read without UIKit, which
    /// would tie this constant to the main actor.
    static var runsOnPad: Bool {
        #if os(iOS)
        isPad(model: ProcessInfo.processInfo.environment["SIMULATOR_MODEL_IDENTIFIER"] ?? machine())
        #else
        false
        #endif
    }

    static func isPad(model: String) -> Bool { model.hasPrefix("iPad") }

    private static func machine() -> String {
        var info = utsname()
        uname(&info)
        return withUnsafeBytes(of: &info.machine) { raw in
            String(decoding: raw.prefix { $0 != 0 }, as: UTF8.self)
        }
    }
}
