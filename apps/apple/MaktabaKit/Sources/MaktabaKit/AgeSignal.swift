import Foundation
import Observation

// Epic 57 story 57.04: what the device says about who holds it, sent to the home. The home
// uses it only to tighten (a looser statement waits for a guardian), so nothing here is
// trusted by anyone: a modified app that sends "adult", or nothing, gains nothing beyond its
// account's own limit.

/// What Apple's Declared Age Range answered.
public enum AgeRangeAnswer: Equatable, Sendable, Codable {
    /// A range was shared: its bounds (no upper bound for an adult range) and how it was
    /// declared, in snake case (`self_declared`, `guardian_declared`, `confirmed`).
    case shared(lower: Int?, upper: Int?, declaration: String?)
    /// The person (or their guardian) chose not to share.
    case declined
    /// Nothing to ask: no API on this system, no capability in this build, no account.
    case notAvailable
}

/// Where the signal is read: Apple's APIs on a device (the Player's `AppleAgeSignalSource`),
/// a double in tests (the simulator cannot be a Family Sharing child).
@MainActor
public protocol AgeSignalSource: AnyObject {
    /// Asks Apple for the age range (gates 9, 13 and 18). May show the system's sheet.
    func requestAgeRange() async -> AgeRangeAnswer
    /// Screen Time's film and TV limits on Apple's 0 to 1000 scale (1000: no limit); nil
    /// where they cannot be read.
    func ratingCeilings() -> (movie: Int?, tv: Int?)
    /// The device's region ("US").
    var region: String? { get }
}

/// One statement to the home (`POST /api/me/age-signal`).
public struct AgeSignalReading: Equatable, Sendable, Codable {
    public var lower: Int?
    public var upper: Int?
    public var declaration: String?
    public var movieCeiling: Int?
    public var tvCeiling: Int?
    public var region: String?

    public init(range: AgeRangeAnswer? = nil, movieCeiling: Int? = nil, tvCeiling: Int? = nil, region: String? = nil) {
        if case let .shared(lower, upper, declaration)? = range {
            self.lower = lower
            self.upper = upper
            self.declaration = declaration
        }
        self.movieCeiling = movieCeiling.map { min(1000, max(0, $0)) }
        self.tvCeiling = tvCeiling.map { min(1000, max(0, $0)) }
        self.region = region.flatMap(Self.validRegion)
    }

    /// Two letters or three digits, as the home takes it.
    static func validRegion(_ r: String) -> String? {
        let ok = (r.count == 2 && r.allSatisfy { $0.isASCII && $0.isLetter }) || (r.count == 3 && r.allSatisfy { $0.isASCII && $0.isNumber })
        return ok ? r.uppercased() : nil
    }

    /// It says something about age: a range, or a ceiling (a ceiling of 1000 too: it is the
    /// device saying "no limit", which the home needs after a stricter one).
    public var saysSomething: Bool {
        lower != nil || upper != nil || movieCeiling != nil || tvCeiling != nil
    }

    /// The request's body: only what was read.
    public var body: [String: Any] {
        var b: [String: Any] = [:]
        if lower != nil || upper != nil {
            var range: [String: Any] = [:]
            if let lower { range["lower"] = lower }
            if let upper { range["upper"] = upper }
            if let declaration, Self.snake(declaration) { range["declaration"] = declaration }
            b["age_range"] = range
        }
        if let movieCeiling { b["movie_ceiling"] = movieCeiling }
        if let tvCeiling { b["tv_ceiling"] = tvCeiling }
        if let region { b["region"] = region }
        return b
    }

    static func snake(_ s: String) -> Bool {
        guard let first = s.first, first.isASCII, first.isLowercase, s.count <= 40 else { return false }
        return s.allSatisfy { $0.isASCII && ($0.isLowercase || $0.isNumber || $0 == "_") }
    }
}

/// Sends the device's signal for the current sign-in, kept by the session
/// (`AppSession.ageSignal`).
///
/// - The age range is asked for once per sign-in (signing out forgets it), and again only
///   from Profile > Age range. The answer is kept on this device (the band and how it was
///   declared, never a birth date) so the next launch sends the same thing without asking.
/// - The rating limits are read at sign-in and each time the app comes to the front
///   (watching them change needs Family Controls, which this app does not ask for).
/// - It posts only when what it would send differs from what it last sent for this sign-in,
///   then refreshes the access token (`refresh: true`), so the next token carries the limit.
/// - A home from before this route (404) is not asked again. `409 no_sign_in` (an access
///   token minted before the home knew sign-ins) refreshes the token and sends once more.
@MainActor @Observable
public final class AgeSignalSender {
    /// What the home last answered: the device limit in force, and whether a looser
    /// statement waits for a guardian.
    public private(set) var answer: AgeSignalAnswer?
    /// What Apple last answered about the age range (nil: not asked on this sign-in).
    public private(set) var range: AgeRangeAnswer?
    /// What was last read from the device, for the Profile's diagnostics line.
    public private(set) var lastReading: AgeSignalReading?
    /// The home has the route (false after a 404).
    public private(set) var supported = true

    @ObservationIgnored private var source: (any AgeSignalSource)?
    @ObservationIgnored private var client: APIClient?
    @ObservationIgnored private var key: String?
    @ObservationIgnored private var lastSent: AgeSignalReading?
    @ObservationIgnored private var sending = false
    @ObservationIgnored private var again = false
    @ObservationIgnored private let defaults: UserDefaults
    /// Called after the home took a statement and the token was refreshed.
    @ObservationIgnored public var onApplied: (@MainActor () async -> Void)?

    public init(defaults: UserDefaults = .standard) {
        self.defaults = defaults
    }

    /// Where the signal is read from (the app sets it once, before sign-in).
    public func use(_ source: any AgeSignalSource) {
        self.source = source
    }

    /// A sign-in began or was restored: `user` at `client.home`.
    public func signedIn(client: APIClient, user: String) {
        self.client = client
        key = "ageSignal.range.\(client.home.absoluteString)#\(user)"
        answer = nil
        lastSent = nil
        lastReading = nil
        supported = true
        range = key.flatMap { defaults.data(forKey: $0) }.flatMap { try? JSONDecoder().decode(AgeRangeAnswer.self, from: $0) }
    }

    /// Signed out: the next sign-in asks afresh (`forget`), or only another home is in use.
    public func signedOut(forget: Bool) {
        if forget, let key { defaults.removeObject(forKey: key) }
        client = nil
        key = nil
        answer = nil
        range = nil
        lastSent = nil
        lastReading = nil
    }

    /// Reads the device and sends what changed. `askAgain`: ask Apple for the age range even
    /// if it was asked on this sign-in (Profile > Age range).
    public func sync(askAgain: Bool = false) async {
        guard let source, let client, supported else { return }
        if sending {
            again = true
            return
        }
        sending = true
        defer { sending = false }
        var ask = askAgain
        repeat {
            again = false
            if range == nil || ask {
                ask = false
                let got = await source.requestAgeRange()
                guard self.client === client else { return }
                // "not available" is not an answer: nothing was asked, so nothing is kept
                // and an earlier answer stays
                if got != .notAvailable || range == nil { range = got }
                if got != .notAvailable, let key, let data = try? JSONEncoder().encode(got) { defaults.set(data, forKey: key) }
            }
            let ceilings = source.ratingCeilings()
            let reading = AgeSignalReading(range: range, movieCeiling: ceilings.movie, tvCeiling: ceilings.tv, region: source.region)
            lastReading = reading
            guard reading != lastSent else { continue }
            // nothing to say and nothing said before: the account's own limit stands
            guard reading.saysSomething || lastSent?.saysSomething == true else {
                lastSent = reading
                continue
            }
            await post(reading, to: client)
        } while again
    }

    private func post(_ reading: AgeSignalReading, to client: APIClient, retried: Bool = false) async {
        do {
            let a = try await client.sendAgeSignal(reading.body)
            guard self.client === client else { return }
            lastSent = reading
            answer = a
            if a.refresh { try? await client.refreshAccessToken() }
            await onApplied?()
        } catch {
            guard self.client === client, !Log.isCancellation(error) else { return }
            if case let APIError.status(code, _, _)? = error as? APIError, code == 404 || code == 405 {
                supported = false
                return
            }
            if ParentalRefusal(error) == .noSignIn, !retried {
                // the token names no sign-in: a fresh one does
                guard (try? await client.refreshAccessToken()) != nil else { return }
                await post(reading, to: client, retried: true)
                return
            }
            if case let APIError.status(code, _, _)? = error as? APIError, code == 422 || code == 409 {
                // the home will not take this statement: not sent again until it changes
                lastSent = reading
            }
            Log.failure("age signal", error, logger: Log.network)
        }
    }

    // MARK: what the Profile shows

    /// The Age range row's value: "9–12", "18+", or "Not shared".
    public func rangeText(_ locale: Locale = AppLanguage.locale) -> String {
        guard case let .shared(lower, upper, _)? = range else { return ParentalWords.text("ageSignal.profile.notShared", locale) }
        var style = IntegerFormatStyle<Int>.number.grouping(.never)
        style = style.locale(locale)
        switch (lower, upper) {
        case let (l?, u?): return "\u{200E}\(l.formatted(style))–\(u.formatted(style))"
        case let (l?, nil): return AgeTier.label(l, locale)
        case let (nil, u?): return "\u{200E}0–\(u.formatted(style))"
        case (nil, nil): return ParentalWords.text("ageSignal.profile.notShared", locale)
        }
    }

    /// The hidden diagnostics line (Profile > About, five taps): what the device says now,
    /// for the real-device check of story 57.04.
    public var diagnostics: String {
        let r = lastReading
        func n(_ v: Int?) -> String { v.map(String.init) ?? "-" }
        return "age \(n(r?.lower))-\(n(r?.upper)) \(r?.declaration ?? "-") · movie \(n(r?.movieCeiling)) · tv \(n(r?.tvCeiling)) · region \(r?.region ?? "-") · limit \(n(answer?.tierLimit)) · pending \(answer?.pendingLooser == true)"
    }
}

/// A source that says a fixed thing: the tests' double, and what a Debug launch with
/// `-maktaba-age-signal` uses.
@MainActor
public final class FixedAgeSignal: AgeSignalSource {
    public var answer: AgeRangeAnswer
    public var movie: Int?
    public var tv: Int?
    public var region: String?
    public private(set) var asked = 0

    public init(_ answer: AgeRangeAnswer = .notAvailable, movie: Int? = nil, tv: Int? = nil, region: String? = "US") {
        self.answer = answer
        self.movie = movie
        self.tv = tv
        self.region = region
    }

    /// `9-12:guardian_declared:400:400` (range, declaration, film ceiling, TV ceiling; a part
    /// left empty or `-` is not said), `declined`, or `none`.
    public convenience init?(spec: String) {
        let parts = spec.split(separator: ":", omittingEmptySubsequences: false).map(String.init)
        guard let first = parts.first else { return nil }
        func value(_ i: Int) -> String? { parts.count > i && !parts[i].isEmpty && parts[i] != "-" ? parts[i] : nil }
        let answer: AgeRangeAnswer
        switch first {
        case "none", "", "-": answer = .notAvailable
        case "declined": answer = .declined
        default:
            let b = first.split(separator: "-", omittingEmptySubsequences: false).map { Int($0) }
            guard let lower = b.first ?? nil else { return nil }
            answer = .shared(lower: lower, upper: b.count > 1 ? b[1] : nil, declaration: value(1))
        }
        self.init(answer, movie: value(2).flatMap(Int.init), tv: value(3).flatMap(Int.init))
    }

    public func requestAgeRange() async -> AgeRangeAnswer {
        asked += 1
        return answer
    }

    public func ratingCeilings() -> (movie: Int?, tv: Int?) { (movie, tv) }
}
