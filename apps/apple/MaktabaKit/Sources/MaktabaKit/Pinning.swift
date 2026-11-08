import CryptoKit
import Foundation
import Security

/// The home addresses reached by IP whose certificate is checked against one of the home's
/// own names (Epic 50 story 50.15 §3). A router blocking DNS rebinding refuses the home's LAN
/// name, so the cloud's home list also gives `https://<ip>:<port>` (`local_ip`) with the name
/// (`tls_name`) the home's certificate must hold there. The chain is checked as usual; only the
/// name differs, so the app still proves it reached that home and no other.
public final class PinnedNames: @unchecked Sendable {
    public static let shared = PinnedNames()
    private let lock = NSLock()
    // each home's pins (its server ID: address|port to name); a list replaces its home's own
    private var homes: [String: [String: String]] = [:]

    public init() {}

    static func key(_ host: String, _ port: Int) -> String {
        host.trimmingCharacters(in: CharacterSet(charactersIn: "[]")).lowercased() + "|" + String(port)
    }

    /// The ID a home's names carry (`<encoded ip>.<home id>.<domain>`), from its server ID as the
    /// cloud derives it (`direct.HomeID`): the first 32 hex digits of SHA-256("direct-v1" + id).
    public static func homeID(_ serverID: String) -> String {
        SHA256.hash(data: Data(("direct-v1" + serverID).utf8)).map { String(format: "%02x", $0) }.joined().prefix(32)
            .description
    }

    /// Pins the `local_ip` `entries` (URL and tls_name) of the home `serverID`, replacing that
    /// home's earlier pins. An entry is kept only when its URL is https to an IP address (with its
    /// port) and its name is this home's name for that same address; anything else is ignored, so
    /// a home list can never make an address answer for an unrelated name or for another home.
    public func pinHome(_ serverID: String, _ entries: [(url: URL, name: String)]) {
        guard !serverID.isEmpty else { return }
        let hid = Self.homeID(serverID)
        var kept: [String: String] = [:]
        for e in entries {
            let n = e.name.trimmingCharacters(in: .whitespaces).trimmingCharacters(in: CharacterSet(charactersIn: ".")).lowercased()
            guard e.url.scheme?.lowercased() == "https", let host = e.url.host(), Self.isIPLiteral(host),
                  Self.namesAddress(n, host: host, homeID: hid) else { continue }
            kept[Self.key(host, e.url.port ?? 443)] = n
        }
        lock.withLock { homes[serverID] = kept.isEmpty ? nil : kept }
    }

    /// The name `host`:`port`'s certificate is checked against, when one home alone pinned it.
    /// Two homes on the same private address (each on its own LAN) leave it unpinned: checked by
    /// IP, so refused, and the app goes on to the home's next connection; a certificate of one
    /// home is never taken for the other.
    public func name(host: String, port: Int) -> String? {
        let k = Self.key(host, port)
        let found = lock.withLock { Set(homes.values.compactMap { $0[k] }) }
        return found.count == 1 ? found.first : nil
    }

    /// Forgets every pin (tests; a sign-out keeps them: they name no account).
    public func clear() { lock.withLock { homes.removeAll() } }

    /// Whether `name` is the home `homeID`'s name for `host`: four labels or more, the first
    /// `host` as the cloud encodes it ("192-168-1-20", "fd00--7"; `direct.EncodeIP`), the second
    /// `homeID`.
    static func namesAddress(_ name: String, host: String, homeID: String) -> Bool {
        let labels = name.split(separator: ".", omittingEmptySubsequences: false)
        guard labels.count >= 4, labels.allSatisfy({ !$0.isEmpty }), labels[1] == homeID,
              let label = labels.first else { return false }
        let hex = Set("0123456789abcdef-")
        guard label.allSatisfy({ hex.contains($0) }) else { return false }
        let v4 = label.filter { $0 == "-" }.count == 3 && !label.contains("--") && !label.contains(where: { "abcdef".contains($0) })
        let text = label.replacingOccurrences(of: "-", with: v4 ? "." : ":")
        guard let a = addressBytes(text) else { return false }
        return a == addressBytes(host.trimmingCharacters(in: CharacterSet(charactersIn: "[]")))
    }

    static func addressBytes(_ s: String) -> [UInt8]? {
        var v4 = in_addr()
        if inet_pton(AF_INET, s, &v4) == 1 { return withUnsafeBytes(of: &v4) { Array($0) } }
        var v6 = in6_addr()
        if inet_pton(AF_INET6, s, &v6) == 1 { return withUnsafeBytes(of: &v6) { Array($0) } }
        return nil
    }

    static func isIPLiteral(_ host: String) -> Bool {
        let h = host.trimmingCharacters(in: CharacterSet(charactersIn: "[]"))
        if h.contains(":") { return true }
        let parts = h.split(separator: ".", omittingEmptySubsequences: false)
        return parts.count == 4 && parts.allSatisfy { UInt8($0) != nil }
    }
}

/// The URL session for homes: the system's checks, except that a pinned address
/// (`PinnedNames`) is checked against its home's name instead of its IP. The API, the stream
/// proxy (so the player too) and the address probe use it.
public final class PinnedTrust: NSObject, URLSessionDelegate, Sendable {
    public static let shared = PinnedTrust(names: .shared)
    /// The session every home request goes through by default.
    public static let session = URLSession(configuration: .default, delegate: PinnedTrust.shared, delegateQueue: nil)

    let names: PinnedNames
    init(names: PinnedNames) { self.names = names }

    public func urlSession(_ session: URLSession, didReceive challenge: URLAuthenticationChallenge) async
        -> (URLSession.AuthChallengeDisposition, URLCredential?) {
        answer(challenge.protectionSpace)
    }

    /// Default handling unless the address is pinned; pinned, the chain must be valid for its
    /// home's name, or the connection is refused.
    func answer(_ space: URLProtectionSpace) -> (URLSession.AuthChallengeDisposition, URLCredential?) {
        guard space.authenticationMethod == NSURLAuthenticationMethodServerTrust, let trust = space.serverTrust,
              let ok = check(trust, host: space.host, port: space.port) else { return (.performDefaultHandling, nil) }
        return ok ? (.useCredential, URLCredential(trust: trust)) : (.cancelAuthenticationChallenge, nil)
    }

    /// Whether `trust` is valid for `host`:`port`'s pinned name; nil when it isn't pinned.
    func check(_ trust: SecTrust, host: String, port: Int) -> Bool? {
        guard let name = names.name(host: host, port: port) else { return nil }
        SecTrustSetPolicies(trust, SecPolicyCreateSSL(true, name as CFString))
        var error: CFError?
        return SecTrustEvaluateWithError(trust, &error)
    }
}
