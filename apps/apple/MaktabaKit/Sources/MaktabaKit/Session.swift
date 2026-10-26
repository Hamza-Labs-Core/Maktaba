import Foundation
import Observation
import Security

/// A home's tokens in the Keychain, one entry per home and app (the service names the app;
/// the account names the home), so signing out of one home leaves the others.
///
/// Read once, then kept in memory (shared by every store of the same app and home), so a
/// request does not touch the Keychain. On the Mac the tokens live in the data-protection
/// keychain, as on iOS: the old login keychain asks the person each time an app with a new
/// signature reads an item. A token found there is moved over once; a build that may not
/// use the data-protection keychain (no signing entitlement: an ad-hoc local build) stays
/// on the login keychain.
public final class TokenStore: @unchecked Sendable {
    private let service: String
    private let home: String
    private let lock = NSLock()
    private var primary: SecretStore
    private var legacy: SecretStore?

    public convenience init(app: String, home: URL) {
        let service = "\(app).tokens"
        #if os(macOS)
        self.init(service: service, home: home,
                  primary: KeychainSecrets(service: service, dataProtection: true),
                  legacy: KeychainSecrets(service: service, dataProtection: false))
        #else
        self.init(service: service, home: home, primary: KeychainSecrets(service: service, dataProtection: false), legacy: nil)
        #endif
    }

    init(service: String, home: URL, primary: SecretStore, legacy: SecretStore?) {
        self.service = service
        self.home = home.absoluteString
        self.primary = primary
        self.legacy = legacy
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
            for kind in ["access", "refresh"] {
                primary.delete(account(kind))
                legacy?.delete(account(kind))
                TokenCache.set(cacheKey(kind), nil)
            }
        }
    }

    private func account(_ kind: String) -> String { "\(home)#\(kind)" }
    private func cacheKey(_ kind: String) -> String { "\(service)|\(account(kind))" }

    private func read(_ kind: String) -> String? {
        if let known = TokenCache.get(cacheKey(kind)) { return known }
        var (value, status) = primary.read(account(kind))
        if status == errSecMissingEntitlement { useLegacyOnly() }
        if value == nil, let legacy, let old = legacy.read(account(kind)).value {
            // moved once from the login keychain (it asks no more after this)
            switch primary.write(account(kind), old) {
            case errSecSuccess: legacy.delete(account(kind))
            case errSecMissingEntitlement: useLegacyOnly()
            default: break
            }
            value = old
        } else if value == nil, status == errSecMissingEntitlement {
            value = primary.read(account(kind)).value
        }
        TokenCache.set(cacheKey(kind), value)
        return value
    }

    private func write(_ kind: String, _ value: String) {
        if primary.write(account(kind), value) == errSecMissingEntitlement {
            useLegacyOnly()
            _ = primary.write(account(kind), value)
        }
        TokenCache.set(cacheKey(kind), value)
    }

    /// This build may not use the data-protection keychain: the login keychain it is.
    private func useLegacyOnly() {
        guard let legacy else { return }
        primary = legacy
        self.legacy = nil
    }
}

/// What the app knows of each token: nil until read; then the value, or none.
private enum TokenCache {
    private static let lock = NSLock()
    nonisolated(unsafe) private static var values: [String: String?] = [:]

    static func get(_ key: String) -> String?? { lock.withLock { values[key] } }
    static func set(_ key: String, _ value: String?) { lock.withLock { values[key] = .some(value) } }
}

/// Where secrets are kept, one service's accounts (the Keychain; a fake in tests).
protocol SecretStore: Sendable {
    func read(_ account: String) -> (value: String?, status: OSStatus)
    @discardableResult func write(_ account: String, _ value: String) -> OSStatus
    func delete(_ account: String)
}

struct KeychainSecrets: SecretStore {
    let service: String
    /// macOS: the data-protection keychain (iOS's kind) instead of the login keychain.
    let dataProtection: Bool

    private func key(_ account: String) -> [String: Any] {
        var k: [String: Any] = [kSecClass as String: kSecClassGenericPassword,
                                kSecAttrService as String: service,
                                kSecAttrAccount as String: account]
        if dataProtection { k[kSecUseDataProtectionKeychain as String] = true }
        return k
    }

    func read(_ account: String) -> (value: String?, status: OSStatus) {
        var q = key(account)
        q[kSecReturnData as String] = true
        q[kSecMatchLimit as String] = kSecMatchLimitOne
        var out: CFTypeRef?
        let status = SecItemCopyMatching(q as CFDictionary, &out)
        guard status == errSecSuccess, let d = out as? Data else { return (nil, status) }
        return (String(decoding: d, as: UTF8.self), status)
    }

    func write(_ account: String, _ value: String) -> OSStatus {
        SecItemDelete(key(account) as CFDictionary)
        var add = key(account)
        add[kSecValueData as String] = Data(value.utf8)
        add[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlock
        return SecItemAdd(add as CFDictionary, nil)
    }

    func delete(_ account: String) {
        SecItemDelete(key(account) as CFDictionary)
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

    public var signedIn: Bool {
        if case .signedIn = phase { true } else { false }
    }

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

    /// This device was signed in some other way (a TV's code, approved on a phone): who
    /// is it now.
    public func signedInByCode() async throws {
        guard let client else { return }
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
