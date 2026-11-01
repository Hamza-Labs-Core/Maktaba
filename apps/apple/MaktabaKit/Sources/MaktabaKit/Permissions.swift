#if os(iOS)
import AVFoundation
#endif
import dnssd
import Foundation
import Network
import Observation

/// What an app may need the person to allow, and where each one stands. The apps list only
/// what they really use (see web/src/help/content/en/apple-apps.md#permissions):
///
/// - **Local Network** (iPhone, iPad, Mac): a home is usually on the same Wi-Fi or Ethernet
///   (`http://192.168.1.20:8088`, `mac.local`), and iOS 14+ and macOS 15+ block traffic to the
///   local network until the person allows it. tvOS has no such privilege (Apple's TN3179),
///   so the TV app needs nothing.
/// - **Camera** (iPhone, iPad; optional): to scan a home's QR code in the home picker, or a
///   device's sign-in QR code to approve it. Everything it scans can be typed instead; the
///   Mac takes a pasted link.
public enum PermissionKind: String, CaseIterable, Identifiable, Sendable {
    case localNetwork
    case camera

    public var id: String { rawValue }

    /// Without it the app cannot do its main job (reach a home on this network).
    public var isRequired: Bool {
        switch self {
        case .localNetwork: true
        case .camera: false
        }
    }

    /// What this platform asks for: Local Network on iOS, iPadOS and macOS, the camera on
    /// iOS and iPadOS; nothing on tvOS.
    public static var forThisPlatform: [PermissionKind] {
        #if os(tvOS) || os(watchOS)
        []
        #elseif os(iOS)
        [.localNetwork, .camera]
        #else
        [.localNetwork]
        #endif
    }
}

/// The camera's permission (AVFoundation on iOS; a fake in tests).
public protocol CameraAccess: Sendable {
    func state() -> PermissionState
    /// Asks (the system's question the first time); true when allowed.
    func request() async -> Bool
}

/// The device's camera permission.
public struct SystemCamera: CameraAccess {
    public init() {}

    public func state() -> PermissionState {
        #if os(iOS)
        switch AVCaptureDevice.authorizationStatus(for: .video) {
        case .authorized: .granted
        case .denied, .restricted: .denied
        case .notDetermined: .notAsked
        @unknown default: .unknown
        }
        #else
        .unknown
        #endif
    }

    public func request() async -> Bool {
        #if os(iOS)
        await AVCaptureDevice.requestAccess(for: .video)
        #else
        false
        #endif
    }
}

/// A camera whose answers are given (tests, and the UI tests' scripted runs).
public struct FakeCamera: CameraAccess {
    let current: PermissionState
    let answer: Bool

    public init(_ current: PermissionState, answers answer: Bool = true) {
        self.current = current
        self.answer = answer
    }

    public func state() -> PermissionState { current }
    public func request() async -> Bool { answer }
}

/// Where a permission stands, as the walkthrough shows it.
public enum PermissionState: String, Sendable, Equatable {
    case granted
    case denied
    /// The app has not asked yet (the system has not shown its question).
    case notAsked
    /// The app could not tell (no network, the check timed out, another error).
    case unknown
}

// MARK: - Local Network detection

/// What a Bonjour browse for `_maktaba._tcp` reports. There is no API that says whether an
/// app may use the local network (TN3179); a browse is the usual way to find out: refused,
/// it waits with `kDNSServiceErr_PolicyDenied`; allowed, it becomes ready (and lists homes).
/// The first browse is also what makes iOS ask the question.
public enum BrowseSignal: Sendable, Equatable {
    case ready
    /// How many `_maktaba._tcp` services the browse sees now.
    case results(Int)
    /// Waiting; the DNS-SD error when it is one (`-65570` is "refused by policy").
    case waiting(dnsError: Int32?)
    case failed(dnsError: Int32?)
    /// The browse ended without a failure.
    case ended
}

/// Turns browse signals into a verdict. Pure, so the rules are tested without a network:
/// - any home listed → allowed;
/// - waiting with `PolicyDenied` → not allowed;
/// - ready and no refusal for `grace` → allowed (an empty network lists nothing);
/// - another error, or nothing by `timeout` → can't tell.
public struct LocalNetworkVerdict: Sendable {
    public static let policyDenied = Int32(kDNSServiceErr_PolicyDenied)

    public let grace: Duration
    public let timeout: Duration
    private var readyAt: Duration?
    public private(set) var state: PermissionState?

    public init(grace: Duration = .seconds(2), timeout: Duration = .seconds(6)) {
        self.grace = grace
        self.timeout = timeout
    }

    /// The verdict a signal at `elapsed` gives (nil: not yet known). A later signal can
    /// change it: while iOS shows its question a browse is refused, and it is retried
    /// once the person allows it.
    @discardableResult
    public mutating func feed(_ signal: BrowseSignal, at elapsed: Duration) -> PermissionState? {
        switch signal {
        case .ready:
            if readyAt == nil { readyAt = elapsed }
        case let .results(n):
            if n > 0 { state = .granted }
        case let .waiting(code):
            readyAt = nil
            state = code == Self.policyDenied ? .denied : (state == .granted ? .granted : .unknown)
        case let .failed(code):
            readyAt = nil
            state = code == Self.policyDenied ? .denied : .unknown
        case .ended:
            if state == nil { state = readyAt != nil ? .granted : .unknown }
        }
        applyGrace(at: elapsed)
        return state
    }

    /// The verdict as time passes with no new signal. Only silence times out: a signal
    /// that arrives late (a busy device) is still heard, along with any queued behind it.
    @discardableResult
    public mutating func tick(at elapsed: Duration) -> PermissionState? {
        applyGrace(at: elapsed)
        if state == nil, elapsed >= timeout { state = .unknown }
        return state
    }

    private mutating func applyGrace(at elapsed: Duration) {
        if state == nil || state == .unknown, let readyAt, elapsed - readyAt >= grace {
            state = .granted
        }
    }

    /// A verdict that will not change by waiting longer (a home listed, a refusal, an error).
    public var isFinal: Bool { state != nil }
}

/// Something that browses for `_maktaba._tcp` and reports what happens. The stream ends
/// (and the browse stops) when the caller stops reading it.
public protocol LocalNetworkProbe: Sendable {
    func signals() -> AsyncStream<BrowseSignal>
}

/// The real probe: an `NWBrowser` for `_maktaba._tcp` (listed under `NSBonjourServices`).
public struct BonjourProbe: LocalNetworkProbe {
    public init() {}

    public func signals() -> AsyncStream<BrowseSignal> {
        AsyncStream { cont in
            let browser = NWBrowser(for: .bonjour(type: "_maktaba._tcp", domain: nil), using: .tcp)
            browser.stateUpdateHandler = { state in
                switch state {
                case .ready: cont.yield(.ready)
                case let .waiting(error): cont.yield(.waiting(dnsError: Self.dnsCode(error)))
                case let .failed(error):
                    cont.yield(.failed(dnsError: Self.dnsCode(error)))
                    cont.finish()
                case .cancelled: cont.finish()
                default: break
                }
            }
            browser.browseResultsChangedHandler = { results, _ in cont.yield(.results(results.count)) }
            cont.onTermination = { _ in browser.cancel() }
            browser.start(queue: DispatchQueue(label: "maktaba.permissions.bonjour"))
        }
    }

    static func dnsCode(_ error: NWError) -> Int32? {
        if case let .dns(code) = error { return Int32(code) }
        return nil
    }
}

/// A probe that replays signals (unit and UI tests: the simulators have no local network
/// privacy, so a refusal can only be shown this way).
public struct ScriptedProbe: LocalNetworkProbe {
    public let script: [BrowseSignal]
    /// Keep the stream open after the script, as a real browse does.
    public let staysOpen: Bool

    public init(_ script: [BrowseSignal], staysOpen: Bool = true) {
        self.script = script
        self.staysOpen = staysOpen
    }

    /// The scripts the UI tests pick with `-maktaba-permissions-fake <name>`.
    public init?(named name: String) {
        switch name {
        case "granted": self.init([.ready, .results(1)])
        case "denied": self.init([.waiting(dnsError: LocalNetworkVerdict.policyDenied)])
        case "unknown": self.init([.failed(dnsError: nil)], staysOpen: false)
        default: return nil
        }
    }

    public func signals() -> AsyncStream<BrowseSignal> {
        AsyncStream { cont in
            for s in script { cont.yield(s) }
            if !staysOpen { cont.finish() }
        }
    }
}

// MARK: - The walkthrough's model

/// The permissions an app needs, their states, and whether the walkthrough shows: on the
/// first run (until it is closed once), and again whenever a required permission is refused.
/// Re-checked each time the app comes to the foreground.
@MainActor @Observable
public final class PermissionsModel {
    public struct Item: Identifiable, Equatable, Sendable {
        public let kind: PermissionKind
        public var state: PermissionState
        public var id: PermissionKind { kind }
        public var isRequired: Bool { kind.isRequired }
    }

    public struct Timing: Sendable {
        /// A check on launch or on coming back to the foreground.
        public var check: LocalNetworkVerdict
        /// After the person asks: long enough to answer the system's question.
        public var request: Duration
        public static let standard = Timing(check: LocalNetworkVerdict(), request: .seconds(30))

        public init(check: LocalNetworkVerdict, request: Duration) {
            self.check = check
            self.request = request
        }
    }

    static let seenKey = "permissions.walkthroughSeen"
    static let askedKey = "permissions.localNetwork.asked"

    public private(set) var items: [Item]
    public var isPresented = false
    /// A check or a request is running for this permission.
    public private(set) var busy: Set<PermissionKind> = []

    private let defaults: UserDefaults
    private let probe: any LocalNetworkProbe
    private let camera: any CameraAccess
    private let timing: Timing
    /// Closed this run while something required was refused: not shown again until the
    /// refusal is new (it was allowed, then refused) or the app starts again.
    private var dismissedWhileDenied = false

    public init(
        kinds: [PermissionKind] = PermissionKind.forThisPlatform,
        defaults: UserDefaults = .standard,
        probe: any LocalNetworkProbe = BonjourProbe(),
        camera: any CameraAccess = SystemCamera(),
        timing: Timing = .standard
    ) {
        self.items = kinds.map { Item(kind: $0, state: .notAsked) }
        self.defaults = defaults
        self.probe = probe
        self.camera = camera
        self.timing = timing
    }

    /// The model an app uses. Debug builds take `-maktaba-permissions-reset` (forget the
    /// "seen" and "asked" flags) and `-maktaba-permissions-fake granted|denied|unknown`
    /// (a scripted browse) from the launch arguments, for the UI tests.
    public static func forThisApp(arguments: [String] = CommandLine.arguments) -> PermissionsModel {
        #if DEBUG
        if arguments.contains("-maktaba-permissions-reset") {
            UserDefaults.standard.removeObject(forKey: seenKey)
            UserDefaults.standard.removeObject(forKey: askedKey)
        }
        if let i = arguments.firstIndex(of: "-maktaba-permissions-fake"), i + 1 < arguments.count,
           let fake = ScriptedProbe(named: arguments[i + 1]) {
            let quick = Timing(check: LocalNetworkVerdict(grace: .milliseconds(300), timeout: .seconds(2)), request: .seconds(2))
            return PermissionsModel(probe: fake, timing: quick)
        }
        #endif
        return PermissionsModel()
    }

    /// The walkthrough has been closed once.
    public var seen: Bool { defaults.bool(forKey: Self.seenKey) }
    /// The app has browsed once (so the system has asked, or will).
    var asked: Bool { defaults.bool(forKey: Self.askedKey) }

    public func state(of kind: PermissionKind) -> PermissionState {
        items.first { $0.kind == kind }?.state ?? .unknown
    }

    public var requiredMissing: Bool {
        items.contains { $0.isRequired && $0.state == .denied }
    }

    /// Checks every permission again (on launch, and when the app comes to the foreground).
    /// Before the walkthrough has been seen nothing is browsed, so the system's question
    /// comes only after the walkthrough has said what it is for.
    public func refresh() async {
        for kind in items.map(\.kind) where !busy.contains(kind) {
            switch kind {
            case .localNetwork:
                if !asked && !seen {
                    set(.localNetwork, .notAsked)
                } else {
                    await browse(timing.check, until: nil)
                }
            case .camera:
                set(.camera, camera.state())
            }
        }
        updatePresentation()
    }

    /// Asks for a permission: for Local Network, a browse (iOS shows its question the first
    /// time), followed until it is allowed or `timing.request` passes.
    public func request(_ kind: PermissionKind) async {
        guard !busy.contains(kind) else { return }
        switch kind {
        case .localNetwork:
            // the system asks only once: after that a check gives the answer at once
            let first = !asked
            defaults.set(true, forKey: Self.askedKey)
            if first {
                await browse(LocalNetworkVerdict(grace: timing.check.grace, timeout: timing.request), until: timing.request)
            } else {
                await browse(timing.check, until: nil)
            }
        case .camera:
            busy.insert(.camera)
            _ = await camera.request()
            busy.remove(.camera)
            set(.camera, camera.state())
        }
        updatePresentation()
    }

    /// The person closed the walkthrough.
    public func dismiss() {
        defaults.set(true, forKey: Self.seenKey)
        if requiredMissing { dismissedWhileDenied = true }
        isPresented = false
    }

    /// Browses with `verdict`'s rules. With `until` nil it stops at the first verdict;
    /// otherwise it follows changes until a home is listed or `until` passes, showing each.
    private func browse(_ verdict: LocalNetworkVerdict, until: Duration?) async {
        busy.insert(.localNetwork)
        defer { busy.remove(.localNetwork) }
        var v = verdict
        // the clock starts when the probe is being read, not when the browse was asked for:
        // a busy test run (or device) can hold the reader back past the whole timeout
        var start: ContinuousClock.Instant?
        let (events, cont) = AsyncStream<BrowseEvent>.makeStream()
        let stream = probe.signals()
        let feeder = Task {
            cont.yield(.reading)
            for await s in stream { cont.yield(.signal(s)) }
            cont.yield(.signal(.ended))
        }
        let ticker = Task {
            while !Task.isCancelled {
                try? await Task.sleep(for: .milliseconds(100))
                cont.yield(.tick)
            }
        }
        defer {
            feeder.cancel()
            ticker.cancel()
            cont.finish()
        }
        for await e in events {
            let signal: BrowseSignal?
            switch e {
            case .reading:
                start = .now
                continue
            case .tick:
                signal = nil
            case let .signal(s):
                signal = s
            }
            guard let start else { continue }
            let now = ContinuousClock.now - start
            let state = signal.map { v.feed($0, at: now) } ?? v.tick(at: now)
            if let state { set(.localNetwork, state) }
            // time runs out only on a tick, so signals already queued are heard first
            let timedOut = signal == nil && now >= (until ?? v.timeout)
            if let until {
                // allowed is the answer; a refusal may be the system's while its question is up
                if state == .granted || timedOut || signal == .ended { break }
            } else if v.isFinal || timedOut || signal == .ended {
                break
            }
        }
        if v.state == nil { set(.localNetwork, .unknown) }
    }

    private enum BrowseEvent {
        case reading
        case signal(BrowseSignal)
        case tick
    }

    private func set(_ kind: PermissionKind, _ state: PermissionState) {
        guard let i = items.firstIndex(where: { $0.kind == kind }) else { return }
        let was = items[i].state
        items[i].state = state
        // a refusal that is new shows the walkthrough again even if it was closed this run
        if state == .denied && was != .denied && kind.isRequired {
            dismissedWhileDenied = false
        }
    }

    private func updatePresentation() {
        guard !items.isEmpty else {
            isPresented = false
            return
        }
        if !seen || (requiredMissing && !dismissedWhileDenied) {
            isPresented = true
        }
    }
}
