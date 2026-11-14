import Foundation
import Observation

// Epic 58 story 58.03: a TV that cannot find the home on its network asks Maktaba Cloud for
// a code; a person signed in on the home approves it from "Approve a TV or device". The
// home's own codes (story 42.6) use the full Crockford alphabet and the cloud's a smaller
// one, so a code can look like either: the home's code is tried first, and a code the home
// does not hold that has the cloud's shape is then looked up through the home.

/// The cloud's TV codes: eight characters of `23456789ABCDEFGHJKMNPQRSTVWXYZ`, shown `ABCD-2345`.
public enum CloudCode {
    static let alphabet = Set("23456789ABCDEFGHJKMNPQRSTVWXYZ")

    /// Whether `raw` (dashes and spaces left out, any case) has the cloud's shape.
    public static func isCloudShaped(_ raw: String) -> Bool {
        let s = raw.uppercased().filter { $0 != "-" && $0 != " " }
        return s.count == 8 && s.allSatisfy { alphabet.contains($0) }
    }
}

/// What asked with a cloud code (`GET /api/pairing/cloud-code/{code}`), for "Is this your TV?".
public struct CloudTVCode: Decodable, Sendable, Equatable {
    public let code: String
    /// `tizen`, `webos`, `roku`, …
    public let platform: String
    public let appVersion: String?
    /// The country of the TV's address (ISO 3166 alpha-2), nil when the cloud could not tell.
    public let country: String?
    public let expiresAt: Date?

    public init(code: String, platform: String, appVersion: String? = nil, country: String? = nil, expiresAt: Date? = nil) {
        self.code = code
        self.platform = platform
        self.appVersion = appVersion?.nilIfBlank
        self.country = country?.nilIfBlank
        self.expiresAt = expiresAt
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        func k(_ s: String) -> AnyKey { AnyKey(stringValue: s) }
        code = (c.lenient(k("code")) as String?) ?? ""
        platform = (c.lenient(k("platform")) as String?)?.nilIfBlank ?? "TV"
        appVersion = (c.lenient(k("app_version")) as String?)?.nilIfBlank
        country = (c.lenient(k("country")) as String?)?.nilIfBlank
        expiresAt = (c.lenient(k("expires_at")) as String?).flatMap(homeTime)
    }
}

/// What the home said of a cloud code.
public enum CloudCodeLookup: Sendable, Equatable {
    case found(CloudTVCode)
    /// No TV is waiting with it (wrong, expired, or a home from before cloud codes).
    case notFound
    /// The home has no remote access: it cannot reach Maktaba Cloud for the code.
    case notLinked
}

/// One app signed in as the person by a code (`GET /api/me/app-sign-ins`): a TV approved on
/// the home's network or through a cloud code.
public struct AppSignIn: Decodable, Identifiable, Hashable, Sendable {
    public let id: String
    /// `tv`, `player`, …
    public let kind: String
    /// The name it gave, or its platform (`tizen`) for a cloud code.
    public let label: String?
    /// `cloud_code` when it was approved through a Maktaba Cloud code.
    public let via: String?
    public let createdAt: Date?
    public let lastSeenAt: Date?
    public let expiresAt: Date?
    public let current: Bool

    public init(id: String, kind: String = "tv", label: String? = nil, via: String? = nil,
                createdAt: Date? = nil, lastSeenAt: Date? = nil, expiresAt: Date? = nil, current: Bool = false) {
        self.id = id
        self.kind = kind
        self.label = label?.nilIfBlank
        self.via = via?.nilIfBlank
        self.createdAt = createdAt
        self.lastSeenAt = lastSeenAt
        self.expiresAt = expiresAt
        self.current = current
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        func k(_ s: String) -> AnyKey { AnyKey(stringValue: s) }
        id = try c.decode(String.self, forKey: k("id"))
        kind = (c.lenient(k("kind")) as String?)?.nilIfBlank ?? "tv"
        label = (c.lenient(k("label")) as String?)?.nilIfBlank
        via = (c.lenient(k("via")) as String?)?.nilIfBlank
        createdAt = (c.lenient(k("created_at")) as String?).flatMap(homeTime)
        lastSeenAt = (c.lenient(k("last_seen_at")) as String?).flatMap(homeTime)
        expiresAt = (c.lenient(k("expires_at")) as String?).flatMap(homeTime)
        current = c.lenient(k("current")) ?? false
    }

    public var viaCloudCode: Bool { via == "cloud_code" }

    /// The row's name: "tizen TV, via Maktaba Cloud code" for a cloud code, else the name it
    /// gave (or its app's name).
    public func title(_ words: ConnectStrings) -> String {
        let name = label ?? words.appName(kind)
        return viaCloudCode ? words.text(.appSignInViaCloudCode, name) : name
    }
}

extension APIClient {
    /// What asked with the cloud code `code` (`GET /api/pairing/cloud-code/{code}`, signed in
    /// on the home).
    public func cloudCode(_ code: String) async throws -> CloudCodeLookup {
        let compact = code.uppercased().filter { $0 != "-" && $0 != " " }
        do {
            let c: CloudTVCode = try await get("/api/pairing/cloud-code/\(Self.segment(compact))")
            return .found(c)
        } catch let e as APIError {
            switch e {
            case .status(409, _, _) where e.problem?.code == "cloud_not_linked": return .notLinked
            // a home from before cloud codes has no such route (its router's 404 or 405)
            case .status(404, _, _), .status(405, _, _): return .notFound
            default: throw e
            }
        }
    }

    /// Signs the TV showing the cloud code `code` in as the person signed in here
    /// (`POST /api/pairing/cloud-approve`).
    public func approveCloudCode(_ code: String) async throws {
        let compact = code.uppercased().filter { $0 != "-" && $0 != " " }
        _ = try await sendRaw(request("POST", "/api/pairing/cloud-approve", body: ["code": compact]))
    }

    /// The apps signed in as the person by a code, newest first (`GET /api/me/app-sign-ins`).
    /// A home from before the list (404 or 405) has none to show.
    public func appSignIns() async throws -> [AppSignIn] {
        do {
            let r: Items<AppSignIn> = try await get("/api/me/app-sign-ins")
            return r.items
        } catch let APIError.status(code, _, _) where code == 404 || code == 405 {
            return []
        }
    }

    /// Signs one app out (`DELETE /api/me/app-sign-ins/{id}`).
    public func signOutApp(_ id: String) async throws {
        _ = try await browseRaw(request("DELETE", "/api/me/app-sign-ins/\(Self.segment(id))"))
    }
}

/// "Approve a TV or device"'s steps, kept out of the view so they are tested as they are: the
/// home's code first; a code the home does not hold that has the cloud's shape is looked up
/// through the home's cloud link, the person confirms the TV (its platform and country), and
/// only then is it approved.
@MainActor @Observable
public final class ApproveCode {
    public enum Step: Equatable, Sendable {
        /// Nothing looked up yet (or the person said it is not this one).
        case idle
        /// The home's own code, and what the home said of it.
        case home(code: String, DeviceLookup)
        /// A cloud code: the TV to confirm before approving it.
        case cloud(code: String, CloudTVCode)
        /// Approved: the sentence to show.
        case approved(String)

        /// The code this step is about (nil: none, or already approved).
        public var code: String? {
            switch self {
            case let .home(code, _), let .cloud(code, _): code
            case .idle, .approved: nil
            }
        }
    }

    public private(set) var step: Step = .idle
    public private(set) var busy = false
    /// Why the last step did not go through (shown under the form).
    public var problem: String?

    private let client: APIClient
    public let words: ConnectStrings

    public init(client: APIClient, words: ConnectStrings = .forPreferredLanguages()) {
        self.client = client
        self.words = words
    }

    /// Starts again (the code was changed, or "Not this one").
    public func reset() {
        step = .idle
        problem = nil
    }

    /// The code as `XXXX-XXXX` from what was typed or scanned (nil: not a code).
    public static func normalize(_ raw: String) -> String? {
        DeviceCode.normalize(raw) ?? {
            if case let .signInCode(c) = ScannedCode.parse(raw) { return c } else { return nil }
        }()
    }

    /// Looks `raw` up; returns the code as it reads (`XXXX-XXXX`), for the field.
    @discardableResult
    public func lookUp(_ raw: String) async -> String? {
        problem = nil
        step = .idle
        guard let code = Self.normalize(raw) else {
            problem = words.text(.notACode)
            return nil
        }
        busy = true
        defer { busy = false }
        do {
            let home = try await client.pendingDevice(code: code)
            if home == .notFound, CloudCode.isCloudShaped(code) {
                switch try await client.cloudCode(code) {
                case let .found(tv): step = .cloud(code: code, tv)
                case .notFound: step = .home(code: code, .notFound)
                case .notLinked: problem = words.text(.approveCloudNeedsRemote)
                }
            } else {
                step = .home(code: code, home)
            }
        } catch {
            problem = message(error)
        }
        return code
    }

    /// Approves what is on screen: the home's code, or the cloud code once confirmed.
    public func approve() async {
        problem = nil
        busy = true
        defer { busy = false }
        do {
            switch step {
            case let .home(code, lookup):
                try await client.approveDevice(code: code)
                var name: String?
                if case let .found(d) = lookup { name = d.deviceLabel ?? d.clientApp.map(words.appName) ?? d.device }
                step = .approved(words.text(.approved, name ?? words.text(.anotherDevice)))
            case let .cloud(code, _):
                try await client.approveCloudCode(code)
                step = .approved(words.text(.approveCloudDone))
            case .idle, .approved:
                return
            }
        } catch {
            problem = message(error)
        }
    }

    /// "Is this your TV? tizen, in Jordan." (the platform alone when the country is not known).
    public func confirmation(_ tv: CloudTVCode) -> String {
        guard let country = tv.country else { return words.text(.approveCloudConfirmNoCountry, tv.platform) }
        let name = words.locale.localizedString(forRegionCode: country) ?? country
        return words.text(.approveCloudConfirm, tv.platform, name)
    }

    private func message(_ error: Error) -> String {
        guard let e = error as? APIError else { return HomeFailure(error).message() }
        switch e {
        // a sign-in through Maktaba Cloud cannot approve: only one on the home can
        case .status(403, _, _): return words.text(.approveFromHome)
        case .status(409, _, _) where e.problem?.code == "cloud_not_linked": return words.text(.approveCloudNeedsRemote)
        case .status(409, _, _): return words.text(.alreadyApproved)
        case .status(404, _, _), .status(410, _, _): return words.text(.expired)
        case .status(422, _, _): return words.text(.notACode)
        default: return HomeFailure(e).message()
        }
    }
}
