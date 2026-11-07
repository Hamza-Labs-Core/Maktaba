import Foundation

/// Whether this person, on this device, sends crash reports to a home (Epic 53 story 53.14
/// §3): off until they say yes. The question is asked once per home after signing in, with
/// two equal buttons; the answer is kept in the defaults and changed in Settings → Privacy.
/// An admin cannot turn it on for someone else's device: it lives here, not at the home.
public struct CrashConsent: Sendable {
    nonisolated(unsafe) let defaults: UserDefaults

    public init(defaults: UserDefaults = .standard) { self.defaults = defaults }

    /// The key a home's answer is kept under: its instance id, else its address.
    public static func key(_ home: Home) -> String { "crashReports." + (home.homeID ?? home.url.absoluteString) }

    /// nil: not asked yet (a fresh install, or one updated from a release before this).
    public func answer(_ home: Home) -> Bool? {
        defaults.object(forKey: Self.key(home)) == nil ? nil : defaults.bool(forKey: Self.key(home))
    }

    public func isOn(_ home: Home) -> Bool { answer(home) == true }

    public func set(_ on: Bool, for home: Home) { defaults.set(on, forKey: Self.key(home)) }

    /// Whether the question may show at this launch: a UI test sees it only when it asks
    /// (`-maktaba-crash-question`), so the other tests' first screens stay as they were.
    public static func questionAllowed(_ args: [String] = ProcessInfo.processInfo.arguments) -> Bool {
        if args.contains("-maktaba-crash-question") { return true }
        let uiTest = ["-maktaba-permissions-fake", "-uitest-reset", "-maktaba-session-reset", "-maktaba-ui-test"]
        return !args.contains(where: uiTest.contains)
    }
}

/// The crashes kept on this device and which were sent (Epic 53 story 53.14): each kept file
/// (`CrashReports.directory`) goes to the home once crash reports are on, on the next launch
/// signed in. A 429 or no answer keeps it for later: at most 3 retries, over at most 7 days.
/// When crash reports are off nothing is sent and the crash stays on the device (the last 5),
/// where a problem report can attach it.
public final class CrashOutbox: @unchecked Sendable {
    public static let maxRetries = 3
    public static let maxAge: TimeInterval = 7 * 86400

    struct Entry: Codable, Equatable {
        /// events of the file already sent (a payload can hold several crashes)
        var sent = 0
        var attempts = 0
        var next: Date?
        /// sent in full, or given up on
        var done = false
        var gaveUp = false
    }

    struct State: Codable {
        var entries: [String: Entry] = [:]
        /// the newest crash file the person was told about
        var announced = ""
    }

    /// What the app says about the last launch's crash, once.
    public enum Notice: Equatable, Sendable {
        /// "A report was sent to {home}."
        case sent
        /// "The crash is kept on this device; you can attach it to a problem report."
        case kept
    }

    public enum SendResult: Sendable {
        case sent
        /// later: after this long (a 429's Retry-After), or the outbox's own wait
        case later(TimeInterval?)
        /// the home says crash reports are off for this device (403 crash_reports_off)
        case refused
    }

    public let directory: URL
    private let lock = NSLock()

    public init(directory: URL) { self.directory = directory }

    public static let shared: CrashOutbox? = CrashReports.directory.map(CrashOutbox.init)

    private var stateURL: URL { directory.appendingPathComponent("outbox.json") }

    func load() -> State {
        guard let d = try? Data(contentsOf: stateURL) else { return State() }
        let dec = JSONDecoder()
        dec.dateDecodingStrategy = .secondsSince1970
        return (try? dec.decode(State.self, from: d)) ?? State()
    }

    func save(_ s: State) {
        let enc = JSONEncoder()
        enc.dateEncodingStrategy = .secondsSince1970
        try? FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        try? enc.encode(s).write(to: stateURL, options: .atomic)
    }

    /// The kept crash files, oldest first.
    public func files() -> [URL] {
        let all = (try? FileManager.default.contentsOfDirectory(at: directory, includingPropertiesForKeys: nil)) ?? []
        return all.filter { $0.lastPathComponent.hasPrefix("crash-") && $0.pathExtension == "json" }
            .sorted { $0.lastPathComponent < $1.lastPathComponent }
    }

    /// When a kept file was written (its name: crash-<unix time>-<id>.json).
    static func time(of url: URL) -> Date? {
        let parts = url.deletingPathExtension().lastPathComponent.split(separator: "-")
        return parts.count > 1 ? TimeInterval(parts[1]).map(Date.init(timeIntervalSince1970:)) : nil
    }

    /// The kept crashes' events (for a problem report: the person ticks them in, seeing them).
    public func events(limit: Int = 3) -> [[String: Any]] {
        Array(files().suffix(limit).flatMap { url in (try? Data(contentsOf: url)).map { Envelope.events(fromFile: $0) } ?? [] }
            .suffix(limit)).map { Envelope.scrubbed($0) as? [String: Any] ?? [:] }
    }

    /// Sends what is due through `send` (one envelope at a time). Nothing at all when `on` is
    /// false. Stops at a refusal: the home has this device as off.
    @discardableResult
    public func flush(on: Bool, header: Envelope.Header, log: @autoclosure () -> [String], now: Date = Date(),
                      send: (Data) async -> SendResult) async -> Int {
        guard on else { return 0 }
        var state = lock.withLock { load() }
        var sent = 0
        let lines = log()
        outer: for url in files() {
            let name = url.lastPathComponent
            var e = state.entries[name] ?? Entry()
            if e.done { continue }
            if let next = e.next, now < next { continue }
            if e.attempts > 0, let t = Self.time(of: url), now.timeIntervalSince(t) > Self.maxAge {
                e.done = true
                e.gaveUp = true
                state.entries[name] = e
                continue
            }
            let events = (try? Data(contentsOf: url)).map { Envelope.events(fromFile: $0) } ?? []
            while e.sent < events.count {
                switch await send(Envelope.build(header, event: events[e.sent], log: lines)) {
                case .sent:
                    e.sent += 1
                    sent += 1
                case let .later(wait):
                    e.attempts += 1
                    if e.attempts > Self.maxRetries {
                        e.done = true
                        e.gaveUp = true
                    } else {
                        // a 429's wait, else an hour, then a day: three tries within the week
                        e.next = now.addingTimeInterval(wait ?? [3600, 86400, 2 * 86400][min(e.attempts - 1, 2)])
                    }
                    state.entries[name] = e
                    continue outer
                case .refused:
                    state.entries[name] = e
                    break outer
                }
            }
            e.done = true
            e.next = nil
            state.entries[name] = e
        }
        // files no longer kept (the oldest go at 5) leave the state too
        let names = Set(files().map(\.lastPathComponent))
        state.entries = state.entries.filter { names.contains($0.key) }
        let final = state
        lock.withLock { save(final) }
        return sent
    }

    /// What to say about crashes kept since the person was last told, once: sent when each
    /// went to the home, else kept.
    public func notice() -> Notice? {
        lock.withLock {
            var state = load()
            let newer = files().filter { $0.lastPathComponent > state.announced }
            guard let last = newer.last else { return nil }
            state.announced = last.lastPathComponent
            save(state)
            let allSent = newer.allSatisfy { f in
                guard let e = state.entries[f.lastPathComponent] else { return false }
                return e.done && !e.gaveUp
            }
            return allSent ? .sent : .kept
        }
    }

    /// Whether a file was sent in full (tests, the Diagnostics page).
    func wasSent(_ name: String) -> Bool { lock.withLock { load().entries[name].map { $0.done && !$0.gaveUp } ?? false } }
}

extension CrashOutbox {
    /// Sends what is due to `client`'s home, when the person turned crash reports on for it.
    public func flush(to client: APIClient, home: Home, consent: CrashConsent = CrashConsent()) async -> Int {
        await flush(on: consent.isOn(home), header: .current(), log: LogRing.shared?.lines() ?? []) { data in
            do {
                try await client.sendCrashEnvelope(data)
                return .sent
            } catch let e as APIError {
                if case let .status(code, _, retryAfter) = e {
                    if code == 403 { return .refused }
                    if code == 429 { return .later(retryAfter.map(TimeInterval.init)) }
                    // 413 or 400: this envelope will never pass; counted as a try
                    return .later(nil)
                }
                return .later(nil)
            } catch {
                return .later(nil)
            }
        }
    }
}
