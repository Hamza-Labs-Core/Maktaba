import Foundation

// Epic 42 story 42.2: reaching a home. A home is found on this network (Bonjour), typed by
// its address, or scanned / opened as a link (`maktaba://connect?id=&n=&u=&u=`). Whatever
// the way, the app asks the address whether it really is a Maktaba home
// (`GET /api/discovery/home`; `GET /api/system/version` on a home from before it) and keeps
// the first address that answers.

// MARK: - The connect link (a home's QR code)

/// `maktaba://connect?id=<home id>&n=<name>&u=<base url>[&u=...]`: a home's QR code and
/// share link. `u` repeats, the local address first; the app tries them in that order.
public struct ConnectLink: Equatable, Sendable {
    public let homeID: String?
    public let name: String?
    /// Every `u` that is an http(s) address, in the link's order, without repeats.
    public let urls: [URL]

    public init?(_ url: URL) {
        guard url.scheme?.lowercased() == "maktaba",
              let c = URLComponents(url: url, resolvingAgainstBaseURL: false) else { return nil }
        // maktaba://connect?… (host) or maktaba:connect?… (path)
        let what = (c.host ?? c.path).lowercased().trimmingCharacters(in: CharacterSet(charactersIn: "/"))
        guard what == "connect" else { return nil }
        let items = c.queryItems ?? []
        func first(_ name: String) -> String? {
            items.first { $0.name == name }?.value?.trimmingCharacters(in: .whitespacesAndNewlines).nilIfEmpty
        }
        var urls: [URL] = []
        for item in items where item.name == "u" {
            guard let raw = item.value, let u = Home.normalizeBase(raw), !urls.contains(u) else { continue }
            urls.append(u)
        }
        guard !urls.isEmpty else { return nil }
        homeID = first("id")
        name = first("n")
        self.urls = urls
    }

    public init?(string: String) {
        guard let url = URL(string: string.trimmingCharacters(in: .whitespacesAndNewlines)) else { return nil }
        self.init(url)
    }
}

/// What a scanned QR code, an opened link or a pasted text is.
public enum ScannedCode: Equatable, Sendable {
    /// A home's connect link.
    case connect(ConnectLink)
    /// A home's address (a person typed or pasted it, or a QR code holds a plain URL).
    case address(URL)
    /// A device's sign-in code (`XXXX-XXXX`), alone or in the approve page's address
    /// (`…/pair?code=XXXX-XXXX`, a TV's or a phone's QR code).
    case signInCode(String)

    public static func parse(_ text: String) -> ScannedCode? {
        let s = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !s.isEmpty else { return nil }
        if let link = ConnectLink(string: s) { return .connect(link) }
        if let url = URL(string: s), let c = URLComponents(url: url, resolvingAgainstBaseURL: false),
           let raw = c.queryItems?.first(where: { $0.name == "code" })?.value,
           url.scheme?.lowercased() == "maktaba" || c.path.hasSuffix("/pair"),
           let code = DeviceCode.normalize(raw) {
            return .signInCode(code)
        }
        if !s.contains("://") && !s.contains(".") && !s.contains(":"), let code = DeviceCode.normalize(s) {
            return .signInCode(code)
        }
        if let url = Home.normalize(s) { return .address(url) }
        return nil
    }
}

// MARK: - A device's sign-in code

/// The code a device shows to be signed in (`POST /api/pairing/device`): eight letters and
/// digits of Crockford's alphabet, shown as `XXXX-XXXX`.
public enum DeviceCode {
    static let alphabet = Set("0123456789ABCDEFGHJKMNPQRSTVWXYZ")

    /// What a person typed or a QR code held, as `XXXX-XXXX` (nil: not a code). Case,
    /// spaces and dashes do not matter; O reads as 0 and I or L as 1 (the code never has
    /// them); Arabic-Indic digits read as their digits.
    public static func normalize(_ raw: String) -> String? {
        var out = ""
        for ch in raw.uppercased() {
            switch ch {
            case " ", "-", "\u{2010}", "\u{2011}", "\u{2013}": continue
            case "O": out.append("0")
            case "I", "L": out.append("1")
            default:
                if let d = ch.unicodeScalars.first.flatMap({ arabicDigit($0) }) {
                    out.append(d)
                } else {
                    out.append(ch)
                }
            }
        }
        guard out.count == 8, out.allSatisfy({ alphabet.contains($0) }) else { return nil }
        return String(out.prefix(4)) + "-" + String(out.suffix(4))
    }

    private static func arabicDigit(_ u: Unicode.Scalar) -> Character? {
        switch u.value {
        case 0x0660...0x0669: Character(String(u.value - 0x0660))
        case 0x06F0...0x06F9: Character(String(u.value - 0x06F0))
        default: nil
        }
    }
}

// MARK: - Is this address a home?

/// `GET /api/discovery/home` (public): who the home is and how one signs in there.
public struct HomeDiscovery: Decodable, Equatable, Sendable {
    public let id: String?
    public let name: String?
    public let version: String?
    public let urls: [String]
    public let signIn: SignInMethods?

    enum CodingKeys: String, CodingKey {
        case id, name, version, urls
        case signIn = "sign_in"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decodeIfPresent(String.self, forKey: .id)?.nilIfEmpty
        name = try c.decodeIfPresent(String.self, forKey: .name)?.trimmingCharacters(in: .whitespacesAndNewlines).nilIfEmpty
        version = try c.decodeIfPresent(String.self, forKey: .version)
        urls = try c.decodeIfPresent([String].self, forKey: .urls) ?? []
        signIn = try c.decodeIfPresent(SignInMethods.self, forKey: .signIn)
    }
}

/// The ways a home lets an app sign in (`sign_in` of `/api/discovery/home`).
public struct SignInMethods: Codable, Equatable, Sendable {
    public let password: Bool
    public let code: Bool

    public init(password: Bool, code: Bool) {
        self.password = password
        self.code = code
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        password = try c.decodeIfPresent(Bool.self, forKey: .password) ?? true
        code = try c.decodeIfPresent(Bool.self, forKey: .code) ?? false
    }

    enum CodingKeys: String, CodingKey { case password, code }
}

/// `GET /api/system/version`, what a home from before `/api/discovery/home` says.
struct LegacyVersion: Decodable {
    let version: String
    let homeInstanceID: String?

    enum CodingKeys: String, CodingKey {
        case version
        case homeInstanceID = "home_instance_id"
    }
}

/// A home that answered at `url`.
public struct ReachedHome: Equatable, Sendable {
    public let url: URL
    public let id: String?
    public let name: String?
    public let version: String?
    /// The other addresses the home gave (local, relay), its own order.
    public let addresses: [URL]
    /// nil: an older home that does not say (password; a code when it answers one).
    public let signIn: SignInMethods?
}

/// Why no address of a home could be used.
public enum ReachFailure: Error, Equatable, Sendable {
    /// Nothing answered (off, another network, a wrong port).
    case unreachable(AppError.Kind)
    /// Something answered, but not a Maktaba home.
    case notAHome
    /// A home answered, but another one than the link names.
    case otherHome
}

/// Asks addresses whether they are a Maktaba home, in order, and keeps the first that is.
public struct HomeProbe: Sendable {
    let session: URLSession
    /// How long one address may take to answer.
    let timeout: TimeInterval

    public init(session: URLSession = .shared, timeout: TimeInterval = 6) {
        self.session = session
        self.timeout = timeout
    }

    /// The first of `candidates` that is a home (and is the home `expectedID` names, when
    /// it says its id), tried one after the other.
    public func reach(_ candidates: [URL], expectedID: String? = nil) async -> Result<ReachedHome, ReachFailure> {
        var failure = ReachFailure.unreachable(.unreachable)
        for url in candidates {
            if Task.isCancelled { break }
            switch await check(url) {
            case let .success(home):
                if let expectedID, let id = home.id, id.lowercased() != expectedID.lowercased() {
                    failure = .otherHome
                    continue
                }
                return .success(home)
            case let .failure(f):
                // "not a home" or "another home" says more than "did not answer"
                if case .unreachable = failure { failure = f }
            }
        }
        return .failure(failure)
    }

    /// Whether `url` is a home: `/api/discovery/home`, else (a home from before it, 404)
    /// `/api/system/version`.
    public func check(_ url: URL) async -> Result<ReachedHome, ReachFailure> {
        let (code, data): (Int, Data)
        do {
            (code, data) = try await get(url, "/api/discovery/home")
        } catch {
            return .failure(.unreachable(AppError(error).kind))
        }
        if code == 200 {
            guard let d = try? JSONDecoder().decode(HomeDiscovery.self, from: data) else { return .failure(.notAHome) }
            let addresses = d.urls.compactMap(Home.normalizeBase).filter { $0 != url }
            return .success(ReachedHome(url: url, id: d.id, name: d.name, version: d.version, addresses: addresses, signIn: d.signIn))
        }
        guard code == 404 || code == 405 else { return .failure(.notAHome) }
        guard let (vcode, vdata) = try? await get(url, "/api/system/version"), vcode == 200,
              let v = try? JSONDecoder().decode(LegacyVersion.self, from: vdata) else { return .failure(.notAHome) }
        return .success(ReachedHome(url: url, id: v.homeInstanceID?.nilIfEmpty, name: nil, version: v.version, addresses: [], signIn: nil))
    }

    private func get(_ base: URL, _ path: String) async throws -> (Int, Data) {
        var r = URLRequest(url: base.appending(path: path))
        r.timeoutInterval = timeout
        r.setValue("application/json", forHTTPHeaderField: "Accept")
        r.setValue(ClientHeader.value, forHTTPHeaderField: ClientHeader.name)
        let (data, response) = try await session.data(for: r)
        return ((response as? HTTPURLResponse)?.statusCode ?? 0, data)
    }
}

extension String {
    var nilIfEmpty: String? { isEmpty ? nil : self }
}
