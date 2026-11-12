import Foundation

// Epic 57 (parental controls): what the home says about the signed-in account's limit, and
// the routes a restricted account, a guardian and an admin call. The home enforces every
// rule; an app only shows what the home answers. The shapes are the Go handlers'
// (`api/internal/handlers/parental`, `shared/api/openapi.yaml` under "Parental controls").

// MARK: tiers

/// The home's age tiers: `0` all ages, `9`, `13`, `16`, `18`; a limit may also be `-1`,
/// nothing that has a tier.
public enum AgeTier {
    public static let all = 0
    public static let nothing = -1
    /// The content tiers, lowest first.
    public static let content = [0, 9, 13, 16, 18]
    /// What a limit may be, strictest first (the pickers' order: ascending age).
    public static let limits = [-1, 0, 9, 13, 16, 18]

    /// A tier as people read it: "All ages", "13+". In Arabic the plus leads and the label
    /// starts with a left-to-right mark, so it sits on the correct side in a mirrored line.
    public static func label(_ tier: Int, _ locale: Locale = AppLanguage.locale) -> String {
        switch tier {
        case ..<0: return ParentalWords.text("parental.tier.none", locale)
        case 0: return ParentalWords.text("parental.tier.all", locale)
        default:
            let rtl = locale.language.characterDirection == .rightToLeft
            return rtl ? "\u{200E}+\(tier)" : "\(tier)+"
        }
    }

    /// A content tier, nil being unrated.
    public static func contentLabel(_ tier: Int?, _ locale: Locale = AppLanguage.locale) -> String {
        tier.map { label($0, locale) } ?? ParentalWords.text("parental.tier.unrated", locale)
    }
}

/// The words of the parental screens, English and Arabic, under the epic's own keys
/// (`parental.blocked.title`, `family.title`, `parental.time.left`) in MaktabaKit's catalog,
/// so the iPhone, iPad and Apple TV apps say the same thing.
public enum ParentalWords {
    /// The sentence for `key` with its arguments (`%@`, `%1$@`, `%lld`).
    public static func text(_ key: String, _ locale: Locale, _ args: CVarArg...) -> String {
        let format = kitWord(key, locale) ?? key
        return args.isEmpty ? format : String(format: format, locale: locale, arguments: args)
    }

    /// The same in the app's language.
    public static func text(_ key: String, _ args: CVarArg...) -> String {
        let locale = AppLanguage.locale
        let format = kitWord(key, locale) ?? key
        return args.isEmpty ? format : String(format: format, locale: locale, arguments: args)
    }

    /// A clock time in the app's language ("7:00 PM", «٧:٠٠ م»).
    public static func clock(_ date: Date, _ locale: Locale = AppLanguage.locale, zone: TimeZone = .current) -> String {
        var style = Date.FormatStyle(date: .omitted, time: .shortened)
        style.locale = locale
        style.timeZone = zone
        return date.formatted(style)
    }

    /// A length of time for a sentence: "5 min", "1 hr 20 min" (the system's words).
    public static func length(seconds: Int, _ locale: Locale = AppLanguage.locale) -> String {
        let s = max(0, seconds)
        let shown = s >= 60 ? (s / 60) * 60 : s
        var style = Duration.UnitsFormatStyle(allowedUnits: s >= 60 ? [.hours, .minutes] : [.seconds], width: .abbreviated)
        style.locale = locale
        return Duration.seconds(shown).formatted(style)
    }
}

// MARK: the caller's own limit

/// `GET /api/me/parental`: whether this sign-in is limited, by the account or by the device.
public struct ParentalLimit: Decodable, Equatable, Sendable {
    public let restricted: Bool
    /// The limit in force: the stricter of the account's and the device's.
    public let maxTier: Int?
    public let unrated: String?
    public let allowOnly: Bool
    /// The limit this sign-in's device set (57.04); present on an unrestricted account too.
    public let deviceLimit: Int?
    /// The device has since said something looser, which waits for a guardian.
    public let devicePending: Bool
    public let version: Int

    /// No limit: an account that is not restricted on a device that said nothing, a home
    /// from before Epic 57, and before the first answer.
    public static let none = ParentalLimit()

    public init(restricted: Bool = false, maxTier: Int? = nil, unrated: String? = nil, allowOnly: Bool = false,
                deviceLimit: Int? = nil, devicePending: Bool = false, version: Int = 0) {
        self.restricted = restricted
        self.maxTier = maxTier
        self.unrated = unrated
        self.allowOnly = allowOnly
        self.deviceLimit = deviceLimit
        self.devicePending = devicePending
        self.version = version
    }

    enum CodingKeys: String, CodingKey {
        case restricted, unrated, version
        case maxTier = "max_tier"
        case allowOnly = "allow_only"
        case deviceLimit = "device_limit"
        case devicePending = "device_pending"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        restricted = c.lenient(.restricted) ?? false
        maxTier = c.lenient(.maxTier)
        unrated = c.lenient(.unrated)
        allowOnly = c.lenient(.allowOnly) ?? false
        deviceLimit = c.lenient(.deviceLimit)
        devicePending = c.lenient(.devicePending) ?? false
        version = c.lenient(.version) ?? 0
    }

    /// The home hides things from this sign-in: a "not available" may be a block.
    public var limited: Bool { restricted || deviceLimit != nil }

    /// Why something is not available, for the blocked page's line.
    public enum Reason: Equatable, Sendable {
        case account(tier: Int)
        case device(tier: Int)
    }

    /// The device's limit when it is the one in force (an unrestricted account, or a device
    /// stricter than the account), else the account's.
    public var reason: Reason? {
        if let d = deviceLimit, !restricted || maxTier == nil || d <= (maxTier ?? d) { return .device(tier: d) }
        if restricted, let m = maxTier { return .account(tier: m) }
        return nil
    }

    /// The blocked page's reason line ("Your account is limited to 9+."), nil with no limit.
    public func reasonLine(_ locale: Locale = AppLanguage.locale) -> String? {
        switch reason {
        case let .account(t): ParentalWords.text("parental.blocked.account", locale, AgeTier.label(t, locale))
        case let .device(t): ParentalWords.text("parental.blocked.device", locale, AgeTier.label(t, locale))
        case nil: nil
        }
    }
}

// MARK: what the home answers when it refuses

/// A refusal of the parental routes, or of playback for a parental reason, read from the
/// home's problem (`code`, `reason`, and the times beside them).
public enum ParentalRefusal: Equatable, Sendable {
    /// 403 `parental-time`: the account's watching time is over for now. `reason` is
    /// `outside_slot`, `limit_reached` or `no_heartbeat`.
    case time(reason: String, nextAllowedAt: Date?)
    /// 403 `restricted` with `reason: programme`: the programme on a channel now is above
    /// the limit; ask again at `retryAt`.
    case programme(retryAt: Date?)
    /// 403 `restricted`: not for a restricted account at all.
    case restricted
    /// 403 `not_restricted`: only a restricted account asks a parent.
    case notRestricted
    /// 403 `pin_wrong`.
    case pinWrong
    /// 429 `pin_locked`: PIN entry is closed for `retryAfter` seconds.
    case pinLocked(retryAfter: Int?)
    /// 403 `password_wrong` (setting a PIN).
    case passwordWrong
    /// 429 `too_many_requests`: 20 requests already wait.
    case tooManyRequests
    /// 409 `stale`: someone else changed the limit since it was read.
    case stale
    /// 409 `no_sign_in`: the credential names no app sign-in (refresh, then send again).
    case noSignIn
    /// 403 `device_limited`: this sign-in is itself limited by its device.
    case deviceLimited
    /// 403 `admin_only`, or a play-only sign-in through Maktaba Cloud: not this caller's to change.
    case notYours
    /// 410 `request_expired`, 409 `request_decided`.
    case requestGone
    /// 409 `restricted_admin`: an admin cannot be made restricted.
    case restrictedAdmin
    /// 409 `restricted_guardian`: a restricted account (or the account itself) cannot look
    /// after it.
    case restrictedGuardian

    public init?(_ error: Error) {
        guard case let APIError.status(status, body, retryAfter)? = error as? APIError else { return nil }
        let fields = (try? JSONDecoder().decode(LooseFields.self, from: Data(body.utf8)))?.fields ?? [:]
        let code = fields["code"]
        let slug = Problem.parse(body)?.slug
        func time(_ key: String) -> Date? { fields[key].flatMap(homeTime) }
        if status == 403, code == "parental_time" || slug == "parental-time" {
            self = .time(reason: fields["reason"] ?? "limit_reached", nextAllowedAt: time("next_allowed_at"))
            return
        }
        switch (status, code) {
        case (403, "restricted"):
            self = fields["reason"] == "programme" ? .programme(retryAt: time("retry_at")) : .restricted
        case (403, "not_restricted"): self = .notRestricted
        case (403, "pin_wrong"): self = .pinWrong
        case (429, "pin_locked"): self = .pinLocked(retryAfter: fields["retry_after"].flatMap(Int.init) ?? retryAfter)
        case (403, "password_wrong"): self = .passwordWrong
        case (429, "too_many_requests"): self = .tooManyRequests
        case (409, "stale"): self = .stale
        case (409, "no_sign_in"): self = .noSignIn
        case (409, "restricted_admin"): self = .restrictedAdmin
        case (409, "restricted_guardian"): self = .restrictedGuardian
        case (403, "device_limited"): self = .deviceLimited
        case (403, "admin_only"), (403, "cloud_scope"), (403, "scope"): self = .notYours
        case (410, _), (409, "request_decided"): self = .requestGone
        default: return nil
        }
    }
}

// MARK: models

/// What a rule, a request or an event names.
public struct ParentalSubject: Hashable, Sendable {
    public static let kinds = ["library", "collection", "series", "channel", "youtube_source", "plugin", "video"]
    public let kind: String
    public let id: String

    public init(kind: String, id: String) {
        self.kind = kind
        self.id = id
    }

    public static func video(_ id: String) -> ParentalSubject { .init(kind: "video", id: id) }
    public static func channel(_ id: String) -> ParentalSubject { .init(kind: "channel", id: id) }
    /// More watching time today (story 57.06): the kind is `time`, the id the minutes as text.
    public static func time(minutes: Int) -> ParentalSubject { .init(kind: "time", id: String(min(480, max(1, minutes)))) }
    /// What a child may ask for at once.
    public static let timeChoices = [15, 30, 60]
}

/// A subject as a guardian may see it (a request's, an event's).
public struct ParentalSubjectInfo: Decodable, Hashable, Sendable {
    public let kind: String
    public let id: String
    public let title: String?
    public let posterURL: String?
    public let tier: Int?
    /// Deleted since.
    public let removed: Bool
    /// In a library this guardian cannot open: no title is sent.
    public let hidden: Bool

    enum CodingKeys: String, CodingKey {
        case kind, id, title, tier, removed, hidden
        case posterURL = "poster_url"
    }

    public init(kind: String, id: String, title: String? = nil, tier: Int? = nil, removed: Bool = false, hidden: Bool = false) {
        self.kind = kind
        self.id = id
        self.title = title
        posterURL = nil
        self.tier = tier
        self.removed = removed
        self.hidden = hidden
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        kind = c.lenient(.kind) ?? ""
        id = c.lenient(.id) ?? ""
        title = c.lenient(.title)
        posterURL = c.lenient(.posterURL)
        tier = c.lenient(.tier)
        removed = c.lenient(.removed) ?? false
        hidden = c.lenient(.hidden) ?? false
    }

    /// The title a list shows: the subject's, "Removed" for one deleted since, else its kind.
    public func displayTitle(_ locale: Locale = AppLanguage.locale) -> String {
        if removed { return ParentalWords.text("activity.removed", locale) }
        if let title, !title.isEmpty { return title }
        return ParentalWords.text("family.subject.\(kind)", locale)
    }
}

/// A person by name (a child, a guardian, someone with a parent PIN).
public struct ParentalPerson: Decodable, Hashable, Sendable, Identifiable {
    public let id: String
    public let username: String

    public init(id: String, username: String) {
        self.id = id
        self.username = username
    }
}

/// A restricted account's limit (`parental_policies`).
public struct ParentalPolicy: Decodable, Hashable, Sendable {
    public let userID: String
    public let maxTier: Int
    /// `hide`, or `tier_0` … `tier_18`: what a video with no tier counts as.
    public let unrated: String
    public let allowOnly: Bool
    public let version: Int

    enum CodingKeys: String, CodingKey {
        case unrated, version
        case userID = "user_id"
        case maxTier = "max_tier"
        case allowOnly = "allow_only"
    }

    public init(userID: String, maxTier: Int, unrated: String = "hide", allowOnly: Bool = false, version: Int = 1) {
        self.userID = userID
        self.maxTier = maxTier
        self.unrated = unrated
        self.allowOnly = allowOnly
        self.version = version
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        userID = c.lenient(.userID) ?? ""
        maxTier = c.lenient(.maxTier) ?? AgeTier.nothing
        unrated = c.lenient(.unrated) ?? "hide"
        allowOnly = c.lenient(.allowOnly) ?? false
        version = c.lenient(.version) ?? 0
    }

    /// The unrated rules a picker offers: hide, then as each tier.
    public static let unratedChoices = ["hide"] + AgeTier.content.map { "tier_\($0)" }

    /// An unrated rule as people read it: "Hide", or the tier it counts as.
    public static func unratedLabel(_ rule: String, _ locale: Locale = AppLanguage.locale) -> String {
        if rule.hasPrefix("tier_"), let t = Int(rule.dropFirst(5)) { return AgeTier.label(t, locale) }
        return ParentalWords.text("family.child.unrated.hide", locale)
    }
}

/// `GET /api/parental/children`: a restricted account the caller looks after, with its limit.
public struct ParentalChild: Decodable, Hashable, Sendable, Identifiable {
    public let id: String
    public let username: String
    public let policy: ParentalPolicy

    enum CodingKeys: String, CodingKey { case id, username }

    public init(id: String, username: String, policy: ParentalPolicy) {
        self.id = id
        self.username = username
        self.policy = policy
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        username = c.lenient(.username) ?? ""
        policy = try ParentalPolicy(from: decoder)
    }
}

/// What one sign-in's device said about its user's age (57.04), as a guardian lists it.
public struct ParentalSignal: Decodable, Hashable, Sendable, Identifiable {
    public struct Pending: Decodable, Hashable, Sendable {
        public let tierLimit: Int?
        enum CodingKeys: String, CodingKey { case tierLimit = "tier_limit" }
        public init(tierLimit: Int?) { self.tierLimit = tierLimit }
        public init(from decoder: Decoder) throws {
            tierLimit = try decoder.container(keyedBy: CodingKeys.self).lenient(.tierLimit)
        }
    }

    /// The sign-in (`refresh_family_id`).
    public let id: String
    public let lower: Int?
    public let upper: Int?
    public let declaration: String?
    public let movieCeiling: Int?
    public let tvCeiling: Int?
    public let region: String?
    public let tierLimit: Int?
    /// A looser statement waiting for a guardian (its limit; nil inside: no limit at all).
    public let pendingLooser: Pending?
    public let observedAt: Date?

    enum CodingKeys: String, CodingKey {
        case lower, upper, declaration, region
        case id = "refresh_family_id"
        case movieCeiling = "movie_ceiling"
        case tvCeiling = "tv_ceiling"
        case tierLimit = "tier_limit"
        case pendingLooser = "pending_looser"
        case observedAt = "observed_at"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        lower = c.lenient(.lower)
        upper = c.lenient(.upper)
        declaration = c.lenient(.declaration)
        movieCeiling = c.lenient(.movieCeiling)
        tvCeiling = c.lenient(.tvCeiling)
        region = c.lenient(.region)
        tierLimit = c.lenient(.tierLimit)
        pendingLooser = c.lenient(.pendingLooser)
        observedAt = c.lenient(.observedAt)
    }

    /// Where the limit came from: an age range was shared, a Screen Time ceiling is set.
    public var fromAgeRange: Bool { lower != nil || upper != nil }
    public var fromScreenTime: Bool { (movieCeiling ?? 1000) < 1000 || (tvCeiling ?? 1000) < 1000 }
}

/// `GET /api/users/{id}/parental`: the limit, who looks after the account, its devices.
public struct ParentalAccount: Decodable, Sendable {
    public let policy: ParentalPolicy
    /// What the caller is to this account: `guardian` or `admin`.
    public let access: String
    public let guardians: [ParentalPerson]
    public let signals: [ParentalSignal]

    enum CodingKeys: String, CodingKey { case policy, access, guardians, signals }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        policy = try c.decode(ParentalPolicy.self, forKey: .policy)
        access = c.lenient(.access) ?? "guardian"
        guardians = c.lenientList(.guardians)
        signals = c.lenientList(.signals)
    }

    public var isAdmin: Bool { access == "admin" }
}

/// An allow or block rule.
public struct ParentalRule: Decodable, Hashable, Sendable, Identifiable {
    public let id: String
    /// `allow` or `block`.
    public let effect: String
    public let subjectKind: String
    public let subjectID: String
    public let expiresAt: Date?

    enum CodingKeys: String, CodingKey {
        case id, effect
        case subjectKind = "subject_kind"
        case subjectID = "subject_id"
        case expiresAt = "expires_at"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        effect = c.lenient(.effect) ?? ""
        subjectKind = c.lenient(.subjectKind) ?? ""
        subjectID = c.lenient(.subjectID) ?? ""
        expiresAt = c.lenient(.expiresAt)
    }
}

/// A child's request to open something.
public struct ParentalRequest: Decodable, Hashable, Sendable, Identifiable {
    public let id: String
    public let childID: String
    public let subjectKind: String
    public let subjectID: String
    /// `pending`, `approved`, `denied`, `expired`.
    public let status: String
    public let createdAt: Date?
    public let expiresAt: Date?
    /// A request for more time: how many minutes.
    public let minutes: Int?

    public var isTime: Bool { subjectKind == "time" }

    enum CodingKeys: String, CodingKey {
        case id, status, minutes
        case childID = "child_id"
        case subjectKind = "subject_kind"
        case subjectID = "subject_id"
        case createdAt = "created_at"
        case expiresAt = "expires_at"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        childID = c.lenient(.childID) ?? ""
        subjectKind = c.lenient(.subjectKind) ?? ""
        subjectID = c.lenient(.subjectID) ?? ""
        status = c.lenient(.status) ?? "pending"
        createdAt = c.lenient(.createdAt)
        expiresAt = c.lenient(.expiresAt)
        minutes = c.lenient(.minutes) ?? (subjectKind == "time" ? Int(subjectID) : nil)
    }
}

/// A request as a guardian's list shows it: with the child and what was asked for.
public struct ParentalRequestItem: Decodable, Hashable, Sendable, Identifiable {
    public let request: ParentalRequest
    public let child: ParentalPerson
    public let subject: ParentalSubjectInfo

    public var id: String { request.id }

    /// The video whose poster the request's row shows: only a video the home sent a poster
    /// for (one this guardian may open, not removed). Nil for anything else.
    public var posterVideo: String? {
        guard subject.kind == "video", !subject.hidden, !subject.removed, subject.posterURL?.isEmpty == false else { return nil }
        return subject.id
    }

    /// "Sara asks for The long night", or for a time request "Sara asks for 30 min more time".
    public func line(_ locale: Locale = AppLanguage.locale) -> String {
        if request.isTime {
            return ParentalWords.text("family.request.time", locale, child.username,
                                      ParentalWords.length(seconds: (request.minutes ?? 0) * 60, locale))
        }
        return ParentalWords.text("family.request.from", locale, child.username, subject.displayTitle(locale))
    }

    enum CodingKeys: String, CodingKey { case child, subject }

    public init(from decoder: Decoder) throws {
        request = try ParentalRequest(from: decoder)
        let c = try decoder.container(keyedBy: CodingKeys.self)
        child = c.lenient(.child) ?? ParentalPerson(id: request.childID, username: "")
        subject = c.lenient(.subject) ?? ParentalSubjectInfo(kind: request.subjectKind, id: request.subjectID)
    }
}

/// When an allow or block rule, or an approval, ends, as the home is told.
public enum ParentalEnding: Equatable, Sendable {
    /// It does not end.
    case never
    /// `expires_at`: the instant, on the sender's clock.
    case at(Date)
    /// `until: day_end`: the end of the home's day (the next 04:00 in the home's time zone),
    /// worked out by the home, so the sender's clock and time zone do not matter.
    case dayEnd

    /// The fields of the request's body that say it.
    var body: [String: Any] {
        switch self {
        case .never: [:]
        case let .at(date): ["expires_at": APIClient.rfc3339(date)]
        case .dayEnd: ["until": "day_end"]
        }
    }

    /// The home answered that it does not take `until` (one from before it: 400 for a field
    /// it does not know, or 422): the sender then works the day's end out itself.
    static func untilRefused(_ error: Error) -> Bool {
        guard case let APIError.status(code, _, _)? = error as? APIError else { return false }
        return code == 400 || code == 422
    }
}

/// How long an approval lasts: until the home's day ends, for seven days, or for good.
public enum ApprovalLength: String, CaseIterable, Sendable {
    case today, week, always

    public var key: String {
        switch self {
        case .today: "family.approve.today"
        case .week: "family.approve.week"
        case .always: "family.approve.always"
        }
    }

    /// What the home is told. "Today" is the home's own day's end (`until: day_end`): the
    /// home computes it, whatever this device's clock and time zone say.
    public func ending(from now: Date = .now) -> ParentalEnding {
        switch self {
        case .always: .never
        case .week: .at(now.addingTimeInterval(7 * 86400))
        case .today: .dayEnd
        }
    }

    /// A calendar in the home's time zone (`time_zone` of the time rules), for working out
    /// the home's day line here; this device's zone when the home's is not known.
    public static func homeCalendar(timeZone: String?) -> Calendar {
        var calendar = Calendar(identifier: .gregorian)
        if let timeZone, let zone = TimeZone(identifier: timeZone) { calendar.timeZone = zone }
        return calendar
    }

    /// When the allow rule ends (nil: never), worked out here: only for a home that does not
    /// take `until`. "Today" ends at the next 04:00, the home's day line, in `calendar`
    /// (`homeCalendar`: the home's time zone, not this device's).
    public func expiresAt(from now: Date = .now, calendar: Calendar = .current) -> Date? {
        switch self {
        case .always: return nil
        case .week: return now.addingTimeInterval(7 * 86400)
        case .today:
            let four = calendar.date(bySettingHour: 4, minute: 0, second: 0, of: now) ?? now
            return four > now ? four : calendar.date(byAdding: .day, value: 1, to: four)
        }
    }
}

/// One row of a child's activity (`GET /api/users/{id}/parental/activity`).
public struct ParentalEvent: Decodable, Hashable, Sendable, Identifiable {
    public let id: String
    /// `watched`, `blocked`, `asked`, `approved`, `denied`, `pin_locked`, `signal`,
    /// `time_up`, `time_granted`.
    public let kind: String
    public let at: Date
    public let subject: ParentalSubjectInfo?
    public let guardian: ParentalPerson?
    public let device: String?
    /// A time event's minutes (it has no subject).
    public let minutes: Int?

    enum CodingKeys: String, CodingKey { case id, kind, at, subject, guardian, device, minutes }

    public init(id: String, kind: String, at: Date, subject: ParentalSubjectInfo? = nil, guardian: ParentalPerson? = nil,
                device: String? = nil, minutes: Int? = nil) {
        self.minutes = minutes
        self.id = id
        self.kind = kind
        self.at = at
        self.subject = subject
        self.guardian = guardian
        self.device = device
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        kind = c.lenient(.kind) ?? ""
        at = try c.decode(Date.self, forKey: .at)
        subject = c.lenient(.subject)
        guardian = c.lenient(.guardian)
        device = c.lenient(.device)
        minutes = c.lenient(.minutes)
    }
}

public struct ParentalActivityPage: Decodable, Sendable {
    public let events: [ParentalEvent]
    public let nextCursor: String?

    enum CodingKeys: String, CodingKey {
        case events
        case nextCursor = "next_cursor"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        events = c.lenientList(.events)
        let next: String? = c.lenient(.nextCursor)
        nextCursor = next?.isEmpty == false ? next : nil
    }
}

// MARK: time (57.14, 57.15)

/// `GET /api/me/parental/time`: whether the caller may watch now, for how long, and when next.
public struct ParentalTime: Decodable, Equatable, Sendable {
    public let allowed: Bool
    /// A rule covers this moment: `remainingSeconds` is a real countdown.
    public let limited: Bool
    /// `outside_slot` or `limit_reached` when not allowed.
    public let reason: String?
    public let remainingSeconds: Int?
    public let slotEndsAt: Date?
    public let nextAllowedAt: Date?
    /// The home's clock for this answer.
    public let now: Date?

    enum CodingKeys: String, CodingKey {
        case allowed, limited, reason, now
        case remainingSeconds = "remaining_seconds"
        case slotEndsAt = "slot_ends_at"
        case nextAllowedAt = "next_allowed_at"
    }

    public init(allowed: Bool, limited: Bool, reason: String? = nil, remainingSeconds: Int? = nil, slotEndsAt: Date? = nil,
                nextAllowedAt: Date? = nil, now: Date? = nil) {
        self.allowed = allowed
        self.limited = limited
        self.reason = reason
        self.remainingSeconds = remainingSeconds
        self.slotEndsAt = slotEndsAt
        self.nextAllowedAt = nextAllowedAt
        self.now = now
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        allowed = c.lenient(.allowed) ?? true
        limited = c.lenient(.limited) ?? false
        let r: String? = c.lenient(.reason)
        reason = r?.isEmpty == false ? r : nil
        remainingSeconds = c.lenient(.remainingSeconds)
        slotEndsAt = c.lenient(.slotEndsAt)
        nextAllowedAt = c.lenient(.nextAllowedAt)
        now = c.lenient(.now)
    }
}

/// One time rule: the days it covers, an optional slot, an optional daily limit, an optional
/// library. Every rule that covers a moment must allow it.
public struct ParentalTimeRule: Codable, Hashable, Sendable, Identifiable {
    /// The home's id; a rule not saved yet has one of its own (never sent).
    public var id: String
    public var libraryID: String?
    /// A bitmask of the home days covered, bit 0 Sunday … bit 6 Saturday (127: every day).
    public var days: Int
    /// "HH:MM" in the home's time zone; both or neither.
    public var slotStart: String?
    public var slotEnd: String?
    public var dailyMinutes: Int?

    public static let everyDay = 127

    enum CodingKeys: String, CodingKey {
        case id, days
        case libraryID = "library_id"
        case slotStart = "slot_start"
        case slotEnd = "slot_end"
        case dailyMinutes = "daily_minutes"
    }

    public init(id: String = "new-\(UUID().uuidString)", libraryID: String? = nil, days: Int = everyDay,
                slotStart: String? = nil, slotEnd: String? = nil, dailyMinutes: Int? = nil) {
        self.id = id
        self.libraryID = libraryID
        self.days = days
        self.slotStart = slotStart
        self.slotEnd = slotEnd
        self.dailyMinutes = dailyMinutes
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = c.lenient(.id) ?? "new-\(UUID().uuidString)"
        let lib: String? = c.lenient(.libraryID)
        libraryID = lib?.isEmpty == false ? lib : nil
        days = c.lenient(.days) ?? Self.everyDay
        slotStart = (c.lenient(.slotStart) as String?).flatMap { $0.isEmpty ? nil : String($0.prefix(5)) }
        slotEnd = (c.lenient(.slotEnd) as String?).flatMap { $0.isEmpty ? nil : String($0.prefix(5)) }
        dailyMinutes = c.lenient(.dailyMinutes)
    }

    /// What the home is sent: no id (the list replaces the rules), no empty field.
    var body: [String: Any] {
        var b: [String: Any] = ["days": days]
        if let libraryID { b["library_id"] = libraryID }
        if let slotStart, let slotEnd {
            b["slot_start"] = slotStart
            b["slot_end"] = slotEnd
        }
        if let dailyMinutes { b["daily_minutes"] = dailyMinutes }
        return b
    }

    /// The first choice the editor offers (README Q10): one hour a day, 07:00 to 20:00.
    public static let suggested = ParentalTimeRule(days: everyDay, slotStart: "07:00", slotEnd: "20:00", dailyMinutes: 60)

    public func covers(day bit: Int) -> Bool { days & (1 << bit) != 0 }

    public mutating func set(day bit: Int, _ on: Bool) {
        if on { days |= 1 << bit } else { days &= ~(1 << bit) }
    }
}

public struct ParentalTimeRules: Decodable, Sendable {
    public let rules: [ParentalTimeRule]
    public let timeZone: String?

    enum CodingKeys: String, CodingKey {
        case rules
        case timeZone = "time_zone"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        rules = c.lenientList(.rules)
        timeZone = c.lenient(.timeZone)
    }
}

/// `GET /api/parental/users/{id}/time-usage`: seconds watched per home day and library, and
/// the extra time given.
public struct ParentalTimeUsage: Decodable, Sendable {
    public struct Row: Decodable, Hashable, Sendable {
        public let day: String
        public let libraryID: String
        public let seconds: Int
        enum CodingKeys: String, CodingKey {
            case day, seconds
            case libraryID = "library_id"
        }
        public init(day: String, libraryID: String, seconds: Int) {
            self.day = day
            self.libraryID = libraryID
            self.seconds = seconds
        }
        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            day = c.lenient(.day) ?? ""
            libraryID = c.lenient(.libraryID) ?? ""
            seconds = c.lenient(.seconds) ?? 0
        }
    }

    public struct Grant: Decodable, Hashable, Sendable, Identifiable {
        public let id: String
        public let day: String
        public let minutes: Int
        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: AnyKey.self)
            id = c.lenient(AnyKey(stringValue: "id")) ?? UUID().uuidString
            day = c.lenient(AnyKey(stringValue: "day")) ?? ""
            minutes = c.lenient(AnyKey(stringValue: "minutes")) ?? 0
        }
    }

    public let timeZone: String?
    /// The home's day now ("2026-11-11"; a day starts at 04:00 home time).
    public let today: String
    public let usage: [Row]
    public let grants: [Grant]

    enum CodingKeys: String, CodingKey {
        case today, usage, grants
        case timeZone = "time_zone"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        timeZone = c.lenient(.timeZone)
        today = c.lenient(.today) ?? ""
        usage = c.lenientList(.usage)
        grants = c.lenientList(.grants)
    }

    /// Seconds watched on `day`, every library together.
    public func seconds(on day: String) -> Int {
        usage.filter { $0.day == day }.reduce(0) { $0 + $1.seconds }
    }

    /// Minutes given on `day`.
    public func granted(on day: String) -> Int {
        grants.filter { $0.day == day }.reduce(0) { $0 + $1.minutes }
    }
}

// MARK: live TV (57.10)

/// `GET /api/channels/{id}/access`: whether the programme on now is within the caller's limit.
public struct ChannelAccess: Decodable, Equatable, Sendable {
    public let restricted: Bool
    public let allowed: Bool
    /// When the answer may change (nil: nothing in the next 12 hours changes it).
    public let until: Date?

    public init(restricted: Bool, allowed: Bool, until: Date? = nil) {
        self.restricted = restricted
        self.allowed = allowed
        self.until = until
    }

    enum CodingKeys: String, CodingKey { case restricted, allowed, until }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        restricted = c.lenient(.restricted) ?? false
        allowed = c.lenient(.allowed) ?? true
        until = c.lenient(.until)
    }

    /// How long to wait before asking again: until `until` (at least 5 s, at most 5 minutes,
    /// so a guardian's change shows without the programme changing).
    public func askAgain(after now: Date = .now) -> TimeInterval {
        guard let until else { return 300 }
        return min(300, max(5, until.timeIntervalSince(now) + 1))
    }
}

/// `POST /api/channels/{id}/play`: the channel's signed stream address.
public struct ChannelPlay: Decodable, Sendable {
    public let url: String
    public let expiresAt: Date?
    public let until: Date?

    enum CodingKeys: String, CodingKey {
        case url, until
        case expiresAt = "expires_at"
    }
}

// MARK: the routes

private struct RequestAnswer: Decodable { let request: ParentalRequest }
private struct PeopleAnswer: Decodable {
    let guardians: [ParentalPerson]
    init(from decoder: Decoder) throws {
        guardians = try decoder.container(keyedBy: AnyKey.self).lenientList(AnyKey(stringValue: "guardians"))
    }
}

/// `POST /api/me/age-signal`'s answer.
public struct AgeSignalAnswer: Decodable, Equatable, Sendable {
    /// The device limit now in force on this sign-in (nil: the device limits nothing).
    public let tierLimit: Int?
    /// What was sent is looser than the limit in force: it waits for a guardian.
    public let pendingLooser: Bool
    /// Refresh the access token now, so the next one carries the limit.
    public let refresh: Bool

    enum CodingKeys: String, CodingKey {
        case refresh
        case tierLimit = "tier_limit"
        case pendingLooser = "pending_looser"
    }

    public init(tierLimit: Int?, pendingLooser: Bool = false, refresh: Bool = true) {
        self.tierLimit = tierLimit
        self.pendingLooser = pendingLooser
        self.refresh = refresh
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        tierLimit = c.lenient(.tierLimit)
        pendingLooser = c.lenient(.pendingLooser) ?? false
        refresh = c.lenient(.refresh) ?? true
    }
}

public extension APIClient {
    // MARK: the caller

    /// `GET /api/me/parental`; nil on a home from before Epic 57 (404).
    func myParental() async throws -> ParentalLimit? {
        var r = request("GET", "/api/me/parental")
        r.cachePolicy = .reloadIgnoringLocalCacheData
        let (data, response) = try await exchange(r, answered: [404])
        if response?.statusCode == 404 { return nil }
        do {
            return try JSONDecoder.home.decode(ParentalLimit.self, from: data)
        } catch {
            throw APIError.decoding(String(describing: error))
        }
    }

    /// `GET /api/me/parental/time` (for one library's rules too, when given); nil on a home
    /// from before the time rules (404).
    func myParentalTime(library: String? = nil) async throws -> ParentalTime? {
        var r = request("GET", "/api/me/parental/time", query: library.map { ["library_id": $0] } ?? [:])
        r.cachePolicy = .reloadIgnoringLocalCacheData
        let (data, response) = try await exchange(r, answered: [404])
        if response?.statusCode == 404 { return nil }
        do {
            return try JSONDecoder.home.decode(ParentalTime.self, from: data)
        } catch {
            throw APIError.decoding(String(describing: error))
        }
    }

    /// `POST /api/me/age-signal` with what the device said (`AgeSignalReading.body`).
    func sendAgeSignal(_ body: [String: Any]) async throws -> AgeSignalAnswer {
        try await send(request("POST", "/api/me/age-signal", body: body))
    }

    /// A new access token now (after an age signal: the next token carries the device limit).
    func refreshAccessToken() async throws {
        _ = try await refreshOnce()
    }

    // MARK: ask a parent, the parent PIN (57.06)

    /// `POST /api/me/parental/requests`: asks the guardians to allow one item. Asking again
    /// while the first request waits answers that request.
    func askParent(_ subject: ParentalSubject) async throws -> ParentalRequest {
        let a: RequestAnswer = try await send(request("POST", "/api/me/parental/requests",
                                                      body: ["subject_kind": subject.kind, "subject_id": subject.id]))
        return a.request
    }

    /// `GET /api/me/parental/guardians`: who has a parent PIN (a restricted account only).
    func parentalUnlockers() async throws -> [ParentalPerson] {
        let a: PeopleAnswer = try await get("/api/me/parental/guardians")
        return a.guardians
    }

    /// `POST /api/me/parental/unlock`: a guardian's PIN opens one item until 04:00 home time.
    func unlock(_ subject: ParentalSubject, guardian: String, pin: String) async throws {
        _ = try await sendRaw(request("POST", "/api/me/parental/unlock", body: [
            "guardian_id": guardian, "pin": pin, "subject_kind": subject.kind, "subject_id": subject.id,
        ]), redirects: SameOriginOnly.shared)
    }

    /// `GET /api/me/parental-pin`: whether the caller has set a parent PIN.
    func parentalPINSet() async throws -> Bool {
        struct A: Decodable { let set: Bool }
        let a: A = try await get("/api/me/parental-pin")
        return a.set
    }

    /// `PUT /api/me/parental-pin`: sets the caller's six-digit PIN; the account's password
    /// confirms it.
    func setParentalPIN(_ pin: String, password: String) async throws {
        _ = try await sendRaw(request("PUT", "/api/me/parental-pin", body: ["pin": pin, "password": password]),
                              redirects: SameOriginOnly.shared)
    }

    // MARK: guardians and admins (57.02, 57.06, 57.07)

    /// `GET /api/parental/children`: empty for anyone who looks after no one.
    func parentalChildren() async throws -> [ParentalChild] {
        struct A: Decodable {
            let children: [ParentalChild]
            init(from decoder: Decoder) throws {
                children = try decoder.container(keyedBy: AnyKey.self).lenientList(AnyKey(stringValue: "children"))
            }
        }
        var r = request("GET", "/api/parental/children")
        r.cachePolicy = .reloadIgnoringLocalCacheData
        let (data, response) = try await exchange(r, answered: [404])
        if response?.statusCode == 404 { return [] }
        return (try? JSONDecoder.home.decode(A.self, from: data))?.children ?? []
    }

    func parentalAccount(_ user: String) async throws -> ParentalAccount {
        try await get("/api/users/\(Self.segment(user))/parental")
    }

    /// `PUT /api/users/{id}/parental`: sets the limit (and makes an account restricted, for
    /// an admin). `version` is the one last read; a change over someone else's answers 409.
    func setParental(_ user: String, maxTier: Int, unrated: String, allowOnly: Bool, version: Int) async throws -> ParentalAccount {
        try await send(request("PUT", "/api/users/\(Self.segment(user))/parental", body: [
            "max_tier": maxTier, "unrated": unrated, "allow_only": allowOnly, "version": version,
        ]))
    }

    /// `DELETE /api/users/{id}/parental` (admins): the account stops being restricted.
    func liftParental(_ user: String) async throws {
        _ = try await sendRaw(request("DELETE", "/api/users/\(Self.segment(user))/parental"))
    }

    func addGuardian(_ guardian: String, of user: String) async throws {
        _ = try await sendRaw(request("POST", "/api/users/\(Self.segment(user))/parental/guardians", body: ["guardian_id": guardian]))
    }

    func removeGuardian(_ guardian: String, of user: String) async throws {
        _ = try await sendRaw(request("DELETE", "/api/users/\(Self.segment(user))/parental/guardians/\(Self.segment(guardian))"))
    }

    func parentalRules(_ user: String) async throws -> [ParentalRule] {
        struct A: Decodable {
            let rules: [ParentalRule]
            init(from decoder: Decoder) throws {
                rules = try decoder.container(keyedBy: AnyKey.self).lenientList(AnyKey(stringValue: "rules"))
            }
        }
        let a: A = try await get("/api/users/\(Self.segment(user))/parental/rules")
        return a.rules
    }

    func addParentalRule(_ user: String, effect: String, subject: ParentalSubject,
                         ending: ParentalEnding = .never) async throws -> ParentalRule {
        var body: [String: Any] = ["effect": effect, "subject_kind": subject.kind, "subject_id": subject.id]
        body.merge(ending.body) { _, new in new }
        return try await send(request("POST", "/api/users/\(Self.segment(user))/parental/rules", body: body))
    }

    func removeParentalRule(_ rule: String, of user: String) async throws {
        _ = try await sendRaw(request("DELETE", "/api/users/\(Self.segment(user))/parental/rules/\(Self.segment(rule))"))
    }

    /// `POST /api/users/{id}/parental/signals/{family}/clear`: applies a waiting looser
    /// statement, or removes the signal when none waits.
    func clearSignal(_ family: String, of user: String) async throws {
        _ = try await sendRaw(request("POST", "/api/users/\(Self.segment(user))/parental/signals/\(Self.segment(family))/clear"))
    }

    /// `GET /api/admin/library-acl` (admins): the home's accounts, by name. The one admin
    /// route that lists them, as the web's Users and Family pages read it; anyone else is
    /// refused by the home.
    func homeAccounts() async throws -> [ParentalPerson] {
        struct A: Decodable {
            let users: [ParentalPerson]
            init(from decoder: Decoder) throws {
                users = try decoder.container(keyedBy: AnyKey.self).lenientList(AnyKey(stringValue: "users"))
            }
        }
        let a: A = try await get("/api/admin/library-acl")
        return a.users
    }

    /// `GET /api/libraries/manage` (admins): each library's stored default tier. A library is
    /// in the answer only when the home says its tier (`age_tier`: a tier, or null for none);
    /// a home from before that field says nothing, so nothing is known.
    func libraryAgeTiers() async throws -> [String: Int?] {
        struct Row: Decodable {
            let id: String
            let known: Bool
            let tier: Int?
            init(from decoder: Decoder) throws {
                let c = try decoder.container(keyedBy: AnyKey.self)
                id = try c.decode(String.self, forKey: AnyKey(stringValue: "id"))
                let key = AnyKey(stringValue: "age_tier")
                known = c.contains(key)
                tier = c.lenient(key)
            }
        }
        struct A: Decodable {
            let items: [Row]
            init(from decoder: Decoder) throws {
                items = try decoder.container(keyedBy: AnyKey.self).lenientList(AnyKey(stringValue: "items"))
            }
        }
        let a: A = try await get("/api/libraries/manage")
        var out: [String: Int?] = [:]
        for row in a.items where row.known { out[row.id] = .some(row.tier) }
        return out
    }

    /// `GET /api/parental/requests?status=pending`: the requests the caller may answer.
    func parentalRequests(status: String = "pending") async throws -> [ParentalRequestItem] {
        struct A: Decodable {
            let requests: [ParentalRequestItem]
            init(from decoder: Decoder) throws {
                requests = try decoder.container(keyedBy: AnyKey.self).lenientList(AnyKey(stringValue: "requests"))
            }
        }
        let a: A = try await get("/api/parental/requests", query: ["status": status])
        return a.requests
    }

    /// `ending` for an item's allow rule (never for a time request: the home answers 422);
    /// `minutes` for a time request (nil: as asked).
    func approveRequest(_ id: String, ending: ParentalEnding = .never, minutes: Int? = nil) async throws {
        var body: [String: Any] = ending.body
        if let minutes { body["minutes"] = minutes }
        _ = try await sendRaw(request("POST", "/api/parental/requests/\(Self.segment(id))/approve", body: body))
    }

    func denyRequest(_ id: String) async throws {
        _ = try await sendRaw(request("POST", "/api/parental/requests/\(Self.segment(id))/deny"))
    }

    /// `GET /api/users/{id}/parental/activity`: newest first, 50 a page.
    func parentalActivity(_ user: String, kind: String? = nil, cursor: String? = nil) async throws -> ParentalActivityPage {
        var q: [String: String] = [:]
        if let kind { q["kind"] = kind }
        if let cursor { q["cursor"] = cursor }
        return try await get("/api/users/\(Self.segment(user))/parental/activity", query: q)
    }

    // MARK: time rules (57.14)

    func timeRules(_ user: String) async throws -> ParentalTimeRules {
        try await get("/api/parental/users/\(Self.segment(user))/time-rules")
    }

    /// `PUT …/time-rules`: replaces the account's rules.
    func setTimeRules(_ user: String, _ rules: [ParentalTimeRule]) async throws -> ParentalTimeRules {
        try await send(request("PUT", "/api/parental/users/\(Self.segment(user))/time-rules", body: ["rules": rules.map(\.body)]))
    }

    func timeUsage(_ user: String) async throws -> ParentalTimeUsage {
        try await get("/api/parental/users/\(Self.segment(user))/time-usage")
    }

    /// `POST …/time-grants`: more time today.
    func grantTime(_ user: String, minutes: Int) async throws {
        _ = try await sendRaw(request("POST", "/api/parental/users/\(Self.segment(user))/time-grants", body: ["minutes": minutes]))
    }

    // MARK: rating libraries (57.01)

    /// `PUT /api/libraries/{id}/age-tier` (admins): the library's default tier, nil for none.
    func setLibraryAgeTier(_ library: String, _ tier: Int?) async throws {
        _ = try await sendRaw(request("PUT", "/api/libraries/\(Self.segment(library))/age-tier",
                                      body: ["tier": tier.map { $0 as Any } ?? NSNull()]))
    }

    // MARK: live TV (57.10)

    func channelAccess(_ channel: String) async throws -> ChannelAccess {
        var r = request("GET", "/api/channels/\(Self.segment(channel))/access")
        r.cachePolicy = .reloadIgnoringLocalCacheData
        return try await send(r)
    }

    /// `POST /api/channels/{id}/play`: the signed address; 403 `restricted` (reason
    /// `programme`, `retry_at`) while the programme on now is above the limit.
    func playChannel(_ channel: String) async throws -> ChannelPlay {
        try await send(request("POST", "/api/channels/\(Self.segment(channel))/play"))
    }

    // MARK: progress (57.14: the stream's heartbeat)

    /// `POST /api/stream/sessions/{id}/progress`, telling a refusal: the home answers 403
    /// `parental-time` when the account's time is over, and playback stops there. A restricted
    /// account's stream is served only while this is posted at least every 60 seconds.
    func postProgress(session: String, positionSec: Int, completed: Bool = false) async throws {
        var body: [String: Any] = ["position_sec": positionSec]
        if completed { body["completed"] = true }
        _ = try await sendRaw(request("POST", "/api/stream/sessions/\(Self.segment(session))/progress", body: body))
    }
}
