import Foundation

/// The compatibility contract (Epic 58 story 58.01, `docs/api/levels.md`): what an app does at
/// a home, from the home's `api_level`, `api_level_oldest` and `client_floor`
/// (`GET /api/discovery/home`) and the app's own build. The same decision as the home's Go one
/// (`api/internal/compat`) and the Android core's; `shared/compat/vectors.json` holds the
/// cases every copy must agree on.
public enum ApiLevels {
    /// The oldest home level these apps can use: 0, every home, those from before the contract too.
    public static let min = 0
    /// The level these apps were built for.
    public static let builtFor = 1
}

public enum CompatVerdict: String, Sendable, Equatable {
    /// Carry on: what the home lacks is hidden through its capabilities.
    case ok
    /// The home is older than the app can use: Update the home.
    case updateHome = "update_home"
    /// The home no longer serves this build: Update this app.
    case updateApp = "update_app"
}

/// What a home said of the contract. A home from before it says nothing: level 0, no floor.
public struct CompatHome: Equatable, Sendable {
    public var apiLevel: Int
    public var apiLevelOldest: Int
    public var clientFloor: [String: String]

    public init(apiLevel: Int = 0, apiLevelOldest: Int = 0, clientFloor: [String: String] = [:]) {
        self.apiLevel = apiLevel
        self.apiLevelOldest = apiLevelOldest
        self.clientFloor = clientFloor
    }
}

/// This app's build, as `X-Maktaba-Client` names it, and the levels it was made for.
public struct CompatApp: Equatable, Sendable {
    public var name: String
    public var platform: String
    public var version: String
    public var minAPILevel: Int
    public var builtForAPILevel: Int

    public init(name: String, platform: String, version: String,
                minAPILevel: Int = ApiLevels.min, builtForAPILevel: Int = ApiLevels.builtFor) {
        self.name = name
        self.platform = platform
        self.version = version
        self.minAPILevel = minAPILevel
        self.builtForAPILevel = builtForAPILevel
    }

    /// The floor key of this app: `<app>/<platform>`.
    public var key: String { "\(name)/\(platform)" }

    /// The app an `X-Maktaba-Client` value names (`tv/1.2.0 (tvos; tv)`).
    public init(clientHeader value: String, minAPILevel: Int = ApiLevels.min, builtForAPILevel: Int = ApiLevels.builtFor) {
        let head = value.split(separator: "(", maxSplits: 1, omittingEmptySubsequences: false).first.map(String.init) ?? ""
        let nameVersion = head.trimmingCharacters(in: .whitespaces).split(separator: "/", maxSplits: 1, omittingEmptySubsequences: false)
        var platform = ""
        if let open = value.firstIndex(of: "(") {
            let inside = value[value.index(after: open)...].prefix { $0 != ")" }
            platform = String(inside.prefix { $0 != ";" }).trimmingCharacters(in: .whitespaces).lowercased()
        }
        self.init(name: nameVersion.first.map { String($0).lowercased() } ?? "",
                  platform: platform,
                  version: nameVersion.count > 1 ? String(nameVersion[1]).trimmingCharacters(in: .whitespaces) : "",
                  minAPILevel: minAPILevel, builtForAPILevel: builtForAPILevel)
    }

    /// This app, from its own `X-Maktaba-Client`.
    public static let current = CompatApp(clientHeader: ClientHeader.value)
}

public enum Compat {
    /// Compares two dotted versions by number: negative, 0 or positive, nil when either has no
    /// number to read. A missing part is 0 (`1.4` is `1.4.0`); a pre-release suffix ends the
    /// number (`1.3.9-beta` is `1.3.9`); a leading `v` is dropped.
    public static func compareVersions(_ a: String, _ b: String) -> Int? {
        guard let pa = parts(a), let pb = parts(b) else { return nil }
        for i in 0..<max(pa.count, pb.count) {
            let x = i < pa.count ? pa[i] : 0
            let y = i < pb.count ? pb[i] : 0
            if x != y { return x < y ? -1 : 1 }
        }
        return 0
    }

    private static func parts(_ v: String) -> [Int]? {
        var s = v.trimmingCharacters(in: .whitespaces)
        if s.hasPrefix("v") { s.removeFirst() }
        var out: [Int] = []
        for p in s.split(separator: ".", omittingEmptySubsequences: false) {
            let digits = p.prefix { $0.isASCII && $0.isNumber }
            guard !digits.isEmpty, let n = Int(digits) else { break }
            out.append(n)
            if digits.count < p.count { break }
        }
        return out.isEmpty ? nil : out
    }

    /// Whether `app`'s version is below its entry in `floor` (an unreadable version never is).
    public static func belowFloor(_ floor: [String: String], _ app: CompatApp) -> Bool {
        guard let min = floor[app.key], let c = compareVersions(app.version, min) else { return false }
        return c < 0
    }

    /// What `app` does at `home`.
    public static func decide(_ home: CompatHome, _ app: CompatApp) -> CompatVerdict {
        if home.apiLevel < app.minAPILevel { return .updateHome }
        if app.builtForAPILevel < home.apiLevelOldest { return .updateApp }
        if belowFloor(home.clientFloor, app) { return .updateApp }
        return .ok
    }
}

/// Why this app and the home in use cannot work together, the page shown instead of signing in.
public struct CompatBlock: Equatable, Sendable {
    public let verdict: CompatVerdict
    public let homeURL: URL
    public let homeName: String
    public let homeVersion: String?
    /// Whether the person is known to run the home (nil: no one signed in here yet).
    public let admin: Bool?
    /// This app's platform, as `X-Maktaba-Client` names it: which store's words to use.
    public let platform: String

    public init(verdict: CompatVerdict, homeURL: URL, homeName: String, homeVersion: String?, admin: Bool?, platform: String) {
        self.verdict = verdict
        self.homeURL = homeURL
        self.homeName = homeName
        self.homeVersion = homeVersion
        self.admin = admin
        self.platform = platform
    }

    /// The page's words: its title, then its lines. Update the home names the home and its
    /// version, and speaks to its admin or to anyone else (both, before anyone signed in here);
    /// Update this app says how, in the words of this app's platform.
    public func text(_ words: ConnectStrings) -> (title: String, lines: [String]) {
        switch verdict {
        case .updateHome:
            var lines: [String] = []
            if let v = homeVersion, !v.isEmpty { lines.append(words.text(.compatHomeVersion, homeName, v)) }
            if admin != false { lines.append(words.text(.compatUpdateHomeAdmin, homeName)) }
            if admin != true { lines.append(words.text(.compatUpdateHomeViewer, homeName)) }
            return (words.text(.compatUpdateHomeTitle), lines)
        case .updateApp, .ok:
            return (words.text(.compatUpdateAppTitle),
                    [words.text(.compatUpdateAppWhy, homeName), words.text(platform == "tvos" ? .compatUpdateAppTV : .compatUpdateAppApple)])
        }
    }
}
