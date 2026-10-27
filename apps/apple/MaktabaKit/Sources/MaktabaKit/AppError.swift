import Foundation

/// What went wrong, in the app's words: every failure a screen shows is sorted into one of a
/// few kinds (the same kinds as the web's `errorKind.ts`), and the screen says the kind's
/// sentence and offers its actions. Never the home's problem title or detail, an HTTP body
/// or a system error's text: those can name paths on the home's disk and are English only.
/// The status and the problem's code stay here for the logs.
public struct AppError: Error, Equatable, Sendable {
    public enum Kind: String, CaseIterable, Sendable {
        /// This device has no network.
        case offline
        /// The home did not answer (down, wrong address, another network).
        case unreachable
        /// The home answered too slowly.
        case timedOut
        /// A secure connection could not be made (a bad certificate, plain http refused).
        case insecure
        /// The sign-in ended: sign in again.
        case signedOut
        /// A username and password the home does not know.
        case wrongCredentials
        /// Signed in, but this account may not do that.
        case noAccess
        /// It is no longer on the home.
        case notFound
        /// The home is still preparing it (a transcode starting).
        case notReady
        /// The home is busy (429, 503): in a moment.
        case busy
        /// The home's disk is too full to prepare it (507).
        case diskFull
        /// The home refused what was asked (400, 409, 422).
        case invalid
        /// An answer this app does not understand (another release of the home).
        case badAnswer
        /// Something went wrong on the home.
        case server
        /// The screen went away: nothing to show.
        case cancelled
    }

    /// What a person can do about it, in the order a screen offers them.
    public enum Action: String, Sendable {
        case retry, signIn, chooseHome, back
    }

    public let kind: Kind
    /// The HTTP status (0: none, no answer came).
    public let status: Int
    /// The problem's machine code or type slug, for the logs (nil: none).
    public let code: String?

    public init(kind: Kind, status: Int = 0, code: String? = nil) {
        self.kind = kind
        self.status = status
        self.code = code
    }

    /// Sorts what a call threw into a kind.
    public init(_ error: Error) {
        switch error {
        case let e as AppError:
            self = e
        case let e as APIError:
            self = Self.classify(e)
        case let e as URLError:
            self.init(kind: Self.kind(of: e.code), code: "urlerror_\(e.code.rawValue)")
        case is CancellationError:
            self.init(kind: .cancelled)
        case is DecodingError:
            self.init(kind: .badAnswer)
        default:
            self.init(kind: .server)
        }
    }

    /// A failure signing in with a username and password: there a 401 is a wrong password,
    /// not a sign-in that ended.
    public static func signingIn(_ error: Error) -> AppError {
        let e = AppError(error)
        if e.kind == .signedOut {
            return AppError(kind: .wrongCredentials, status: e.status, code: e.code)
        }
        return e
    }

    /// Nothing for a screen to say: the request was cancelled because the screen moved on.
    public var isCancellation: Bool { kind == .cancelled }

    /// The actions a screen offers, the first the main one.
    public var actions: [Action] {
        switch kind {
        case .offline, .timedOut, .busy, .notReady, .server, .badAnswer, .diskFull: [.retry]
        case .unreachable: [.retry, .chooseHome]
        case .insecure: [.chooseHome, .retry]
        case .signedOut: [.signIn]
        case .wrongCredentials, .invalid: []
        case .noAccess, .notFound: [.back]
        case .cancelled: []
        }
    }

    /// The SF Symbol a screen shows with it.
    public var systemImage: String {
        switch kind {
        case .offline: "wifi.slash"
        case .unreachable, .timedOut: "wifi.exclamationmark"
        case .insecure: "lock.trianglebadge.exclamationmark"
        case .signedOut, .wrongCredentials, .noAccess: "person.crop.circle.badge.exclamationmark"
        case .notFound: "questionmark.folder"
        case .notReady, .busy: "hourglass"
        case .diskFull: "externaldrive.badge.exclamationmark"
        case .invalid, .badAnswer, .server, .cancelled: "exclamationmark.triangle"
        }
    }

    public func message(_ words: ErrorStrings = .forPreferredLanguages()) -> String {
        words.message(kind)
    }

    public func title(_ words: ErrorStrings = .forPreferredLanguages()) -> String {
        words.title(kind)
    }

    // MARK: sorting

    /// Problem type slugs (after `/problems/`) the streaming server answers while a
    /// transcode is still starting.
    static let notReadySlugs: Set<String> = ["manifest-not-found", "rendition-not-found"]
    static let unreachableSlugs: Set<String> = ["home-unreachable", "video-unavailable-offline"]

    static func classify(_ e: APIError) -> AppError {
        switch e {
        case .signedOut:
            return AppError(kind: .signedOut)
        case let .transport(code):
            return AppError(kind: kind(of: code), code: "urlerror_\(code.rawValue)")
        case .decoding:
            return AppError(kind: .badAnswer)
        case let .status(status, _):
            let p = e.problem
            let slug = problemSlug(p?.type)
            let code = p?.code ?? (slug.isEmpty ? nil : slug)
            return AppError(kind: kind(status: status, slug: slug, code: p?.code ?? ""), status: status, code: code)
        }
    }

    /// The kind of an HTTP answer: its status, problem type slug and machine code.
    static func kind(status: Int, slug: String = "", code: String = "") -> Kind {
        if unreachableSlugs.contains(slug) { return .unreachable }
        if status == 503 && code == "home_unreachable" { return .unreachable }
        if status == 504 && code == "home_timeout" { return .timedOut }
        if status == 507 || code == "insufficient_storage" { return .diskFull }
        switch status {
        case 0: return .unreachable
        case 401: return .signedOut
        case 403: return .noAccess
        case 404: return notReadySlugs.contains(slug) ? .notReady : .notFound
        case 410: return .notFound
        case 429, 503: return .busy
        // a relay or proxy in front of the home that could not reach it
        case 502: return .unreachable
        case 504: return .timedOut
        case 400, 409, 422: return .invalid
        default: return .server
        }
    }

    /// The kind of a URLSession failure.
    static func kind(of code: URLError.Code) -> Kind {
        switch code {
        case .cancelled:
            .cancelled
        case .notConnectedToInternet, .dataNotAllowed, .internationalRoamingOff, .callIsActive:
            .offline
        case .timedOut:
            .timedOut
        case .secureConnectionFailed, .serverCertificateHasBadDate, .serverCertificateUntrusted,
             .serverCertificateHasUnknownRoot, .serverCertificateNotYetValid,
             .clientCertificateRejected, .clientCertificateRequired, .appTransportSecurityRequiresSecureConnection:
            .insecure
        case .badServerResponse, .cannotParseResponse, .cannotDecodeRawData, .cannotDecodeContentData,
             .zeroByteResource:
            .badAnswer
        case .userAuthenticationRequired:
            .signedOut
        default:
            // cannotFindHost, cannotConnectToHost, dnsLookupFailed, networkConnectionLost…
            .unreachable
        }
    }

    /// The slug after `/problems/` in a problem type ("" for about:blank or none).
    static func problemSlug(_ type: String?) -> String {
        guard let type, let r = type.range(of: "/problems/", options: .backwards) else { return "" }
        let rest = type[r.upperBound...]
        return String(rest.prefix { $0 != "/" && $0 != "?" && $0 != "#" })
    }
}
