import Foundation
import Security
import Testing
@testable import MaktabaKit

/// Signed as the app is, the Mac Player may use the data-protection keychain (no login-
/// keychain prompts): a token written there reads back.
@Suite struct MacKeychain {
    @Test func theDataProtectionKeychainKeepsAToken() {
        let k = KeychainSecrets(service: "com.hamzalabs.maktaba.player.mac.test.\(UUID().uuidString)", dataProtection: true)
        defer { k.delete("probe") }
        let wrote = k.write("probe", "value")
        let (read, status) = k.read("probe")
        #expect(wrote == errSecSuccess, "write: \(wrote)")
        #expect(read == "value", "read: \(status)")
    }
}
