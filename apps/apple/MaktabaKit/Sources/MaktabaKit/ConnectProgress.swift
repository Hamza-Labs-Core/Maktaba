import Foundation

// Epic 48 story 48.37: every wait on a connect, sign-in or reconnect screen says what is
// happening now. The connect code (`AppSession.connect`, `AppSession` restoring the saved
// sign-in, `CodeSignIn`) publishes one `ConnectProgress` step by step; the screens draw it
// with `ConnectProgressView`, never a bare spinner.

/// What connecting to a home is doing right now, and which attempt it is.
public struct ConnectProgress: Equatable, Sendable {
    public enum Step: Equatable, Sendable {
        /// Starting to look for the home (no address asked yet).
        case finding
        /// None of a saved home's addresses answered: it is looked for among the homes
        /// announced on this network (mDNS), to be asked where it is announced.
        case lookingNearby
        /// Asking `address` whether it is the home: the `index`th (from 1) of `count`.
        case trying(address: String, index: Int, count: Int)
        /// An address answered: this is the home.
        case found(name: String)
        /// Signing in to the home (a password sent, a code asked for, an approval collected).
        case signingIn(home: String)
        /// A code shows until a signed-in device approves it, or it expires at `expiresAt`.
        case awaitingApproval(code: String, expiresAt: Date)
        /// At launch (or on picking a known home): the saved sign-in is checked with the home.
        case reconnecting(name: String)
        /// The home is busy or still starting (a 503 or 429 with Retry-After): asked again
        /// by itself at `retryAt`.
        case homeBusy(retryAt: Date)
        /// It did not work: why, and the addresses tried (empty: no address was involved).
        case failed(reason: Failure, tried: [String])
    }

    /// Why connecting or signing in did not work, as a person is told it.
    public enum Failure: Equatable, Sendable {
        /// No address answered (the home is off, on another network, a wrong port).
        case unreachable
        /// Something answered, but not a Maktaba home.
        case notAHome
        /// A Maktaba home answered, but a different one than the link or the saved home names.
        case otherHome
        /// The home refused the username and password.
        case wrongPassword
        /// The sign-in code expired before anyone approved it.
        case approvalExpired
        /// The home is busy and did not say when to ask again.
        case busy
        /// Anything else the home answered (a 5xx, an answer that did not decode).
        case server
    }

    public var step: Step
    /// Which attempt this is, from 1: a Retry (or an automatic retry after Retry-After, or a
    /// new code after one expired) adds one.
    public var attempt: Int

    public init(_ step: Step, attempt: Int = 1) {
        self.step = step
        self.attempt = max(1, attempt)
    }

    /// Still working (false: it failed and waits for Retry).
    public var isWorking: Bool {
        if case .failed = step { false } else { true }
    }

    /// The moment a countdown on screen runs to (a code's expiry, the next try), if any.
    public var deadline: Date? {
        switch step {
        case let .awaitingApproval(_, at), let .homeBusy(at): at
        default: nil
        }
    }

    /// The failure a reach failure is.
    public static func failure(_ f: ReachFailure) -> Failure {
        switch f {
        case .unreachable: .unreachable
        case .notAHome: .notAHome
        case .otherHome: .otherHome
        }
    }

    /// The failure a home's answer (or no answer) is.
    public static func failure(_ f: HomeFailure) -> Failure {
        switch f.kind {
        case .unreachable: .unreachable
        case .busy: .busy
        default: .server
        }
    }

    /// "1:05": what is left until `deadline` at `now`, never below zero.
    public static func countdown(to deadline: Date, now: Date) -> String {
        let left = max(0, Int(deadline.timeIntervalSince(now).rounded(.up)))
        return String(format: "%d:%02d", left / 60, left % 60)
    }
}

/// The time the connect code reads and waits on: the system's, or a test's fake one that
/// waits on a latch instead of the wall clock.
public struct ConnectClock: Sendable {
    public var now: @Sendable () -> Date
    public var sleep: @Sendable (Duration) async throws -> Void

    public init(now: @escaping @Sendable () -> Date, sleep: @escaping @Sendable (Duration) async throws -> Void) {
        self.now = now
        self.sleep = sleep
    }

    public static let system = ConnectClock(now: { Date() }, sleep: { try await Task.sleep(for: $0) })
}

extension URL {
    /// An address as the progress shows it: the whole URL without a trailing slash (the
    /// scheme stays: http and https of one host are two tries).
    var progressAddress: String {
        var s = absoluteString
        while s.hasSuffix("/") { s.removeLast() }
        return s
    }
}
