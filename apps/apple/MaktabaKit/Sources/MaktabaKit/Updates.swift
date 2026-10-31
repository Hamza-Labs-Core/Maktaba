import Foundation

// The home's automatic updates as Maktaba Remote shows them (read only): what runs, what
// is planned and why it waits, the releases that are blocked, the active or last run with
// its steps and machines, every machine's release, a TV held at an update.
//
//   GET /api/cluster/updates            UpdatesOverview
//   GET /api/cluster/updates/runs/{id}  UpdateRunView
//
// The shapes are api/internal/updates (handlers.go, runs.go, scheduler.go, target.go,
// tvheld.go) and web/src/lib/updatesApi.ts; every field is read tolerantly (Lenient.swift).

/// `GET /api/cluster/updates`.
public struct UpdatesOverview: Decodable, Sendable {
    public let currentVersion: String
    public let watermark: String
    public let autoInstall: Bool
    public let channel: String?
    public let window: String?
    public let plan: UpdatesPlan
    /// The run under way (absent when none is).
    public let run: UpdateRun?
    public let lastRun: UpdateRun?
    public let catalog: [CatalogRelease]
    public let lastCheckOK: Date?
    public let lastCheckError: String?
    public let checkStale: Bool
    public let machines: [UpdateMachine]
    public let stuck: UpdatesStuck?
    public let tvHeld: TVHeldNow?

    enum CodingKeys: String, CodingKey {
        case watermark, policy, plan, run, catalog, machines, stuck
        case currentVersion = "current_version"
        case lastRun = "last_run"
        case lastCheckOK = "last_check_ok"
        case lastCheckError = "last_check_error"
        case checkStale = "check_stale"
        case tvHeld = "tv_held"
    }

    private struct Policy: Decodable {
        let autoInstall: Bool?
        let channel: String?
        let window: String?
        enum CodingKeys: String, CodingKey {
            case channel, window
            case autoInstall = "auto_install"
        }
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        currentVersion = c.lenient(.currentVersion) ?? ""
        watermark = c.lenient(.watermark) ?? ""
        let policy: Policy? = c.lenient(.policy)
        autoInstall = policy?.autoInstall ?? false
        channel = policy?.channel
        window = policy?.window
        plan = c.lenient(.plan) ?? UpdatesPlan()
        run = c.lenient(.run)
        lastRun = c.lenient(.lastRun)
        catalog = c.lenientList(.catalog)
        lastCheckOK = c.lenient(.lastCheckOK)
        lastCheckError = c.lenient(.lastCheckError)
        checkStale = c.lenient(.checkStale) ?? false
        machines = c.lenientList(.machines)
        stuck = c.lenient(.stuck)
        tvHeld = c.lenient(.tvHeld)
    }

    /// The run under way when it is not over, else nil.
    public var activeRun: UpdateRun? {
        guard let run, !run.ended else { return nil }
        return run
    }

    /// The run the screen shows: the one under way, else the last one.
    public var shownRun: UpdateRun? { activeRun ?? lastRun ?? run }

    /// The releases shown as blocked, as the web's Updates page: blocked and not withdrawn,
    /// newer than what runs (the watermark, else the current version), and no newer release
    /// that is neither blocked nor withdrawn (one a newer release passed by is history).
    public var blockedReleases: [CatalogRelease] {
        let current = watermark.isEmpty ? currentVersion : watermark
        return catalog.filter { r in
            guard let reason = r.blockedReason, !reason.isEmpty, r.withdrawnAt == nil else { return false }
            let newer = !ReleaseVersion.valid(current) || !ReleaseVersion.valid(r.version)
                || ReleaseVersion.compare(r.version, current) > 0
            guard newer else { return false }
            return !catalog.contains { o in
                (o.blockedReason ?? "").isEmpty && o.withdrawnAt == nil
                    && ReleaseVersion.valid(o.version) && ReleaseVersion.valid(r.version)
                    && ReleaseVersion.compare(o.version, r.version) > 0
            }
        }
    }

    /// What happens next to the home, in a sentence (the web's `homeNext`).
    public func next(now: Date = Date(), calendar: Calendar = .current, locale: Locale = AppLanguage.locale) -> String {
        if let run = activeRun { return kit("Updating to \(run.version) now.", locale) }
        guard let target = plan.target else { return kit("Nothing to install: up to date.", locale) }
        let version = target.version
        if plan.held { return kit("Version \(version) waits for your approval (Live TV -> Changes).", locale) }
        if let at = plan.nextRunAt {
            let day = UpdateWords.day(at, now: now, calendar: calendar, locale: locale)
            let span = UpdateWords.span(until: at, now: now, locale: locale)
            if let (from, to) = UpdateWords.windowEnds(window) {
                return kit("\(version) installs \(day) between \(from) and \(to) (\(span)).", locale)
            }
            var time = Date.FormatStyle(date: .omitted, time: .shortened).locale(locale)
            time.timeZone = calendar.timeZone
            return kit("\(version) installs \(day) at \(at.formatted(time)) (\(span)).", locale)
        }
        return UpdateWords.planWaiting(plan.waiting ?? "auto_install_off", version: version, locale: locale)
    }
}

/// The next planned update as the home's policy sees it.
public struct UpdatesPlan: Decodable, Sendable {
    public var target: CatalogRelease?
    public var newest: CatalogRelease?
    /// The target breaks the previous home release and waits for an admin's approval.
    public var held = false
    /// The newest release cannot be installed from the running one: the target is the
    /// newest that can, and the next window continues from there.
    public var steppingStone = false
    /// up_to_date, no_upgrade_path, needs_approval, ...
    public var reason: String?
    public var due = false
    public var nextRunAt: Date?
    public var eligibleAt: Date?
    /// Why it is not due now: auto_install_off, deferred, release_age, outside_window, ...
    public var waiting: String?

    enum CodingKeys: String, CodingKey {
        case target, newest, held, reason, due, waiting
        case steppingStone = "stepping_stone"
        case nextRunAt = "next_run_at"
        case eligibleAt = "eligible_at"
    }

    public init() {}

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        target = c.lenient(.target)
        newest = c.lenient(.newest)
        held = c.lenient(.held) ?? false
        steppingStone = c.lenient(.steppingStone) ?? false
        reason = c.lenient(.reason)
        due = c.lenient(.due) ?? false
        nextRunAt = c.lenient(.nextRunAt)
        eligibleAt = c.lenient(.eligibleAt)
        waiting = c.lenient(.waiting)
    }
}

/// A release of the home's catalog.
public struct CatalogRelease: Decodable, Identifiable, Sendable {
    public let version: String
    public let channel: String?
    public let publishedAt: Date?
    /// unverified, verified, failed.
    public let verifyState: String?
    public let withdrawnAt: Date?
    /// Why automatic runs no longer install it: attempts, rolled_back, verify, rehearsal, ...
    public let blockedReason: String?
    public let attempts: Int?

    public var id: String { version }

    enum CodingKeys: String, CodingKey {
        case version, channel, attempts
        case publishedAt = "published_at"
        case verifyState = "verify_state"
        case withdrawnAt = "withdrawn_at"
        case blockedReason = "blocked_reason"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        // a release without a version is not one
        version = try c.decode(String.self, forKey: .version)
        channel = c.lenient(.channel)
        publishedAt = c.lenient(.publishedAt)
        verifyState = c.lenient(.verifyState)
        withdrawnAt = c.lenient(.withdrawnAt)
        blockedReason = c.lenient(.blockedReason)
        attempts = c.lenient(.attempts)
    }
}

/// An update run, as the overview lists it.
public struct UpdateRun: Decodable, Identifiable, Sendable {
    public let id: String
    public let version: String
    public let fromVersion: String?
    /// auto, manual, catch_up, retry.
    public let trigger: String?
    public let state: String
    public let step: String?
    public let startedAt: Date?
    public let finishedAt: Date?
    /// code, step, detail, reason: the server's (its shape varies by code).
    public let error: [String: String]

    enum CodingKeys: String, CodingKey {
        case id, version, trigger, state, step, error
        case fromVersion = "from_version"
        case startedAt = "started_at"
        case finishedAt = "finished_at"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        version = c.lenient(.version) ?? ""
        fromVersion = c.lenient(.fromVersion)
        trigger = c.lenient(.trigger)
        state = c.lenient(.state) ?? ""
        step = c.lenient(.step)
        startedAt = c.lenient(.startedAt)
        finishedAt = c.lenient(.finishedAt)
        error = c.lenientFields(.error)
    }

    /// The states of a run that are over (the web's RUN_ENDED).
    public static let endedStates: Set<String> = [
        "done", "done_with_failures", "done_needs_person", "held", "failed", "rolled_back",
        "failed_rollback", "cancelled", "superseded",
    ]

    public var ended: Bool { Self.endedStates.contains(state) }

    /// The error in words: its code's (or its step's failure), else nil.
    public var errorText: String? {
        guard let code = error["code"], !code.isEmpty else { return nil }
        return UpdateWords.code(code)
    }
}

/// `GET /api/cluster/updates/runs/{id}`: a run with its steps and machines.
public struct UpdateRunView: Decodable, Sendable {
    public let run: UpdateRun
    public let installKind: String?
    /// Why the run waits now (tv_busy, outside_window, ...).
    public let waiting: String?
    public let tv: RunTV?
    public let waitingHome: [WaitingMachine]
    public let steps: [UpdateRunStep]
    public let targets: [UpdateTarget]

    enum CodingKeys: String, CodingKey {
        case detail, steps, targets
        case installKind = "install_kind"
    }

    private struct Detail: Decodable {
        let waiting: String?
        let tv: RunTV?
        let waitingHome: [WaitingMachine]
        enum CodingKeys: String, CodingKey {
            case waiting, tv
            case waitingHome = "waiting_home"
        }

        init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            waiting = c.lenient(.waiting)
            tv = c.lenient(.tv)
            waitingHome = c.lenientList(.waitingHome)
        }
    }

    public init(from decoder: Decoder) throws {
        run = try UpdateRun(from: decoder)
        let c = try decoder.container(keyedBy: CodingKeys.self)
        installKind = c.lenient(.installKind)
        let detail: Detail? = c.lenient(.detail)
        waiting = detail?.waiting.flatMap { $0.isEmpty ? nil : $0 }
        tv = detail?.tv
        waitingHome = detail?.waitingHome ?? []
        steps = c.lenientList(.steps)
        targets = c.lenientList(.targets)
    }
}

/// One step of a run on one host.
public struct UpdateRunStep: Decodable, Identifiable, Sendable {
    public let seq: Int
    public let host: String
    public let step: String
    public let state: String
    public let startedAt: Date?
    public let finishedAt: Date?
    public let detail: String?

    public var id: Int { seq }

    enum CodingKeys: String, CodingKey {
        case seq, host, step, state, detail
        case startedAt = "started_at"
        case finishedAt = "finished_at"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        seq = c.lenient(.seq) ?? 0
        host = c.lenient(.host) ?? ""
        step = c.lenient(.step) ?? ""
        state = c.lenient(.state) ?? ""
        startedAt = c.lenient(.startedAt)
        finishedAt = c.lenient(.finishedAt)
        // {detail: "..."} as the driver writes it; a plain string from an older one
        detail = c.lenientFields(.detail)["detail"] ?? c.lenient(.detail)
    }
}

/// One machine of a run: where it is in the run, and why it stopped.
public struct UpdateTarget: Decodable, Identifiable, Sendable {
    public let nodeID: String
    public let name: String
    public let platform: String?
    public let kind: String?
    /// pending, preparing, prepared, ordered, app_staged, applying, watching, committed,
    /// healthy, held, needs_person, failed, ... (a newer home's state shows raw).
    public let state: String
    public let fromVersion: String?
    public let toVersion: String?
    public let attempts: Int
    /// code, detail, held_by: the server's.
    public let error: [String: String]

    public var id: String { nodeID }

    enum CodingKeys: String, CodingKey {
        case name, platform, kind, state, attempts, error
        case nodeID = "node_id"
        case fromVersion = "from_version"
        case toVersion = "to_version"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        nodeID = c.lenient(.nodeID) ?? ""
        name = c.lenient(.name) ?? ""
        platform = c.lenient(.platform)
        kind = c.lenient(.kind)
        state = c.lenient(.state) ?? ""
        fromVersion = c.lenient(.fromVersion)
        toVersion = c.lenient(.toVersion)
        attempts = c.lenient(.attempts) ?? 0
        error = c.lenientFields(.error)
    }

    public var errorText: String? {
        guard let code = error["code"], !code.isEmpty else { return nil }
        return UpdateWords.code(code)
    }
}

/// A machine that waits for the home to commit the run's release before its order.
public struct WaitingMachine: Decodable, Identifiable, Sendable {
    public let nodeID: String
    public let name: String
    public let version: String?
    public var id: String { nodeID }

    enum CodingKeys: String, CodingKey {
        case name, version
        case nodeID = "node_id"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        nodeID = c.lenient(.nodeID) ?? ""
        name = c.lenient(.name) ?? ""
        version = c.lenient(.version)
    }
}

/// One machine of the Updates page: the home, then every joined machine.
public struct UpdateMachine: Decodable, Identifiable, Sendable {
    public let id: String
    public let name: String
    /// home or node.
    public let role: String
    public let version: String
    /// up_to_date, behind, updating, ahead_of_home, offline, worker_only, or a run's target
    /// state.
    public let state: String
    public let platform: String?
    /// The release the active run brings it to.
    public let target: String?
    public let lastUpdate: UpdateAttempt?
    public let homeDB: HomeDB?

    enum CodingKeys: String, CodingKey {
        case id, name, role, version, state, platform, target
        case lastUpdate = "last_update"
        case homeDB = "home_db"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = c.lenient(.id) ?? ""
        name = c.lenient(.name) ?? ""
        role = c.lenient(.role) ?? "node"
        version = c.lenient(.version) ?? ""
        state = c.lenient(.state) ?? ""
        platform = c.lenient(.platform)
        target = c.lenient(.target)
        lastUpdate = c.lenient(.lastUpdate)
        homeDB = c.lenient(.homeDB)
    }
}

/// One try at bringing a machine to a release (the machine's last completed update).
public struct UpdateAttempt: Decodable, Sendable {
    public let at: Date?
    public let endedAt: Date?
    /// app, server, run.
    public let source: String?
    public let to: String
    public let outcome: String?

    enum CodingKeys: String, CodingKey {
        case at, source, to, outcome
        case endedAt = "ended_at"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        at = c.lenient(.at)
        endedAt = c.lenient(.endedAt)
        source = c.lenient(.source)
        to = c.lenient(.to) ?? ""
        outcome = c.lenient(.outcome)
    }

    /// How it came (the web's updates.last.how).
    public var how: String {
        switch source {
        case "run": kit("an update run")
        case "app": kit("by the desktop app")
        default: kit("installed by hand")
        }
    }
}

/// A joined machine's reach to the home's database, as its machine agent reports it.
public struct HomeDB: Decodable, Sendable {
    public let ok: Bool
    /// auth_failed, no_hba, refused, unreachable, no_database, tls, cert_mismatch, other.
    public let code: String?
    public let detail: String?
    public let at: Date?
    public let healing: Bool

    enum CodingKeys: String, CodingKey {
        case ok, code, detail, at, healing
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        ok = c.lenient(.ok) ?? false
        code = c.lenient(.code)
        detail = c.lenient(.detail)
        at = c.lenient(.at)
        healing = c.lenient(.healing) ?? false
    }

    /// The web's machines.homeDb words.
    public var sentence: String {
        if ok { return kit("Reaches the home's database.") }
        if healing { return kit("The home gave it new access; it is following it.") }
        return kit("This machine can't reach the home's database: \(words(StatusWords.homeDBCodes, "machines.homeDb.code.", code ?? "other"))")
    }
}

/// A run's TV held at its commit: the release installed, the live TV stayed on `heldOn`.
public struct RunTV: Decodable, Sendable {
    public let number: Int?
    public let heldOn: String?
    public let failed: [String]
    /// held, retrying, live, dropped, replaced, failed, gone, pending.
    public let state: String
    public let checks: [TVCheck]

    enum CodingKeys: String, CodingKey {
        case number, failed, state, checks
        case heldOn = "held_on"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        number = c.lenient(.number)
        heldOn = c.lenient(.heldOn)
        failed = c.lenientList(.failed)
        let s: String? = c.lenient(.state)
        state = (s ?? "").isEmpty ? "held" : s!
        checks = c.lenientList(.checks)
    }

    public var held: Bool { state == "held" }

    /// The failed checks: the report's (with their values), else their names.
    public var failedChecks: [TVCheck] {
        checks.isEmpty ? failed.map { TVCheck(check: $0) } : checks
    }

    /// The TV's state in a sentence (the web's updates.tv.*), `release` the run's.
    public func title(release: String) -> String {
        let on = heldOnWords
        switch state {
        case "held": return kit("Installed. TV held on \(on): its check failed")
        case "retrying": return kit("Installed. The TV change is checked again; the TV runs \(on) until it passes")
        case "live": return kit("Installed. The TV change passed on a retry: the TV runs \(release)")
        case "dropped": return kit("Installed. The TV change was dropped: the TV stayed on \(on)")
        case "replaced": return kit("Installed. A later release's TV change replaced this one")
        case "failed": return kit("Installed. The TV change failed again after its retry: see TV changes")
        case "gone": return kit("Installed. The TV was held on \(on); its change is no longer there")
        case "pending":
            return kit("Installed. The TV change goes on by itself (it waits for an approval or a quiet window); the TV runs \(on) until it is on the air")
        default: return kit("Installed. TV on \(on): \(words([:], state))")
        }
    }

    /// The release the TV stays on: its version, else "the previous version".
    var heldOnWords: String {
        (heldOn ?? "").isEmpty ? kit("the previous version") : heldOn!
    }

    /// What a held TV means, for the screen below its title.
    public var heldBody: String? {
        guard held else { return nil }
        return kit("The new server, web app and pipeline are installed. The TV keeps running the streaming software of \(heldOnWords) until its change passes its check.")
    }
}

/// A failed check of a TV change's report (its channel and value). `value` is the home's
/// English; `code` names it and `params` fill it (the web's `tvchanges.value.<code>`), so
/// the app says it in its own language.
public struct TVCheck: Decodable, Sendable, Hashable {
    public let check: String
    public let channelID: String?
    public let result: String?
    public let value: String?
    public let code: String?
    public let params: [String: String]

    enum CodingKeys: String, CodingKey {
        case check, result, value, code, params
        case channelID = "channel_id"
    }

    init(check: String, value: String? = nil, code: String? = nil, params: [String: String] = [:]) {
        self.check = check
        channelID = nil
        result = nil
        self.value = value
        self.code = code
        self.params = params
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        check = c.lenient(.check) ?? ""
        channelID = c.lenient(.channelID)
        result = c.lenient(.result)
        value = c.lenient(.value)
        code = c.lenient(.code)
        params = c.lenientFields(.params)
    }

    /// The check's name ("Staging process"), else as sent.
    public func name(locale: Locale = AppLanguage.locale) -> String {
        guard StatusWords.checkNames[check] != nil else { return check }
        return words(StatusWords.checkNames, "tvchanges.check.", check, locale)
    }

    /// The value in words: its code's with its params, else the home's English.
    public func valueText(locale: Locale = AppLanguage.locale) -> String? {
        if let code, StatusWords.checkValues[code] != nil {
            return filled(StatusWords.checkValues, "tvchanges.value.", code, params, locale)
        }
        guard let value, !value.isEmpty else { return nil }
        return value
    }

    /// "check Staging process failed: The staging slot runs another release".
    public func sentence(locale: Locale = AppLanguage.locale) -> String {
        let name = name(locale: locale)
        guard let value = valueText(locale: locale) else { return kit("check \(name) failed", locale) }
        return kit("check \(name) failed: \(value)", locale)
    }

    public var sentence: String { sentence() }
}

/// The run whose TV is held now.
public struct TVHeldNow: Decodable, Sendable {
    public let runID: String
    public let version: String
    public let tv: RunTV

    enum CodingKeys: String, CodingKey {
        case version
        case runID = "run_id"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        runID = c.lenient(.runID) ?? ""
        version = c.lenient(.version) ?? ""
        tv = try RunTV(from: decoder)
    }

    /// "The TV stays on 0.3.39: check slot.health failed: the staging slot runs another
    /// release" (the first failed check; the screen lists them all).
    public var summary: String {
        let on = tv.heldOnWords
        guard tv.held else { return tv.title(release: version) }
        guard let first = tv.failedChecks.first else { return kit("The TV stays on \(on): its check failed") }
        return kit("The TV stays on \(on): \(first.sentence)")
    }
}

/// A release a gate held for several windows in a row.
public struct UpdatesStuck: Decodable, Sendable {
    public let version: String
    public let code: String
    public let windows: Int
    public let since: Date?

    enum CodingKeys: String, CodingKey {
        case version, code, windows, since
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        version = c.lenient(.version) ?? ""
        code = c.lenient(.code) ?? ""
        windows = c.lenient(.windows) ?? 0
        since = c.lenient(.since)
    }

    /// The web's updates.stuck.title.
    public var sentence: String {
        kit("\(version) has not installed in \(windows) windows: \(UpdateWords.reason(code))")
    }
}

// MARK: versions

/// The server's order of release versions (releaseverify.CompareVersions, the web's
/// `compareVersions`): a prerelease below its release, a `git describe` build after the tag
/// it was built from (by its commits, then dirty after clean).
public enum ReleaseVersion {
    nonisolated(unsafe) private static let release = /^v?(\d+)\.(\d+)\.(\d+)(?:-[0-9A-Za-z.-]+)?$/
    nonisolated(unsafe) private static let describe = /^(\d+\.\d+\.\d+(?:-[0-9a-z.]+)?)-(\d+)-g[0-9a-f]{4,40}$/

    /// A release version ("1.5.0", "v1.5.0-rc.1").
    public static func valid(_ s: String) -> Bool {
        s.trimmingCharacters(in: .whitespaces).wholeMatch(of: release) != nil
    }

    /// -1, 0 or 1; 0 when either is not a version.
    public static func compare(_ a: String, _ b: String) -> Int {
        guard valid(a), valid(b) else { return 0 }
        let da = split(a), db = split(b)
        let c = compareReleases(da.tag, db.tag)
        if c != 0 { return c }
        if da.commits != db.commits { return da.commits < db.commits ? -1 : 1 }
        return da.dirty == db.dirty ? 0 : (da.dirty ? 1 : -1)
    }

    private static func split(_ v: String) -> (tag: String, commits: Int, dirty: Bool) {
        var s = v.trimmingCharacters(in: .whitespaces).lowercased()
        if s.hasPrefix("v") { s.removeFirst() }
        if let plus = s.firstIndex(of: "+") { s = String(s[..<plus]) }
        var dirty = false
        if s.hasSuffix("-dirty") {
            dirty = true
            s.removeLast("-dirty".count)
        }
        if let m = s.wholeMatch(of: describe) {
            return (String(m.1), Int(m.2) ?? 0, dirty)
        }
        return (s, 0, dirty)
    }

    private static func compareReleases(_ a: String, _ b: String) -> Int {
        let ap = a.split(separator: "-", maxSplits: 1, omittingEmptySubsequences: false)
        let bp = b.split(separator: "-", maxSplits: 1, omittingEmptySubsequences: false)
        let an = ap[0].split(separator: ".").map { Int($0) ?? 0 }
        let bn = bp[0].split(separator: ".").map { Int($0) ?? 0 }
        for i in 0..<max(an.count, bn.count) {
            let x = i < an.count ? an[i] : 0, y = i < bn.count ? bn[i] : 0
            if x != y { return x < y ? -1 : 1 }
        }
        let apre = ap.count > 1 ? String(ap[1]) : ""
        let bpre = bp.count > 1 ? String(bp[1]) : ""
        if apre.isEmpty || bpre.isEmpty { return apre == bpre ? 0 : (apre.isEmpty ? 1 : -1) }
        let xs = apre.split(separator: ".", omittingEmptySubsequences: false)
        let ys = bpre.split(separator: ".", omittingEmptySubsequences: false)
        for i in 0..<min(xs.count, ys.count) {
            let x = String(xs[i]), y = String(ys[i])
            let xn = Int(x), yn = Int(y)
            let xNum = !x.isEmpty && x.allSatisfy(\.isASCII) && x.allSatisfy(\.isNumber)
            let yNum = !y.isEmpty && y.allSatisfy(\.isASCII) && y.allSatisfy(\.isNumber)
            if xNum, yNum, let xn, let yn, xn != yn { return xn < yn ? -1 : 1 }
            if xNum != yNum { return xNum ? -1 : 1 }
            if !xNum, x != y { return x < y ? -1 : 1 }
        }
        return xs.count == ys.count ? 0 : (xs.count < ys.count ? -1 : 1)
    }
}
