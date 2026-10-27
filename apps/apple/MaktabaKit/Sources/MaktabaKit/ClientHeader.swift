import Foundation

/// `X-Maktaba-Client` (Epic 44 story 44.2): every request says which app sent it,
/// `<app>/<version> (<platform>; <device>)`, e.g. `player/1.3.0 (ios; phone)`. The home
/// reads it as a label (analytics, its use per app), never for rights; any app but the
/// web's still signs in for tokens, as `native` did.
public enum ClientHeader {
    public static let name = "X-Maktaba-Client"

    /// This app's value, from its bundle.
    public static let value: String = make(
        bundleID: Bundle.main.bundleIdentifier ?? "",
        version: Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "")

    /// The value for a bundle: the app from its id (`…remote…` the Remote, `…tv` the Apple TV
    /// app, else the Player), a `.tablet` id the iPad's, the platform this was built for.
    public static func make(bundleID: String, version: String) -> String {
        let parts = bundleID.lowercased().split(separator: ".")
        let app = parts.contains("remote") ? "remote" : parts.last == "tv" ? "tv" : "player"
        #if os(tvOS)
        let (platform, device) = ("tvos", "tv")
        #elseif os(macOS)
        let (platform, device) = ("macos", "desktop")
        #else
        let (platform, device) = parts.last == "tablet" ? ("ipados", "tablet") : ("ios", "phone")
        #endif
        return "\(app)/\(version.isEmpty ? "dev" : version) (\(platform); \(device))"
    }
}
