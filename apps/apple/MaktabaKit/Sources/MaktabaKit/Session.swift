import Foundation
import Observation
import Security

/// A home's tokens in the Keychain, one entry per home and app (the service names the app;
/// the account names the home), so signing out of one home leaves the others.
public final class TokenStore: @unchecked Sendable {
    private let service: String
    private let home: String
    private let lock = NSLock()

    public init(app: String, home: URL) {
        service = "\(app).tokens"
        self.home = home.absoluteString
    }

    public var access: String? { lock.withLock { read("access") } }
    public var refresh: String? { lock.withLock { read("refresh") } }

    func save(_ t: AuthTokens) {
        lock.withLock {
            write("access", t.accessToken)
            write("refresh", t.refreshToken)
        }
    }

    public func clear() {
        lock.withLock {
            delete("access")
            delete("refresh")
        }
    }

    private func key(_ kind: String) -> [String: Any] {
        [kSecClass as String: kSecClassGenericPassword,
         kSecAttrService as String: service,
         kSecAttrAccount as String: "\(home)#\(kind)"]
    }

    private func write(_ kind: String, _ value: String) {
        SecItemDelete(key(kind) as CFDictionary)
        var add = key(kind)
        add[kSecValueData as String] = Data(value.utf8)
        add[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlock
        SecItemAdd(add as CFDictionary, nil)
    }

    private func read(_ kind: String) -> String? {
        var q = key(kind)
        q[kSecReturnData as String] = true
        q[kSecMatchLimit as String] = kSecMatchLimitOne
        var out: CFTypeRef?
        guard SecItemCopyMatching(q as CFDictionary, &out) == errSecSuccess, let d = out as? Data else { return nil }
        return String(decoding: d, as: UTF8.self)
    }

    private func delete(_ kind: String) {
        SecItemDelete(key(kind) as CFDictionary)
    }
}

/// A home this app knows: its address and a name to show (its address until the home
/// says its name, after signing in).
public struct Home: Codable, Hashable, Identifiable, Sendable {
    public var id: URL { url }
    public let url: URL
    public var name: String

    public init(url: URL, name: String) {
        self.url = url
        self.name = name
    }

    /// The home's address from what a person types: a scheme added when missing
    /// (http on a local address, else https), no path, no trailing slash.
    public static func normalize(_ text: String) -> URL? {
        var s = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !s.isEmpty else { return nil }
        if !s.contains("://") {
            let host = s.split(separator: "/").first.map(String.init) ?? s
            s = (isLocal(host) ? "http://" : "https://") + s
        }
        guard var c = URLComponents(string: s), let host = c.host, !host.isEmpty,
              c.scheme == "http" || c.scheme == "https" else { return nil }
        c.path = ""
        c.query = nil
        c.fragment = nil
        return c.url
    }

    static func isLocal(_ hostPort: String) -> Bool {
        let host = hostPort.split(separator: ":").first.map(String.init) ?? hostPort
        return host == "localhost" || host.hasSuffix(".local") || host.hasPrefix("192.168.")
            || host.hasPrefix("10.") || host.hasPrefix("127.") || host.range(of: #"^172\.(1[6-9]|2\d|3[01])\."#, options: .regularExpression) != nil
    }
}

/// The app's state the screens bind to: the homes it knows, the one in use, who is signed
/// in there. One per app (`AppSession(app: "com.hamzalabs.maktaba.player")`).
@MainActor @Observable
public final class AppSession {
    public enum Phase: Equatable {
        case choosingHome
        case signingIn
        case signedIn(User)
    }

    public private(set) var homes: [Home]
    public private(set) var current: Home?
    public private(set) var phase: Phase = .choosingHome
    public private(set) var client: APIClient?

    private let app: String
    private let defaults: UserDefaults

    public init(app: String, defaults: UserDefaults = .standard) {
        self.app = app
        self.defaults = defaults
        homes = (defaults.data(forKey: "homes")).flatMap { try? JSONDecoder().decode([Home].self, from: $0) } ?? []
        if let last = defaults.url(forKey: "home"), let h = homes.first(where: { $0.url == last }) {
            use(h)
        }
    }

    /// Picks a home: its client, and signed in at once when the Keychain still has tokens.
    public func use(_ home: Home) {
        if !homes.contains(where: { $0.url == home.url }) {
            homes.append(home)
            save()
        }
        current = home
        defaults.set(home.url, forKey: "home")
        let tokens = TokenStore(app: app, home: home.url)
        client = APIClient(home: home.url, tokens: tokens)
        phase = .signingIn
        if tokens.access != nil {
            Task { await restore() }
        }
    }

    public func forget(_ home: Home) {
        TokenStore(app: app, home: home.url).clear()
        homes.removeAll { $0.url == home.url }
        save()
        if current?.url == home.url { switchHome() }
    }

    public func switchHome() {
        current = nil
        client = nil
        phase = .choosingHome
        defaults.removeObject(forKey: "home")
    }

    public func signIn(username: String, password: String) async throws {
        guard let client else { return }
        _ = try await client.signIn(username: username, password: password)
        signedIn(try await client.whoami())
    }

    public func signOut() {
        if let current { TokenStore(app: app, home: current.url).clear() }
        phase = .signingIn
    }

    /// A screen saw `APIError.signedOut`: back to signing in.
    public func signedOut() {
        phase = .signingIn
    }

    private func restore() async {
        guard let client else { return }
        if let me = try? await client.whoami() { signedIn(me) }
    }

    private func signedIn(_ me: Me) {
        named(me.homeName)
        phase = .signedIn(me.user)
    }

    /// The current home's name, as the home says it (nil: it did not say, so the name
    /// stays), kept with the homes this app knows.
    func named(_ name: String?) {
        guard let name, var home = current, home.name != name else { return }
        home.name = name
        current = home
        if let i = homes.firstIndex(where: { $0.url == home.url }) {
            homes[i] = home
        }
        save()
    }

    private func save() {
        defaults.set(try? JSONEncoder().encode(homes), forKey: "homes")
    }
}
