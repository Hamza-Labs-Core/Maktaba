import Foundation

/// Why the home did not give what a screen asked for, as every Maktaba app tells it (the
/// web, Android and the TVs use the same kinds and words).
public enum ErrorKind: Equatable, Hashable, Sendable {
    /// 404 (any other problem) or 410: it is gone.
    case notFound
    /// The stream's playlist is not written yet (404 `manifest-not-found` or
    /// `rendition-not-found`): the home is still preparing the video.
    case notReady
    /// 401: the sign-in ended (the refresh did not work either).
    case signedOut
    /// 403.
    case noAccess
    /// No answer, or the home's front door says the home behind it does not answer.
    case unreachable
    /// 429 or a 503: try again later (`retryAfter` seconds when the home said).
    case busy(retryAfter: Int?)
    /// Anything else: a 5xx, an unknown 4xx, an answer that did not decode.
    case server

    /// The kind of an answer with `status` and its problem+json (nil: not one).
    public static func of(status: Int, problem: Problem?, retryAfter: Int? = nil) -> ErrorKind {
        let slug = problem?.slug
        let code = problem?.code
        if slug == "home-unreachable" || slug == "video-unavailable-offline" { return .unreachable }
        switch status {
        case 401: return .signedOut
        case 403: return .noAccess
        case 404: return slug == "manifest-not-found" || slug == "rendition-not-found" ? .notReady : .notFound
        case 410: return .notFound
        case 429: return .busy(retryAfter: retryAfter)
        case 503: return code == "home_unreachable" ? .unreachable : .busy(retryAfter: retryAfter)
        case 504 where code == "home_timeout": return .unreachable
        default: return .server
        }
    }
}

/// What an error screen offers.
public enum ErrorAction: Hashable, Sendable {
    case tryAgain, signInAgain, chooseAnotherHome, back
}

// MARK: the home's answers when it refuses

/// A problem+json answer (RFC 7807: `{"type", "title", "status", "detail"}`, with the
/// home's `code` and `attempts_left`), read from `APIError.status`.
public struct Problem: Decodable, Equatable, Sendable {
    public let code: String?
    public let title: String?
    public let detail: String?
    public let attemptsLeft: Int?
    public let type: String?
    public let status: Int?

    public init(code: String? = nil, title: String? = nil, detail: String? = nil, attemptsLeft: Int? = nil,
                type: String? = nil, status: Int? = nil) {
        self.code = code
        self.title = title
        self.detail = detail
        self.attemptsLeft = attemptsLeft
        self.type = type
        self.status = status
    }

    enum CodingKeys: String, CodingKey {
        case code, title, detail, type, status
        case attemptsLeft = "attempts_left"
    }

    /// The type's name after `/problems/` ("rendition-not-found"): the domain before it
    /// differs between homes.
    public var slug: String? {
        guard let type, let r = type.range(of: "/problems/") else { return nil }
        let rest = type[r.upperBound...].prefix { $0 != "/" && $0 != "?" && $0 != "#" }
        return rest.isEmpty ? nil : String(rest).lowercased()
    }

    /// The problem in `body`, when it is one.
    public static func parse(_ body: String) -> Problem? {
        try? JSONDecoder().decode(Problem.self, from: Data(body.utf8))
    }
}

public extension APIError {
    /// The home's problem, when it answered with one.
    var problem: Problem? {
        switch self {
        case let .status(_, body, _), let .notReady(body): Problem.parse(body)
        default: nil
        }
    }

    /// The HTTP status the home answered (nil: no answer, or not one to tell).
    var httpStatus: Int? {
        switch self {
        case let .status(code, _, _): code
        case .notReady: 404
        case .signedOut: 401
        default: nil
        }
    }

    var kind: ErrorKind {
        switch self {
        case .transport: .unreachable
        case let .status(code, body, retryAfter): .of(status: code, problem: Problem.parse(body), retryAfter: retryAfter)
        case .decoding: .server
        case .signedOut: .signedOut
        case .notReady: .notReady
        }
    }

    /// What to tell a person: one sentence for the kind, never the home's own answer (that
    /// is for the Details and the logs).
    var message: String { errorDescription ?? ErrorStrings.forPreferredLanguages().text(.server) }
}

/// An error as a screen shows it: its kind, and what the home said for the Details.
public struct HomeFailure: Equatable, Sendable {
    public let kind: ErrorKind
    public let status: Int?
    public let slug: String?
    public let title: String?
    public let detail: String?
    /// The answer as the home sent it, cut to 2 KB: the Details and the logs only.
    public let raw: String?

    public init(kind: ErrorKind, status: Int? = nil, slug: String? = nil, title: String? = nil,
                detail: String? = nil, raw: String? = nil) {
        self.kind = kind
        self.status = status
        self.slug = slug
        self.title = title
        self.detail = detail
        self.raw = raw.map { String($0.prefix(2048)) }
    }

    public init(_ error: Error) {
        switch error {
        case let e as APIError:
            let p = e.problem
            let raw: String? = switch e {
            case let .status(_, body, _), let .notReady(body): body.isEmpty ? nil : body
            case let .transport(why), let .decoding(why): why
            case .signedOut: nil
            }
            self.init(kind: e.kind, status: e.httpStatus, slug: p?.slug, title: p?.title, detail: p?.detail,
                      raw: p == nil ? raw : nil)
        case let e as URLError:
            self.init(kind: .unreachable, detail: e.localizedDescription)
        case let e as NSError where e.domain == NSURLErrorDomain:
            self.init(kind: .unreachable, detail: e.localizedDescription)
        default:
            self.init(kind: .server, detail: error.localizedDescription)
        }
    }

    /// The one sentence a screen shows (`video`: about the video being played).
    public func message(_ words: ErrorStrings = .forPreferredLanguages(), video: Bool = false) -> String {
        words.sentence(kind, video: video)
    }

    /// What the Details show: the status and the problem's type, title and detail (or the
    /// raw answer when it was not a problem). nil: nothing more to tell.
    public var details: String? {
        var lines: [String] = []
        let head = [status.map(String.init), slug].compactMap { $0 }.joined(separator: " · ")
        if !head.isEmpty { lines.append(head) }
        if let title, !title.isEmpty { lines.append(title) }
        if let detail, !detail.isEmpty { lines.append(detail) }
        if let raw, !raw.isEmpty { lines.append(raw) }
        return lines.isEmpty ? nil : lines.joined(separator: "\n")
    }

    /// The player's actions, the first one focused: Try again where waiting may help, Sign
    /// in again after a 401, another home when this one does not answer, Back always.
    public var actions: [ErrorAction] {
        switch kind {
        case .signedOut: [.signInAgain, .back]
        case .unreachable: [.tryAgain, .chooseAnotherHome, .back]
        case .notReady, .busy, .server: [.tryAgain, .back]
        case .notFound, .noAccess: [.back]
        }
    }

    /// The SF Symbol for the kind.
    public var symbol: String {
        switch kind {
        case .notFound: "questionmark.square.dashed"
        case .notReady: "hourglass"
        case .signedOut: "person.crop.circle.badge.exclamationmark"
        case .noAccess: "lock"
        case .unreachable: "wifi.exclamationmark"
        case .busy: "clock"
        case .server: "exclamationmark.triangle"
        }
    }
}
