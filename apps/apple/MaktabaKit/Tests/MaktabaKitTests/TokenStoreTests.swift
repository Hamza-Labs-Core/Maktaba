import Foundation
import Security
import Testing
@testable import MaktabaKit

/// The Keychain is read once per token, not per request (on the Mac every read of the
/// login keychain by a newly signed app asks the person), and tokens move once to the
/// data-protection keychain.
@Suite struct Tokens {
    let home = URL(string: "http://192.168.50.177:8088")!

    func store(_ primary: FakeSecrets, _ legacy: FakeSecrets? = nil) -> TokenStore {
        TokenStore(service: "test.\(UUID().uuidString)", home: home, primary: primary, legacy: legacy)
    }

    @Test func readsTheKeychainOnceForManyRequests() {
        let primary = FakeSecrets(["http://192.168.50.177:8088#access": "a1"])
        let tokens = store(primary)
        for _ in 0..<50 { #expect(tokens.access == "a1") }
        #expect(tokens.refresh == nil)
        #expect(tokens.refresh == nil)
        #expect(primary.reads == 2) // access once, refresh once (its absence too)
    }

    @Test func everyStoreOfTheHomeSeesASaveAndAClear() {
        let primary = FakeSecrets()
        let service = "test.\(UUID().uuidString)"
        let a = TokenStore(service: service, home: home, primary: primary, legacy: nil)
        let b = TokenStore(service: service, home: home, primary: primary, legacy: nil)
        #expect(b.access == nil)
        a.save(AuthTokens(accessToken: "a2", refreshToken: "r2", user: nil))
        #expect(b.access == "a2" && b.refresh == "r2")
        b.clear()
        #expect(a.access == nil && a.refresh == nil)
        #expect(primary.values.isEmpty)
    }

    @Test func movesATokenOnceFromTheLoginKeychain() {
        let primary = FakeSecrets()
        let legacy = FakeSecrets(["http://192.168.50.177:8088#refresh": "r-old"])
        let tokens = store(primary, legacy)
        #expect(tokens.refresh == "r-old")
        #expect(primary.values["http://192.168.50.177:8088#refresh"] == "r-old")
        #expect(legacy.values.isEmpty)
        #expect(tokens.refresh == "r-old")
        #expect(legacy.reads == 1)
    }

    /// An ad-hoc build may not use the data-protection keychain: the login keychain still works.
    @Test func aBuildWithoutTheEntitlementKeepsTheLoginKeychain() {
        let primary = FakeSecrets(failing: errSecMissingEntitlement)
        let legacy = FakeSecrets(["http://192.168.50.177:8088#access": "a-old"])
        let tokens = store(primary, legacy)
        #expect(tokens.access == "a-old")
        #expect(legacy.values["http://192.168.50.177:8088#access"] == "a-old")
        tokens.save(AuthTokens(accessToken: "a3", refreshToken: "r3", user: nil))
        #expect(legacy.values["http://192.168.50.177:8088#access"] == "a3")
        #expect(tokens.access == "a3")
    }

    @Test func aMissingEntitlementOnFirstSaveFallsBackToo() {
        let primary = FakeSecrets(failing: errSecMissingEntitlement)
        let legacy = FakeSecrets()
        let tokens = store(primary, legacy)
        tokens.save(AuthTokens(accessToken: "a4", refreshToken: "r4", user: nil))
        #expect(legacy.values.count == 2)
        #expect(tokens.access == "a4")
    }
}

final class FakeSecrets: SecretStore, @unchecked Sendable {
    private let lock = NSLock()
    private var stored: [String: String]
    private var readCount = 0
    private let failing: OSStatus?

    init(_ values: [String: String] = [:], failing: OSStatus? = nil) {
        stored = values
        self.failing = failing
    }

    var values: [String: String] { lock.withLock { stored } }
    var reads: Int { lock.withLock { readCount } }

    func read(_ account: String) -> (value: String?, status: OSStatus) {
        lock.withLock {
            readCount += 1
            if let failing { return (nil, failing) }
            return stored[account].map { ($0, errSecSuccess) } ?? (nil, errSecItemNotFound)
        }
    }

    func write(_ account: String, _ value: String) -> OSStatus {
        lock.withLock {
            if let failing { return failing }
            stored[account] = value
            return errSecSuccess
        }
    }

    func delete(_ account: String) {
        lock.withLock { _ = stored.removeValue(forKey: account) }
    }
}
