import Foundation
import Observation

/// Where the store asks (the home's client; a fake in tests).
public protocol ParentalSource: AnyObject, Sendable {
    func myParental() async throws -> ParentalLimit?
    func parentalChildren() async throws -> [ParentalChild]
    func myParentalTime(library: String?) async throws -> ParentalTime?
}

extension APIClient: ParentalSource {}

/// The signed-in account's parental state, kept by the session (`AppSession.parental`) as
/// its capabilities are: asked at sign-in, when the app comes to the front and every 5
/// minutes (Epic 57 story 57.05).
///
/// - `current`: the limit in force (none on a home from before Epic 57, and until the first
///   answer; the home hides what it hides whatever the app knows).
/// - `canManage`: an admin, or a guardian of at least one child: the Player's Family section
///   shows. Never for a restricted account.
/// - `epoch`: moves each time the limit changes after the first answer (its `version`, the
///   tier in force, the device's limit). The screens key what they loaded on it, so nothing
///   hidden lingers and what a guardian just allowed appears.
/// - `time`: the home's answer about watching time, with when it was read (57.15).
///
/// A failure keeps the last answer: an app never shows more because the home did not answer,
/// since the home sends only what the account may see.
@MainActor @Observable
public final class ParentalStore {
    public private(set) var current: ParentalLimit = .none
    public private(set) var canManage = false
    public private(set) var epoch = 0
    /// The home answered once (a 404 counts).
    public private(set) var answered = false
    public private(set) var time: TimeReading?

    /// The home's time answer and when this device got it. The countdown shown between two
    /// answers is the home's number less the time since; the app counts nothing of its own.
    public struct TimeReading: Equatable, Sendable {
        public let state: ParentalTime
        public let at: Date

        public init(_ state: ParentalTime, at: Date) {
            self.state = state
            self.at = at
        }
    }

    @ObservationIgnored private var source: (any ParentalSource)?
    @ObservationIgnored private var isAdmin = false
    @ObservationIgnored private var generation = 0
    @ObservationIgnored private var timer: Task<Void, Never>?
    /// The read under way, if any: one at a time (the 5-minute timer, the app coming to the
    /// front, a blocked page's poll and the age signal's answer can all ask at once), so an
    /// older answer never lands after a newer one.
    @ObservationIgnored private var reading: Task<Void, Never>?
    /// Someone asked while a read was under way: one more read follows it.
    @ObservationIgnored private var askedMeanwhile = false
    /// The home has no parental routes (404): nothing more is asked of it.
    @ObservationIgnored private var unsupported = false
    @ObservationIgnored private var timeUnsupported = false
    /// Called when the limit changed (`epoch` moved): the app drops what it kept for the account.
    @ObservationIgnored public var onChange: (@MainActor () -> Void)?

    public let every: Duration
    private let sleep: @Sendable (Duration) async throws -> Void
    private let now: @Sendable () -> Date
    private let defaults: UserDefaults
    /// Where "this sign-in was limited when last asked" is kept on the device (`key`).
    @ObservationIgnored private var kept: String?

    public init(every: Duration = .seconds(300), defaults: UserDefaults = .standard,
                sleep: @escaping @Sendable (Duration) async throws -> Void = { try await Task.sleep(for: $0) },
                now: @escaping @Sendable () -> Date = { Date() }) {
        self.every = every
        self.defaults = defaults
        self.sleep = sleep
        self.now = now
    }

    /// The defaults key for `user` at `home`.
    public nonisolated static func key(home: URL, user: String) -> String {
        "parental.limited.\(home.absoluteString)#\(user)"
    }

    /// Whether `user` at `home` was limited when the home last said (false: never asked).
    /// The Player shows nothing it kept on the device for a limited sign-in before the home
    /// has answered again: a limit may have tightened while the app was closed.
    public nonisolated static func wasLimited(home: URL, user: String, defaults: UserDefaults = .standard) -> Bool {
        defaults.bool(forKey: key(home: home, user: user))
    }

    /// A home was signed in to: forget the last account's answer and ask this one. `key`
    /// (`ParentalStore.key`) keeps whether the sign-in is limited for the next launch.
    public func use(_ source: any ParentalSource, isAdmin: Bool, key: String? = nil) {
        reset()
        kept = key
        self.source = source
        self.isAdmin = isAdmin
        // an admin manages before the first answer; anyone else once the home lists a child
        canManage = isAdmin
    }

    /// Signed out or another home.
    public func reset() {
        timer?.cancel()
        timer = nil
        source = nil
        kept = nil
        isAdmin = false
        generation += 1
        // a read under way is the last sign-in's: its answer is dropped (`generation`)
        reading = nil
        askedMeanwhile = false
        unsupported = false
        timeUnsupported = false
        if current != .none { current = .none }
        if canManage { canManage = false }
        if answered { answered = false }
        if time != nil { time = nil }
    }

    /// Asks now, then every `every` until `reset` or another `use`.
    public func start() {
        timer?.cancel()
        guard source != nil else { return }
        let sleep = sleep, every = every
        timer = Task { [weak self] in
            await self?.refresh()
            while !Task.isCancelled {
                do { try await sleep(every) } catch { return }
                await self?.refresh()
            }
        }
    }

    /// Asks the home: the limit, whether the caller looks after anyone, the time. One read
    /// at a time: a call made while one is under way waits for it and for one more read
    /// after it (the home is asked again, since what the caller knows may be newer than that
    /// read's question), so every caller returns with an answer asked for after its call,
    /// and answers are applied in the order they were asked for.
    public func refresh() async {
        if let reading {
            askedMeanwhile = true
            await reading.value
            return
        }
        let mine = generation
        let task = Task { [weak self] in
            repeat {
                self?.askedMeanwhile = false
                await self?.read()
            } while self?.askedMeanwhile == true && self?.generation == mine
            if self?.generation == mine { self?.reading = nil }
        }
        reading = task
        await task.value
    }

    /// Asks the home once.
    private func read() async {
        guard let source, !unsupported else {
            if !answered, source != nil { answered = true }
            return
        }
        let mine = generation
        do {
            let limit = try await source.myParental()
            guard mine == generation else { return }
            guard let limit else {
                // a home from before Epic 57: no limit, no Family, nothing more asked
                unsupported = true
                apply(.none)
                if canManage { canManage = false }
                answered = true
                return
            }
            apply(limit)
            if limit.restricted {
                if canManage { canManage = false }
            } else if !isAdmin {
                let children = (try? await source.parentalChildren()) ?? []
                guard mine == generation else { return }
                let manages = !children.isEmpty
                if manages != canManage { canManage = manages }
            }
        } catch {
            if Log.isCancellation(error) { return }
            Log.failure("parental limit", error, logger: Log.network)
            guard mine == generation else { return }
        }
        if !answered { answered = true }
        await refreshTime()
    }

    /// Asks the home how much watching time is left (57.15). Only a restricted account is
    /// ever limited, so no one else's app asks.
    public func refreshTime() async {
        guard let source, current.restricted, !timeUnsupported else {
            if time != nil, !current.restricted { time = nil }
            return
        }
        let mine = generation
        do {
            let t = try await source.myParentalTime(library: nil)
            guard mine == generation else { return }
            guard let t else {
                timeUnsupported = true
                time = nil
                return
            }
            time = TimeReading(t, at: now())
        } catch {
            if Log.isCancellation(error) { return }
            Log.failure("parental time", error, logger: Log.network)
        }
    }

    private func apply(_ limit: ParentalLimit) {
        let first = !answered
        // limited now and not when this device last asked (the limit came while the app was
        // closed): what the device kept from before is dropped, as on any change
        var newlyLimited = false
        if let kept {
            newlyLimited = first && limit.limited && !defaults.bool(forKey: kept)
            if defaults.bool(forKey: kept) != limit.limited { defaults.set(limit.limited, forKey: kept) }
        }
        guard limit != current || newlyLimited else { return }
        current = limit
        if !limit.restricted, time != nil { time = nil }
        // otherwise the first answer changes nothing on screen: the home sent only what is allowed
        guard !first || newlyLimited else { return }
        epoch += 1
        onChange?()
    }
}

// MARK: the time of what plays (57.15)

/// The watching time of what plays, asked of the home for the playing video's library: a
/// rule on one library counts only when the home is told the library, so the answer without
/// it (`ParentalStore.time`, Home's line) can say "allowed" while that library's time is over.
///
/// - Over the video (`follow`): asked now and every 30 s; `reading` feeds the countdown and
///   the warnings.
/// - After the home stopped the video for time (`waitUntilAllowed`): never asked at once (the
///   home just said no, and an answer for the wrong question must not reopen the stream),
///   only after a full wait, then every 30 s, and it says yes only when the home's answer
///   for this library is "allowed".
@MainActor @Observable
public final class PlayingTime {
    /// How often the home is asked.
    public nonisolated static let every: Duration = .seconds(30)

    public private(set) var reading: ParentalStore.TimeReading?
    public let library: String?

    @ObservationIgnored private let source: any ParentalSource
    @ObservationIgnored private let every: Duration
    @ObservationIgnored private let sleep: @Sendable (Duration) async throws -> Void
    @ObservationIgnored private let now: @Sendable () -> Date
    /// One full wait has passed since the stop: only then may the home be asked.
    @ObservationIgnored private var waited = false

    public init(source: any ParentalSource, library: String?, every: Duration = PlayingTime.every,
                sleep: @escaping @Sendable (Duration) async throws -> Void = { try await Task.sleep(for: $0) },
                now: @escaping @Sendable () -> Date = { Date() }) {
        self.source = source
        self.library = library
        self.every = every
        self.sleep = sleep
        self.now = now
    }

    /// Asks the home once. Nil when it could not say (or has no time rules: a home from
    /// before them); the last reading is kept then.
    @discardableResult
    public func ask() async -> ParentalTime? {
        do {
            guard let t = try await source.myParentalTime(library: library) else { return nil }
            reading = ParentalStore.TimeReading(t, at: now())
            return t
        } catch {
            if !Log.isCancellation(error) { Log.failure("parental time", error, logger: Log.network) }
            return nil
        }
    }

    /// Over the video: asks now, then every 30 s until the task is cancelled.
    public func follow() async {
        while !Task.isCancelled {
            await ask()
            do { try await sleep(every) } catch { return }
        }
    }

    /// After a stop for time: true once the home says there is time again for this library
    /// (a parent gave some, the slot opened); false when the task was cancelled. The first
    /// question comes only after one full wait; called again later (the app came back to
    /// the front), it asks at once only if that wait already passed.
    public func waitUntilAllowed() async -> Bool {
        while !Task.isCancelled {
            if !waited {
                do { try await sleep(every) } catch { return false }
                waited = true
            }
            if let t = await ask(), t.allowed { return true }
            do { try await sleep(every) } catch { return false }
        }
        return false
    }
}

// MARK: the countdown (57.15)

/// What the time shows, worked out from the home's answer alone.
public enum TimeLeft {
    /// "Time left" shows once this little remains.
    public static let showsUnder = 15 * 60
    /// The warnings: 5 minutes and 1 minute before the end.
    public static let warnings = [300, 60]

    /// Seconds left at `now`: the home's number less the time since it answered (nil: no
    /// rule limits the account now).
    public static func seconds(_ reading: ParentalStore.TimeReading?, at now: Date = .now) -> Int? {
        guard let reading, reading.state.limited, reading.state.allowed, let left = reading.state.remainingSeconds else { return nil }
        return max(0, left - Int(now.timeIntervalSince(reading.at).rounded(.down)))
    }

    /// The line on Home and in the player ("12 min left today"), only in the last 15 minutes.
    public static func line(_ reading: ParentalStore.TimeReading?, at now: Date = .now, _ locale: Locale = AppLanguage.locale) -> String? {
        guard let left = seconds(reading, at: now), left <= showsUnder else { return nil }
        return ParentalWords.text("parental.time.left", locale, ParentalWords.length(seconds: left, locale))
    }

    /// The warning due when the time left went from `before` to `after` seconds: the
    /// threshold crossed (300 or 60), nil when none was.
    public static func warning(from before: Int?, to after: Int?) -> Int? {
        guard let after else { return nil }
        return warnings.last { after <= $0 && (before ?? Int.max) > $0 }
    }

    /// A warning's sentence ("5 min left today").
    public static func warningLine(_ threshold: Int, _ locale: Locale = AppLanguage.locale) -> String {
        ParentalWords.text("parental.time.left", locale, ParentalWords.length(seconds: threshold, locale))
    }

    /// The home's answer says the time is over.
    public static func isUp(_ reading: ParentalStore.TimeReading?) -> Bool {
        guard let s = reading?.state else { return false }
        return !s.allowed
    }
}

/// The time's-up screen's words for a refusal (`parental-time`) or the home's time answer.
public struct TimeUp: Equatable, Sendable {
    /// `outside_slot`, `limit_reached`, `no_heartbeat`.
    public let reason: String
    public let nextAllowedAt: Date?

    public init(reason: String, nextAllowedAt: Date?) {
        self.reason = reason
        self.nextAllowedAt = nextAllowedAt
    }

    /// From a refusal of anything the home gates by time (a stream, a watch, a channel's
    /// access or play): 403 `parental-time`. Nil for any other answer.
    public init?(_ error: Error) {
        guard case let .time(reason, next)? = ParentalRefusal(error) else { return nil }
        self.init(reason: reason, nextAllowedAt: next)
    }

    /// From the home's time answer, when it says not allowed.
    public init?(_ state: ParentalTime) {
        guard !state.allowed else { return nil }
        self.init(reason: state.reason ?? "limit_reached", nextAllowedAt: state.nextAllowedAt)
    }

    public func title(_ locale: Locale = AppLanguage.locale) -> String {
        ParentalWords.text(reason == "outside_slot" ? "parental.time.outside.title" : "parental.time.up.title", locale)
    }

    /// "You can watch again at 7:00 AM" (with the day when it is not today), nil when the
    /// home named no time.
    public func nextLine(_ locale: Locale = AppLanguage.locale, now: Date = .now, calendar: Calendar = .current) -> String? {
        guard let next = nextAllowedAt else { return nil }
        var style = Date.FormatStyle(date: calendar.isDate(next, inSameDayAs: now) ? .omitted : .abbreviated, time: .shortened)
        style.locale = locale
        style.timeZone = calendar.timeZone
        return ParentalWords.text("parental.time.next", locale, next.formatted(style))
    }
}

/// What a player does with the home's answer to a stream it asked for, or to a progress
/// post (the stream's heartbeat), for a parental reason. One place, so iPhone, iPad and
/// Apple TV decide alike; a player never tries again after `timeUp` or `blocked`.
public enum PlaybackVerdict: Equatable, Sendable {
    /// Not a parental answer: the player's own handling.
    case other
    /// 403 `parental-time`: stop, show the time's-up screen.
    case timeUp(TimeUp)
    /// 404 under a parental limit: the home no longer gives the video; stop, show the
    /// blocked page (the home does not tell a hidden video from a missing one).
    case blocked
    /// 403 `parental-time` with `no_heartbeat` on a progress post: the stream sent no
    /// progress for a minute (a long pause, the app held in the background). Open the stream
    /// again once; the home then says whether there is time.
    case reopen

    /// For a stream open or renewal that failed.
    public static func open(_ error: Error, limited: Bool) -> PlaybackVerdict {
        if case let .time(reason, next)? = ParentalRefusal(error) { return .timeUp(TimeUp(reason: reason, nextAllowedAt: next)) }
        if limited, HomeFailure(error).kind == .notFound { return .blocked }
        return .other
    }

    /// For a progress post the home refused. A 404 under a limit is the home saying the video
    /// is now hidden from the account: the stream has ended.
    public static func progress(_ error: Error, limited: Bool = false) -> PlaybackVerdict {
        if limited, case APIError.status(404, _, _)? = error as? APIError { return .blocked }
        guard case let .time(reason, next)? = ParentalRefusal(error) else { return .other }
        return reason == "no_heartbeat" ? .reopen : .timeUp(TimeUp(reason: reason, nextAllowedAt: next))
    }
}

// MARK: the blocked page (57.05) and Ask a parent (57.06)

/// The blocked page for one thing the home would not give ("This isn't available"): its
/// reason line, Ask a parent and Enter parent PIN. It never has a title, poster or rating of
/// what was asked for: the home sends none, and the page is given only the id the child
/// already had.
///
/// Ask a parent and the PIN are a restricted account's (the home answers 403
/// `not_restricted` to anyone else); a sign-in limited only by its device sees the reason.
@MainActor @Observable
public final class BlockedPage {
    public enum Ask: Equatable {
        case idle, sending
        /// The request waits for a parent (just made, or made before).
        case pending
        /// 20 requests already wait.
        case tooMany
        case failed
    }

    public let subject: ParentalSubject
    public let limit: ParentalLimit
    private let client: APIClient
    /// What became of the request for this (the home's list of the child's own requests).
    public let follow: RequestFollow

    public private(set) var ask = Ask.idle
    /// The people with a parent PIN (empty: Enter parent PIN is not offered).
    public fileprivate(set) var unlockers: [ParentalPerson] = []

    public init(client: APIClient, subject: ParentalSubject, limit: ParentalLimit, follow: RequestFollow? = nil) {
        self.client = client
        self.subject = subject
        self.limit = limit
        self.follow = follow ?? RequestFollow(source: client, match: .init(subject))
    }

    public var canAsk: Bool { limit.restricted }
    public var canEnterPIN: Bool { limit.restricted && !unlockers.isEmpty }

    /// A request for this waits for a parent (just made, or made before: the home's list says).
    public var waiting: Bool { follow.outcome == .pending }
    /// A parent said no: said, with Ask again and the PIN.
    public var denied: Bool { follow.outcome == .denied }
    /// The ask button takes a press: not while a request goes out or waits, nor with 20 waiting.
    public var canPressAsk: Bool { canAsk && !waiting && ask != .sending && ask != .tooMany }

    /// The ask button's words: waiting, Ask again after a no, else `first` (the screen's own:
    /// "Ask a parent", "Ask for more time").
    public func askKey(_ first: String) -> String {
        waiting ? "parental.ask.pending" : denied ? "parental.ask.again" : first
    }

    /// Another thing to ask for from the same page (the time's-up screen's minutes); what
    /// became of it is followed as this page's is.
    public func asking(_ subject: ParentalSubject) -> BlockedPage {
        let page = BlockedPage(client: client, subject: subject, limit: limit, follow: follow)
        page.unlockers = unlockers
        return page
    }
    public var reasonLine: String? { limit.reasonLine() }

    /// Who can unlock with a PIN; quiet on failure (the button stays away).
    public func load() async {
        guard limit.restricted else { return }
        if let people = try? await client.parentalUnlockers() { unlockers = people }
    }

    public func askParent() async {
        guard canAsk, ask != .sending else { return }
        ask = .sending
        do {
            follow.asked(try await client.askParent(subject))
            ask = .pending
        } catch {
            if Log.isCancellation(error) {
                ask = .idle
                return
            }
            ask = ParentalRefusal(error) == .tooManyRequests ? .tooMany : .failed
        }
    }

    /// The line under the buttons for the ask's state.
    public func askLine(_ locale: Locale = AppLanguage.locale) -> String? {
        if waiting { return ParentalWords.text("parental.ask.sent", locale) }
        switch ask {
        case .tooMany: return ParentalWords.text("parental.ask.tooMany", locale)
        case .failed: return ParentalWords.text("parental.ask.failed", locale)
        case .idle, .sending, .pending: break
        }
        return denied ? ParentalWords.text("parental.ask.denied", locale) : nil
    }
}

/// Entering a guardian's six-digit PIN on the child's device. The home checks it; the
/// device holds nothing that can.
@MainActor @Observable
public final class PinEntry {
    public enum State: Equatable {
        case entering, checking
        case wrong
        /// PIN entry is closed until then.
        case locked(until: Date)
        case unlocked
        case failed
    }

    public nonisolated static let length = 6

    public let subject: ParentalSubject
    public let guardian: ParentalPerson
    private let client: APIClient
    private let now: () -> Date
    private let sleep: @Sendable (Duration) async throws -> Void

    public private(set) var digits = ""
    public private(set) var state = State.entering

    public init(client: APIClient, subject: ParentalSubject, guardian: ParentalPerson,
                now: @escaping () -> Date = { Date() },
                sleep: @escaping @Sendable (Duration) async throws -> Void = { try await Task.sleep(for: $0) }) {
        self.client = client
        self.subject = subject
        self.guardian = guardian
        self.sleep = sleep
        self.now = now
    }

    /// Whether PIN entry is closed now, and until when.
    public var lockedUntil: Date? {
        if case let .locked(until) = state { return until }
        return nil
    }

    /// While PIN entry is closed: waits until the lock ends, then opens it again (the state
    /// goes back to entering, so the screen's keys take the remote or the finger again
    /// without the sheet being closed). The screen runs it for as long as it shows; a
    /// cancelled wait changes nothing.
    public func reopenWhenLockEnds() async {
        guard case let .locked(until) = state else { return }
        while now() < until {
            do { try await sleep(.seconds(max(1, until.timeIntervalSince(now()).rounded(.up)))) } catch { return }
        }
        if state == .locked(until: until) { state = .entering }
    }

    public var canType: Bool {
        switch state {
        case .entering, .wrong, .failed: true
        case let .locked(until): now() >= until
        case .checking, .unlocked: false
        }
    }

    /// A key of the pad: a digit is added (Western digits, whatever the keyboard's).
    public func press(_ digit: Int) {
        guard canType, (0...9).contains(digit), digits.count < Self.length else { return }
        if state != .entering { state = .entering }
        digits.append(String(digit))
    }

    public func delete() {
        guard canType, !digits.isEmpty else { return }
        digits.removeLast()
    }

    public var complete: Bool { digits.count == Self.length }

    /// What VoiceOver reads for the dots ("3 of 6 digits entered"): both numbers in the
    /// language's own digits (a literal 6 beside a formatted count read as two scripts in
    /// Arabic).
    public func progressLabel(_ locale: Locale = AppLanguage.locale) -> String {
        ParentalWords.text("parental.pin.digits", locale, digits.count, Self.length)
    }

    /// Sends the six digits; a wrong PIN clears them.
    public func submit() async {
        guard complete, canType else { return }
        let pin = digits
        state = .checking
        do {
            try await client.unlock(subject, guardian: guardian.id, pin: pin)
            digits = ""
            state = .unlocked
        } catch {
            digits = ""
            switch ParentalRefusal(error) {
            case .pinWrong: state = .wrong
            case let .pinLocked(after): state = .locked(until: now().addingTimeInterval(TimeInterval(after ?? 900)))
            default: state = Log.isCancellation(error) ? .entering : .failed
            }
        }
    }

    /// The line under the dots. While PIN entry is closed it counts down by the minute: the
    /// screen draws it again as time passes, with the time it is drawn at (`at`).
    public func line(_ locale: Locale = AppLanguage.locale, at: Date? = nil) -> String? {
        switch state {
        case .entering, .checking: return nil
        case .wrong: return ParentalWords.text("parental.pin.wrong", locale)
        case let .locked(until):
            // whole minutes, rounded up: "1 min" until the lock ends
            let seconds = max(1, until.timeIntervalSince(at ?? now()))
            let left = Int((seconds / 60).rounded(.up)) * 60
            return ParentalWords.text("parental.pin.locked", locale, ParentalWords.length(seconds: left, locale))
        case .unlocked: return ParentalWords.text(subject.kind == "time" ? "parental.pin.unlockedTime" : "parental.pin.unlocked", locale)
        case .failed: return ParentalWords.text("parental.ask.failed", locale)
        }
    }
}

/// The pad's keys in phone order, the same in Arabic: the rows read 1 2 3 left to right
/// whatever the layout's direction (the view forces left-to-right).
public enum PinPad {
    public static let rows: [[Int]] = [[1, 2, 3], [4, 5, 6], [7, 8, 9]]

    /// A typed or pasted PIN as Western digits (Arabic-Indic and Persian digits too), nil
    /// unless it is exactly six digits.
    public static func normalize(_ typed: String) -> String? {
        let digits = typed.compactMap { $0.wholeNumberValue }.map(String.init).joined()
        guard digits.count == PinEntry.length, typed.allSatisfy({ $0.wholeNumberValue != nil }) else { return nil }
        return digits
    }
}
