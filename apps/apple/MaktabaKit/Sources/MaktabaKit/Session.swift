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

    /// The same home reached at a new address: its tokens move over from the old one's
    /// entry (one read and one write each; nothing when this one has its own already).
    func adopt(_ other: TokenStore) {
        guard access == nil, let a = other.access, let r = other.refresh else { return }
        lock.withLock {
            write("access", a)
            write("refresh", r)
        }
        other.clear()
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

