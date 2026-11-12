import Foundation
import Observation

/// Where a request's follower asks (the home's client; a fake in tests).
public protocol OwnRequestsSource: AnyObject, Sendable {
    /// `GET /api/me/parental/requests`; nil on a home from before the route.
    func myParentalRequests() async throws -> [OwnRequest]?
}

extension APIClient: OwnRequestsSource {}

/// What became of the child's own request for one thing, as the blocked page and the
/// time's-up screen show it (Epic 57 stories 57.05 and 57.06): the home's list of the
/// caller's requests is read, and the newest one for this thing decides.
///
/// - While it waits (`pending`) the list is read every 15 seconds, for as long as the screen
///   runs `follow` (the screen runs it only while the app is in front). The reading stops
///   when nothing waits, and after 30 minutes with no change; `check` reads once more
///   whenever the child asks (Check again).
/// - `approved` counts only when this screen saw the request waiting (`approvals` moves
///   then, and the screen opens the item or plays on): an approval from days ago is not news.
/// - `denied` is said, with Ask again and the PIN. For time, a denial is dropped once the
///   request's `expires_at` has passed.
/// - `expired`, and a status this app does not know: the ask buttons again.
/// - A home from before the route (404 or 405) is `unsupported`: asked once, never again,
///   and the screen keeps what it did before (a request just made stays "waiting").
///
/// The device keeps nothing: every answer of the home overwrites what was shown.
@MainActor @Observable
public final class RequestFollow {
    /// What is followed: one item, or more watching time (any number of minutes).
    public enum Match: Equatable, Sendable {
        case item(ParentalSubject)
        case time

        public init(_ subject: ParentalSubject) {
            self = subject.kind == "time" ? .time : .item(subject)
        }

        func takes(_ r: OwnRequest) -> Bool {
            switch self {
            case .time: r.isTime
            case let .item(subject): r.subjectKind == subject.kind && r.subjectID == subject.id
            }
        }
    }

    public enum Outcome: Equatable, Sendable {
        /// No request for this (or one whose answer is no longer news): the ask buttons.
        case none
        case pending
        /// Approved while this screen watched.
        case approved
        case denied
        case expired
    }

    /// How often the list is read while a request waits.
    public nonisolated static let every: Duration = .seconds(15)
    /// With no change for this long the reading stops (Check again still reads).
    public nonisolated static let patience: TimeInterval = 30 * 60

    public let match: Match
    /// How many approvals this screen saw arrive: the screen acts when it moves.
    public private(set) var approvals = 0
    /// The home has no such route: nothing more is asked of it.
    public private(set) var unsupported = false
    /// A read is under way (Check again shows it).
    public private(set) var checking = false
    /// The reading stopped after 30 minutes with no change.
    public private(set) var rested = false

    private var stored = Outcome.none
    /// When a denial stops being shown (a time request's `expires_at`).
    private var deniedUntil: Date?

    @ObservationIgnored private let source: any OwnRequestsSource
    @ObservationIgnored private let every: Duration
    @ObservationIgnored private let sleep: @Sendable (Duration) async throws -> Void
    @ObservationIgnored private let now: @Sendable () -> Date
    /// The requests this screen saw waiting: only their approval counts.
    @ObservationIgnored private var seenPending: Set<String> = []
    /// The approved requests already acted on.
    @ObservationIgnored private var acted: Set<String> = []
    @ObservationIgnored private var changedAt: Date
    @ObservationIgnored private var readAt: Date?

    public init(source: any OwnRequestsSource, match: Match, every: Duration = RequestFollow.every,
                sleep: @escaping @Sendable (Duration) async throws -> Void = { try await Task.sleep(for: $0) },
                now: @escaping @Sendable () -> Date = { Date() }) {
        self.source = source
        self.match = match
        self.every = every
        self.sleep = sleep
        self.now = now
        changedAt = now()
    }

    /// What the screen shows now.
    public var outcome: Outcome {
        if stored == .denied, let deniedUntil, now() >= deniedUntil { return .none }
        return stored
    }

    /// The child just asked and the home took the request: it waits, as of now.
    public func asked(_ request: ParentalRequest) {
        seenPending.insert(request.id)
        rested = false
        readAt = now()
        set(.pending, deniedUntil: nil)
        changedAt = now()
    }

    /// Reads the home's list once (Check again, and each turn of `follow`). A failure keeps
    /// what was shown.
    @discardableResult
    public func check() async -> Outcome {
        guard !unsupported else { return outcome }
        checking = true
        defer { checking = false }
        do {
            guard let list = try await source.myParentalRequests() else {
                unsupported = true
                return outcome
            }
            readAt = now()
            apply(list.first(where: match.takes))
        } catch {
            if !Log.isCancellation(error) { Log.failure("own parental requests", error, logger: Log.network) }
        }
        return outcome
    }

    /// Follows the request while the screen is in front: reads now (unless the home was
    /// read within the last 15 seconds), then every 15 seconds while a request waits. Ends
    /// when nothing waits, after 30 minutes with no change, on an older home, and when the
    /// task is cancelled (the app left the front, the screen closed).
    public func follow() async {
        guard !unsupported else { return }
        if readAt.map({ now().timeIntervalSince($0) >= every.seconds }) ?? true { await check() }
        while !Task.isCancelled, !unsupported, stored == .pending {
            if now().timeIntervalSince(changedAt) >= Self.patience {
                rested = true
                return
            }
            do { try await sleep(every) } catch { return }
            await check()
        }
    }

    private func apply(_ request: OwnRequest?) {
        guard let request else {
            set(.none, deniedUntil: nil)
            return
        }
        switch request.status {
        case .pending:
            seenPending.insert(request.id)
            set(.pending, deniedUntil: nil)
        case .approved:
            guard seenPending.contains(request.id) else {
                set(.none, deniedUntil: nil)
                return
            }
            set(.approved, deniedUntil: nil)
            if acted.insert(request.id).inserted { approvals += 1 }
        case .denied:
            // a denied time request is said only until it would have expired anyway
            let until = request.isTime ? request.expiresAt : nil
            if let until, now() >= until {
                set(.none, deniedUntil: nil)
            } else {
                set(.denied, deniedUntil: until)
            }
        case .expired:
            set(.expired, deniedUntil: nil)
        }
    }

    private func set(_ outcome: Outcome, deniedUntil until: Date?) {
        if deniedUntil != until { deniedUntil = until }
        guard stored != outcome else { return }
        stored = outcome
        changedAt = now()
        if outcome == .pending { rested = false }
    }
}

private extension Duration {
    var seconds: TimeInterval {
        TimeInterval(components.seconds) + TimeInterval(components.attoseconds) / 1e18
    }
}
