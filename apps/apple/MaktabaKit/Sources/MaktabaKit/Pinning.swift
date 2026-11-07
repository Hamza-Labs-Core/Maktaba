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
    private var names: [String: String] = [:]

    public init() {}

    static func key(_ host: String, _ port: Int) -> String {
        host.trimmingCharacters(in: CharacterSet(charactersIn: "[]")).lowercased() + "|" + String(port)
    }

    /// Pins `url` (https, an IP address, with its port) to `name`; anything else is ignored.
    public func pin(_ url: URL, name: String) {
        let n = name.trimmingCharacters(in: .whitespaces).trimmingCharacters(in: CharacterSet(charactersIn: ".")).lowercased()
        guard url.scheme?.lowercased() == "https", let host = url.host(), Self.isIPLiteral(host), !n.isEmpty else { return }
        let port = url.port ?? 443
        lock.withLock { names[Self.key(host, port)] = n }
    }

    /// The name `host`:`port`'s certificate is checked against, when it is pinned.
    public func name(host: String, port: Int) -> String? {
        lock.withLock { names[Self.key(host, port)] }
    }

    /// Forgets every pin (tests; a sign-out keeps them: they name no account).
    public func clear() { lock.withLock { names.removeAll() } }

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
