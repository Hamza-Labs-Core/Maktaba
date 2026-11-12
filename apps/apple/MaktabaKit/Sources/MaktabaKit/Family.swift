import Foundation
import Observation

// Epic 57 stories 57.07, 57.08 and 57.15: what a guardian's Family screens show and do. The
// home enforces every rule (a viewer with no child gets 403 or an empty list whatever the
// app shows); these models only call its routes and say what it answered.

/// What a Family screen says after a change the home refused.
public enum FamilyNotice: Equatable, Sendable {
    /// 409 `stale`: someone else changed it; the latest is shown.
    case conflict
    /// 403 for a play-only sign-in through Maktaba Cloud, or a guardian doing an admin's part.
    case readOnly
    /// 403 `device_limited`: this device is itself limited.
    case deviceLimited
    case wrongPassword
    /// 409 `restricted_admin`: an admin cannot be made restricted.
    case adminAccount
    /// 409 `restricted_guardian`: a restricted account cannot look after another.
    case restrictedGuardian
    case failed(HomeFailure)

    public init(_ error: Error) {
        switch ParentalRefusal(error) {
        case .stale: self = .conflict
        case .notYours, .restricted: self = .readOnly
        case .deviceLimited: self = .deviceLimited
        case .passwordWrong: self = .wrongPassword
        case .restrictedAdmin: self = .adminAccount
        case .restrictedGuardian: self = .restrictedGuardian
        default:
            let f = HomeFailure(error)
            self = f.kind == .noAccess ? .readOnly : .failed(f)
        }
    }

    public func line(_ locale: Locale = AppLanguage.locale) -> String {
        switch self {
        case .conflict: ParentalWords.text("family.conflict", locale)
        case .readOnly: ParentalWords.text("family.readOnlyCloud", locale)
        case .deviceLimited: ParentalWords.text("family.deviceLimited", locale)
        case .wrongPassword: ParentalWords.text("family.pin.wrongPassword", locale)
        case .adminAccount: ParentalWords.text("family.makeRestricted.admin", locale)
        case .restrictedGuardian: ParentalWords.text("family.guardians.restricted", locale)
        case let .failed(f): f.message(video: false)
        }
    }
}

/// Profile > Family: the children the caller looks after, the requests waiting, the
/// caller's parent PIN.
@MainActor @Observable
public final class FamilyModel {
    public let client: APIClient
    /// The caller is an admin of the home: every restricted account, Guardians, Lift
    /// restriction and Rate libraries.
    public let isAdmin: Bool

    public private(set) var children: Load<[ParentalChild]> = .loading
    public private(set) var requests: [ParentalRequestItem] = []
    /// nil: not known yet.
    public private(set) var pinSet: Bool?
    /// The home's accounts, for an admin (`GET /api/admin/library-acl`): who can be made
    /// restricted, who can be named a guardian. Empty for anyone else.
    public private(set) var accounts: [ParentalPerson] = []
    public var notice: FamilyNotice?

    public init(client: APIClient, isAdmin: Bool) {
        self.client = client
        self.isAdmin = isAdmin
    }

    public func load() async {
        do {
            let list = try await client.parentalChildren()
            children = .ready(Self.sorted(list))
        } catch {
            if Log.isCancellation(error) { return }
            children = .failed(HomeFailure(error))
        }
        if let r = try? await client.parentalRequests() { requests = r }
        if let set = try? await client.parentalPINSet() { pinSet = set }
        if isAdmin, let people = try? await client.homeAccounts() { accounts = Self.sorted(people) }
    }

    public static func sorted(_ list: [ParentalPerson], _ locale: Locale = AppLanguage.locale) -> [ParentalPerson] {
        list.sorted { $0.username.compare($1.username, options: [.caseInsensitive], locale: locale) == .orderedAscending }
    }

    /// The accounts an admin may make restricted: everyone who is not restricted already and
    /// is not the admin themself (`me`). The list does not say who else is an admin: the home
    /// refuses one (409 `restricted_admin`), and the screen says so.
    public func restrictable(me: String) -> [ParentalPerson] {
        let children = Set((children.value ?? []).map(\.id))
        return accounts.filter { $0.id != me && !children.contains($0.id) }
    }

    /// Makes `person` restricted at `maxTier` (an admin; `PUT /api/users/{id}/parental` with
    /// no version: a new limit), unrated content hidden. True when the home took it.
    public func restrict(_ person: ParentalPerson, maxTier: Int) async -> Bool {
        do {
            _ = try await client.setParental(person.id, maxTier: maxTier, unrated: "hide", allowOnly: false, version: 0)
            await load()
            return true
        } catch {
            if !Log.isCancellation(error) { notice = FamilyNotice(error) }
            return false
        }
    }

    /// By name, in the app's language's order (Arabic collation in Arabic).
    public static func sorted(_ list: [ParentalChild], _ locale: Locale = AppLanguage.locale) -> [ParentalChild] {
        list.sorted { $0.username.compare($1.username, options: [.caseInsensitive], locale: locale) == .orderedAscending }
    }

    /// How many requests wait for `child`.
    public func waiting(for child: String) -> Int {
        requests.filter { $0.child.id == child }.count
    }

    public func approve(_ item: ParentalRequestItem, for length: ApprovalLength) async {
        // more time is for today only: the home makes a grant of the minutes asked
        if item.request.isTime {
            await answer(item) { try await self.client.approveRequest(item.id) }
            return
        }
        await answer(item) {
            let ending = length.ending()
            do {
                try await self.client.approveRequest(item.id, ending: ending)
            } catch where ending == .dayEnd && ParentalEnding.untilRefused(error) {
                // a home from before `until`: the day's end is worked out here, in the
                // home's time zone (its time rules say it), not this device's
                let zone = (try? await self.client.timeRules(item.child.id))?.timeZone
                let calendar = ApprovalLength.homeCalendar(timeZone: zone)
                try await self.client.approveRequest(item.id, ending: length.expiresAt(calendar: calendar).map { .at($0) } ?? .never)
            }
        }
    }

    public func deny(_ item: ParentalRequestItem) async {
        await answer(item) { try await self.client.denyRequest(item.id) }
    }

    private func answer(_ item: ParentalRequestItem, _ call: () async throws -> Void) async {
        do {
            try await call()
            requests.removeAll { $0.id == item.id }
        } catch {
            if Log.isCancellation(error) { return }
            // answered meanwhile, or gone: it leaves the list either way
            if ParentalRefusal(error) == .requestGone {
                requests.removeAll { $0.id == item.id }
            } else {
                notice = FamilyNotice(error)
            }
        }
    }

    /// Sets or changes the caller's PIN; true when the home took it.
    public func setPIN(_ pin: String, password: String) async -> Bool {
        guard let pin = PinPad.normalize(pin) else { return false }
        do {
            try await client.setParentalPIN(pin, password: password)
            pinSet = true
            return true
        } catch {
            if !Log.isCancellation(error) { notice = FamilyNotice(error) }
            return false
        }
    }
}

/// One child's page: the limit, the rules, the devices, the time.
@MainActor @Observable
public final class ChildModel {
    public let client: APIClient
    public let child: ParentalPerson

    public private(set) var account: Load<ParentalAccount> = .loading
    public private(set) var rules: [ParentalRule] = []
    public private(set) var timeRules: [ParentalTimeRule] = []
    public private(set) var timeZone: String?
    public private(set) var usage: ParentalTimeUsage?
    /// The names of the libraries the caller may see, for a rule's and the usage's rows.
    public private(set) var libraries: [String: String] = [:]
    /// The restriction was lifted: the page closes.
    public private(set) var lifted = false
    public var notice: FamilyNotice?

    public init(client: APIClient, child: ParentalPerson) {
        self.client = client
        self.child = child
    }

    public var policy: ParentalPolicy? { account.value?.policy }
    public var isAdmin: Bool { account.value?.isAdmin ?? false }

    public func load() async {
        do {
            account = .ready(try await client.parentalAccount(child.id))
        } catch {
            if Log.isCancellation(error) { return }
            account = .failed(HomeFailure(error))
            return
        }
        if let r = try? await client.parentalRules(child.id) { rules = r }
        if let t = try? await client.timeRules(child.id) {
            timeRules = t.rules
            timeZone = t.timeZone
        }
        usage = try? await client.timeUsage(child.id)
        if let libs = try? await client.librarySummaries() {
            libraries = Dictionary(libs.map { ($0.id, $0.name) }) { a, _ in a }
        }
    }

    /// Changes the limit. A change made over someone else's (409) reads the page again and
    /// says so.
    public func setLimit(maxTier: Int? = nil, unrated: String? = nil, allowOnly: Bool? = nil) async {
        guard let p = policy else { return }
        do {
            account = .ready(try await client.setParental(child.id, maxTier: maxTier ?? p.maxTier, unrated: unrated ?? p.unrated,
                                                          allowOnly: allowOnly ?? p.allowOnly, version: p.version))
        } catch {
            await failed(error)
        }
    }

    public func lift() async {
        do {
            try await client.liftParental(child.id)
            lifted = true
        } catch {
            await failed(error)
        }
    }

    public func rules(_ effect: String) -> [ParentalRule] { rules.filter { $0.effect == effect } }

    public func addRule(_ effect: String, _ subject: ParentalSubject) async {
        do {
            let rule = try await client.addParentalRule(child.id, effect: effect, subject: subject)
            rules.removeAll { $0.id == rule.id }
            rules.append(rule)
            await rereadAccount()
        } catch {
            await failed(error)
        }
    }

    /// The home moves the limit's `version` on every rule change: the account is read again,
    /// so the next change of the limit sends the version the home now has (else it answers
    /// 409 `stale` to the guardian's own change). Quiet on failure: a stale version then
    /// reads the page again, as for someone else's change.
    private func rereadAccount() async {
        if let fresh = try? await client.parentalAccount(child.id) { account = .ready(fresh) }
    }

    public func remove(_ rule: ParentalRule) async {
        do {
            try await client.removeParentalRule(rule.id, of: child.id)
            rules.removeAll { $0.id == rule.id }
            await rereadAccount()
        } catch {
            await failed(error)
        }
    }

    /// Who an admin may name a guardian of this child, from the home's `accounts`: not the
    /// child, not a guardian already, not another restricted account (`restricted`).
    public func guardianCandidates(_ accounts: [ParentalPerson], restricted: Set<String>) -> [ParentalPerson] {
        let have = Set((account.value?.guardians ?? []).map(\.id))
        return accounts.filter { $0.id != child.id && !have.contains($0.id) && !restricted.contains($0.id) }
    }

    public func addGuardian(_ guardian: ParentalPerson) async {
        do {
            try await client.addGuardian(guardian.id, of: child.id)
            await load()
        } catch {
            await failed(error)
        }
    }

    public func removeGuardian(_ guardian: ParentalPerson) async {
        do {
            try await client.removeGuardian(guardian.id, of: child.id)
            await load()
        } catch {
            await failed(error)
        }
    }

    /// A device's waiting looser limit is applied (Devices > Approve), or its signal removed.
    public func clear(_ signal: ParentalSignal) async {
        do {
            try await client.clearSignal(signal.id, of: child.id)
            await load()
        } catch {
            await failed(error)
        }
    }

    /// Replaces the account's time rules.
    public func saveTimeRules(_ rules: [ParentalTimeRule]) async -> Bool {
        do {
            let saved = try await client.setTimeRules(child.id, rules)
            timeRules = saved.rules
            timeZone = saved.timeZone ?? timeZone
            return true
        } catch {
            await failed(error)
            return false
        }
    }

    /// More time today.
    public func grant(minutes: Int) async {
        do {
            try await client.grantTime(child.id, minutes: minutes)
            usage = try? await client.timeUsage(child.id)
        } catch {
            await failed(error)
        }
    }

    /// What a rule names, as its row says it: a library's name, else the kind.
    public func title(of rule: ParentalRule, _ locale: Locale = AppLanguage.locale) -> String {
        if rule.subjectKind == "library", let name = libraries[rule.subjectID] { return name }
        return ParentalWords.text("family.subject.\(rule.subjectKind)", locale)
    }

    private func failed(_ error: Error) async {
        if Log.isCancellation(error) { return }
        let n = FamilyNotice(error)
        notice = n
        // the other guardian's change, shown
        if n == .conflict { await load() }
    }
}

// MARK: activity (57.08)

/// A child's activity, newest first, by day, with one filter.
@MainActor @Observable
public final class ActivityModel {
    public enum Filter: String, CaseIterable, Sendable {
        case all, watched, blocked, requests

        public var key: String { "activity.filter.\(rawValue)" }

        /// The kind the home is asked for (nil: every kind; Requests are three kinds, so
        /// they are picked here from every kind).
        var kind: String? {
            switch self {
            case .all, .requests: nil
            case .watched: "watched"
            case .blocked: "blocked"
            }
        }

        func shows(_ e: ParentalEvent) -> Bool {
            switch self {
            case .all: true
            case .watched: e.kind == "watched"
            case .blocked: e.kind == "blocked"
            case .requests: ["asked", "approved", "denied"].contains(e.kind)
            }
        }
    }

    /// One day's rows.
    public struct Day: Identifiable, Equatable, Sendable {
        public let id: Date
        public let events: [ParentalEvent]
    }

    private let client: APIClient
    public let child: ParentalPerson
    public private(set) var events: Load<[ParentalEvent]> = .loading
    public private(set) var next: String?
    public private(set) var filter = Filter.all

    public init(client: APIClient, child: ParentalPerson) {
        self.client = client
        self.child = child
    }

    public func load() async {
        await read(cursor: nil)
    }

    public func show(_ f: Filter) async {
        guard f != filter else { return }
        filter = f
        events = .loading
        await read(cursor: nil)
    }

    public func more() async {
        guard let next else { return }
        await read(cursor: next)
    }

    private func read(cursor: String?) async {
        let asked = filter
        do {
            let page = try await client.parentalActivity(child.id, kind: asked.kind, cursor: cursor)
            guard asked == filter else { return }
            let before = cursor == nil ? [] : (events.value ?? [])
            events = .ready(before + page.events)
            next = page.nextCursor
        } catch {
            guard !Log.isCancellation(error), asked == filter else { return }
            if cursor == nil { events = .failed(HomeFailure(error)) }
        }
    }

    /// The rows the filter shows, grouped by day in `calendar` (its time zone: the home's
    /// when known), newest day first.
    public func days(calendar: Calendar = .current) -> [Day] {
        Self.days(events.value ?? [], filter: filter, calendar: calendar)
    }

    public nonisolated static func days(_ events: [ParentalEvent], filter: Filter = .all, calendar: Calendar) -> [Day] {
        let shown = events.filter(filter.shows).sorted { $0.at > $1.at }
        let grouped = Dictionary(grouping: shown) { calendar.startOfDay(for: $0.at) }
        return grouped.keys.sorted(by: >).map { Day(id: $0, events: grouped[$0] ?? []) }
    }

    /// A day's heading: "Today", "Yesterday", else the date in the app's calendar and digits.
    public nonisolated static func heading(_ day: Date, now: Date = .now, calendar: Calendar = .current,
                                           _ locale: Locale = AppLanguage.locale) -> String {
        if calendar.isDate(day, inSameDayAs: now) { return ParentalWords.text("activity.day.today", locale) }
        if let y = calendar.date(byAdding: .day, value: -1, to: now), calendar.isDate(day, inSameDayAs: y) {
            return ParentalWords.text("activity.day.yesterday", locale)
        }
        var style = Date.FormatStyle(date: .complete, time: .omitted)
        style.locale = locale
        style.calendar = calendar
        style.timeZone = calendar.timeZone
        return day.formatted(style)
    }

    /// A row's sentence.
    public nonisolated static func line(_ e: ParentalEvent, _ locale: Locale = AppLanguage.locale) -> String {
        // a time event has minutes and no subject: "30 min more time"
        let more = e.minutes.map { ParentalWords.text("activity.time.more", locale, ParentalWords.length(seconds: $0 * 60, locale)) }
        let what = e.subject?.displayTitle(locale) ?? more ?? ""
        let who = e.guardian?.username ?? ""
        switch e.kind {
        case "watched": return ParentalWords.text("activity.kind.watched", locale, what)
        case "blocked": return ParentalWords.text("activity.kind.blocked", locale)
        case "asked": return ParentalWords.text("activity.kind.asked", locale, what)
        case "approved": return ParentalWords.text("activity.kind.approved", locale, who, what)
        case "denied": return ParentalWords.text("activity.kind.denied", locale, who, what)
        case "pin_locked": return ParentalWords.text("activity.kind.pinLocked", locale, who.isEmpty ? what : who)
        case "signal": return ParentalWords.text("activity.kind.signal", locale, e.device ?? what)
        case "time_up": return ParentalWords.text("activity.kind.timeUp", locale)
        case "time_granted":
            guard let m = e.minutes else { return ParentalWords.text("activity.kind.timeGranted", locale) }
            return ParentalWords.text("activity.kind.timeGrantedMinutes", locale, ParentalWords.length(seconds: m * 60, locale))
        default: return e.kind.replacingOccurrences(of: "_", with: " ")
        }
    }

    /// A row's SF Symbol.
    public nonisolated static func symbol(_ kind: String) -> String {
        switch kind {
        case "watched": "play.circle"
        case "blocked": "lock"
        case "asked": "hand.raised"
        case "approved": "checkmark.circle"
        case "denied": "xmark.circle"
        case "pin_locked": "lock.trianglebadge.exclamationmark"
        case "signal": "iphone"
        case "time_up": "hourglass.bottomhalf.filled"
        case "time_granted": "clock.badge.checkmark"
        default: "circle"
        }
    }
}

// MARK: time rules, as the editor shows them (57.15)

public enum TimeRuleWords {
    /// The home days in a week, bit 0 Sunday … bit 6 Saturday, each with its short name.
    public static func days(_ locale: Locale = AppLanguage.locale) -> [(bit: Int, name: String)] {
        var calendar = Calendar(identifier: .gregorian)
        calendar.locale = locale
        let names = calendar.shortStandaloneWeekdaySymbols // Sunday first
        return names.enumerated().map { (bit: $0.offset, name: $0.element) }
    }

    /// A rule in one line: its days, its slot, its daily limit, its library.
    public static func summary(_ rule: ParentalTimeRule, library: String? = nil, _ locale: Locale = AppLanguage.locale) -> String {
        var parts: [String] = []
        if rule.days == ParentalTimeRule.everyDay {
            parts.append(ParentalWords.text("parental.time.everyDay", locale))
        } else {
            parts.append(days(locale).filter { rule.covers(day: $0.bit) }.map(\.name).formatted(.list(type: .and, width: .narrow).locale(locale)))
        }
        if let s = rule.slotStart, let e = rule.slotEnd { parts.append("\u{200E}\(s)–\(e)") }
        if let m = rule.dailyMinutes { parts.append(ParentalWords.length(seconds: m * 60, locale)) }
        if let library { parts.append(library) }
        return parts.joined(separator: " · ")
    }

    /// "HH:MM" from a time of day, and back (for the editor's pickers).
    public static func clock(_ date: Date, calendar: Calendar = .current) -> String {
        let c = calendar.dateComponents([.hour, .minute], from: date)
        return String(format: "%02d:%02d", c.hour ?? 0, c.minute ?? 0)
    }

    public static func date(_ clock: String, calendar: Calendar = .current, on day: Date = .now) -> Date {
        let p = clock.split(separator: ":").compactMap { Int($0) }
        guard p.count >= 2 else { return day }
        return calendar.date(bySettingHour: p[0], minute: p[1], second: 0, of: day) ?? day
    }
}

// MARK: what a rule may name (57.07)

/// One thing a guardian may pick for an allow or block rule.
public struct RuleChoice: Identifiable, Hashable, Sendable {
    public let id: String
    public let title: String

    public init(id: String, title: String) {
        self.id = id
        self.title = title
    }
}

/// The things of each kind a rule may name, read from what the guardian's own account sees.
/// The home decides who may read each list (plugins are an admin's: a guardian who is not
/// one gets the home's refusal, shown in the picker), and checks the rule itself.
public enum RuleChoices {
    /// The kinds the Add menu offers, in its order.
    public static let kinds = ["library", "collection", "series", "channel", "youtube_source", "plugin", "video"]

    /// The choices of `kind`, by title in the app's language's order. A video is searched
    /// for (`query`, at least two characters); the other kinds are listed whole.
    public static func load(_ client: APIClient, kind: String, query: String = "",
                            _ locale: Locale = AppLanguage.locale) async throws -> [RuleChoice] {
        let found: [RuleChoice]
        switch kind {
        case "library":
            found = try await client.librarySummaries().map { RuleChoice(id: $0.id, title: $0.name) }
        case "collection":
            // the home's own collections (a rule names a row of `collections`); the pipeline's
            // topic and clip groups are not collections a rule can name
            var all: [RuleChoice] = []
            for library in try await client.librarySummaries() {
                let list = try await client.libraryCollections(library.id)
                all += list.filter { $0.kind != "topic" && $0.kind != "clip" }
                    .map { RuleChoice(id: $0.id, title: joined(library.name, $0.name)) }
            }
            found = all
        case "series":
            found = try await client.seriesList().map { RuleChoice(id: $0.id, title: $0.name) }
        case "channel":
            found = try await client.channelsNow().channels.map { RuleChoice(id: $0.channel.id, title: $0.channel.name) }
        case "youtube_source":
            var all: [RuleChoice] = []
            for library in try await client.librarySummaries() {
                all += try await client.youtubeSources(library: library.id)
                    .map { RuleChoice(id: $0.id, title: joined(library.name, $0.name)) }
            }
            found = all
        case "plugin":
            found = try await client.installedPlugins(locale)
        default:
            let q = query.trimmingCharacters(in: .whitespacesAndNewlines)
            guard q.count >= 2 else { return [] }
            found = try await client.search(q).titles.map { RuleChoice(id: $0.videoID, title: $0.title) }
        }
        var seen = Set<String>()
        return found.filter { seen.insert($0.id).inserted }
            .sorted { $0.title.compare($1.title, options: [.caseInsensitive], locale: locale) == .orderedAscending }
    }

    /// "Lectures · Seerah": the library first, as the two read in either direction.
    static func joined(_ library: String, _ name: String) -> String {
        library.isEmpty ? name : "\(library) · \(name)"
    }
}

/// A YouTube source of a library (`GET /api/libraries/{id}/youtube/sources`): a channel or a
/// playlist the home downloads from.
public struct YouTubeSourceRow: Decodable, Identifiable, Hashable, Sendable {
    public let id: String
    public let url: String
    public let folder: String?

    /// Its folder's name, else its address (as the home names it in a request).
    public var name: String { folder?.isEmpty == false ? folder ?? url : url }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        id = try c.decode(String.self, forKey: AnyKey(stringValue: "id"))
        url = c.lenient(AnyKey(stringValue: "url")) ?? ""
        folder = c.lenient(AnyKey(stringValue: "folder"))
    }
}

public extension APIClient {
    func youtubeSources(library: String) async throws -> [YouTubeSourceRow] {
        struct A: Decodable {
            let items: [YouTubeSourceRow]
            init(from decoder: Decoder) throws {
                items = try decoder.container(keyedBy: AnyKey.self).lenientList(AnyKey(stringValue: "items"))
            }
        }
        let a: A = try await get("/api/libraries/\(Self.segment(library))/youtube/sources")
        return a.items
    }

    /// `GET /api/plugins` (admins): the installed plugins, each by its name in the app's
    /// language (else its English, else its id).
    func installedPlugins(_ locale: Locale = AppLanguage.locale) async throws -> [RuleChoice] {
        struct Names: Decodable { let name: [String: String]? }
        struct Plugin: Decodable {
            let id: String
            let names: Names?
        }
        struct A: Decodable {
            let plugins: [Plugin]
            init(from decoder: Decoder) throws {
                plugins = try decoder.container(keyedBy: AnyKey.self).lenientList(AnyKey(stringValue: "plugins"))
            }
        }
        let a: A = try await get("/api/plugins")
        let language = locale.language.languageCode?.identifier ?? "en"
        return a.plugins.map { p in
            let name = p.names?.name?[language] ?? p.names?.name?["en"] ?? ""
            return RuleChoice(id: p.id, title: name.isEmpty ? p.id : name)
        }
    }
}
