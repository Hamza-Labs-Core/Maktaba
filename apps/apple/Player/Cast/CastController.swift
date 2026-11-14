import Foundation
import MaktabaKit
import Observation

/// Casting for the signed-in Player (story 58.06): the Cast button's state, Google's local
/// network screen before the first cast, the device list, and the cast itself:
/// 1. the home's addresses and level are read again (`GET /api/discovery/home`): no HTTPS
///    address the TV can reach, no Cast button for this home;
/// 2. on a device, a cast grant is minted with the person's own token
///    (`POST /api/cast/grants`) and the receiver is loaded with Maktaba's custom data
///    (`CastCustomData`); a home without the grant route gets the plain signed URL, which
///    Maktaba's receiver plays as it came, and the person is told (`cast.oldHome`); a refusal
///    is shown and nothing is cast;
/// 3. while the cast plays the phone posts no progress (the receiver does, with the grant),
///    follows what the TV says it plays, and mints the next grant 30 minutes before the one in
///    use ends.
@MainActor @Observable
final class CastController {
    /// Where the home's Cast availability stands.
    enum Reach: Equatable { case unknown, castable, unreachable }

    /// What went wrong, as one sentence (`CastWords`).
    enum Problem: Equatable {
        case refused(CastRefusal)
        case receiver(CastReceiverError)
        case connect
        case failed
    }

    private let context: (any CastContexting)?
    private let defaults: UserDefaults
    @ObservationIgnored private var client: APIClient?
    @ObservationIgnored private var discovery: HomeDiscovery?
    @ObservationIgnored private var refreshTask: Task<Void, Never>?
    /// The cast asked for while the device connects.
    @ObservationIgnored private var pending: (video: Video, at: Double, item: CastItem)?
    /// Told when a cast takes the video from the phone (the position the phone stopped at is
    /// the receiver's start) and when it gives it back (where the TV was).
    @ObservationIgnored var handOver: ((_ videoID: String) -> Double)?
    @ObservationIgnored var giveBack: ((_ videoID: String, _ at: Double?) -> Void)?

    private(set) var reach = Reach.unknown
    private(set) var devices: [CastDevice] = []
    private(set) var device: CastDevice?
    private(set) var status: CastReceiverStatus?
    /// The video the cast was started with, and its title (the mini controller's line).
    private(set) var castVideoID: String?
    private(set) var title: String = ""
    /// The cast went to an older home's plain load (`cast.oldHome` shows).
    private(set) var oldHome = false
    private(set) var problem: Problem?
    /// Connecting or minting: the device row shows a spinner.
    private(set) var busy = false

    var showsInterstitial = false
    var showsPicker = false
    /// Watch screens open: with none, the sections show the mini controller while it casts.
    var watchShowing = 0

    /// Whether the sections show the mini controller.
    var showsMini: Bool { isCasting && watchShowing <= 0 }

    static let interstitialKey = "cast.localNetwork.seen"

    init(context: (any CastContexting)?, defaults: UserDefaults = .standard) {
        self.context = context
        self.defaults = defaults
        context?.events = { [weak self] in self?.heard($0) }
        if let on = context?.connectedDevice { device = on }
    }

    /// The app's one controller, made at its first use: a `@State` initial value is evaluated
    /// at every init of its view, and the SDK's listeners must be added once.
    static let shared = make()

    /// The Player's controller: the fake one in a UI test's Debug build, the Google Cast SDK's
    /// on an iPhone or iPad whose build names a receiver, else none (no Cast button anywhere).
    static func make(arguments: [String] = CommandLine.arguments, config: CastConfig = CastConfig()) -> CastController {
        #if DEBUG
        if let fake = ShellLaunch.value(of: "-maktaba-cast", in: arguments), fake.hasPrefix("fake") {
            // every test run starts before the first cast: the local network screen shows
            UserDefaults.standard.removeObject(forKey: interstitialKey)
            return CastController(context: FakeCastContext(empty: fake == "fake-empty"))
        }
        #endif
        #if canImport(GoogleCast) && os(iOS)
        if config.isEnabled {
            return CastController(context: GoogleCastContext.make(appID: config.receiverAppID))
        }
        #endif
        return CastController(context: nil)
    }

    /// Whether this build casts at all.
    var available: Bool { context != nil }
    /// Whether the Cast button shows for this home.
    var showsButton: Bool { available && reach == .castable }
    var isCasting: Bool { device != nil && castVideoID != nil }

    /// Reads where the home is reachable from (once per home).
    func readReach(_ client: APIClient) async {
        guard available else { return }
        if self.client?.home != client.home {
            self.client = client
            reach = .unknown
        }
        guard reach == .unknown else { return }
        do {
            let d = try await client.discovery()
            discovery = d
            reach = CastCustomData.castable(senderBase: client.home, discoveryURLs: d.urls) ? .castable : .unreachable
        } catch {
            Log.failure("cast: read the home's addresses", error)
        }
    }

    /// The Cast button: Google's local network screen before the first cast, then the list.
    func castTapped() {
        problem = nil
        if defaults.bool(forKey: Self.interstitialKey) {
            context?.startDiscovery()
            showsPicker = true
        } else {
            showsInterstitial = true
        }
    }

    /// The local network screen's Continue: iOS asks next, as the screen said.
    func interstitialContinued() {
        defaults.set(true, forKey: Self.interstitialKey)
        showsInterstitial = false
        context?.startDiscovery()
        showsPicker = true
    }

    /// Casts `video` from `at` to `device`.
    func cast(_ video: Video, at: Double, item: CastItem? = nil, to device: CastDevice) {
        problem = nil
        busy = true
        pending = (video, at, item ?? .video(video.id))
        if self.device == device {
            Task { await loadPending() }
        } else {
            context?.connect(device)
        }
    }

    func play() { context?.play() }
    func pause() { context?.pause() }
    func skip(by seconds: Double) { context?.seek(to: max(0, (status?.position ?? 0) + seconds)) }
    func next() { context?.next() }

    /// Stop casting: the video comes back to the phone where the TV was.
    func stop() {
        context?.disconnect()
    }

    private func heard(_ event: CastEvent) {
        switch event {
        case .devicesChanged:
            devices = context?.devices ?? []
        case let .connected(d):
            device = d
            Task { await loadPending() }
        case .failedToConnect:
            busy = false
            pending = nil
            problem = .connect
        case .disconnected:
            ended()
        case let .status(s):
            follow(s)
        case let .message(m):
            if case let .error(e) = m, let err = CastReceiverError(rawValue: e) { problem = .receiver(err) }
        case let .loadFailed(e):
            busy = false
            problem = e.flatMap(CastReceiverError.init(rawValue:)).map(Problem.receiver) ?? .failed
        }
    }

    /// The TV's status: the phone follows the item and position it reports.
    private func follow(_ s: CastReceiverStatus) {
        status = s
        if let id = s.videoID, id != castVideoID, castVideoID != nil {
            castVideoID = id
            if let client { Task { title = (try? await client.video(id))?.displayTitle ?? title } }
        }
    }

    private func ended() {
        let id = castVideoID, at = status?.position
        refreshTask?.cancel()
        device = nil
        status = nil
        castVideoID = nil
        oldHome = false
        busy = false
        if let id { giveBack?(id, at) }
    }

    private func loadPending() async {
        guard let (video, at, item) = pending, let client, let context else { busy = false; return }
        pending = nil
        defer { busy = false }
        do {
            // read again at cast time (the home's addresses may have moved since the screen opened)
            let d: HomeDiscovery
            if let fresh = try? await client.discovery() { d = fresh } else if let discovery { d = discovery } else {
                throw APIError.transport("no discovery")
            }
            discovery = d
            let start = handOver?(video.id) ?? at
            switch try await client.mintCastGrant(item) {
            case let .granted(grant):
                guard let data = CastCustomData.build(senderBase: client.home, discoveryURLs: d.urls, grant: grant.grant,
                                                      item: item, startSec: start, lang: Self.language,
                                                      apiLevel: d.compat.apiLevel) else {
                    reach = .unreachable
                    giveBack?(video.id, nil)
                    return
                }
                begin(video)
                context.load(CastLoad(contentID: video.id, contentURL: nil, title: video.displayTitle, startSec: start, customData: data))
                keepGranted(grant, item: item)
            case .oldHome:
                // the SDK was set up with Maktaba's receiver: it plays a plain load as it came
                let s = try await client.openStream(video: video.id, startSec: Int(start))
                guard let url = s.playbackURL(home: client.home) else { throw APIError.decoding("no stream URL") }
                begin(video)
                oldHome = true
                context.load(CastLoad(contentID: url.absoluteString, contentURL: url, title: video.displayTitle,
                                      startSec: start, customData: nil))
            case let .refused(why):
                problem = .refused(why)
                giveBack?(video.id, nil)
            }
        } catch is CancellationError {
            giveBack?(video.id, nil)
        } catch {
            Log.failure("cast: load", error)
            problem = .failed
            giveBack?(video.id, nil)
        }
    }

    private func begin(_ video: Video) {
        castVideoID = video.id
        title = video.displayTitle
        oldHome = false
        showsPicker = false
    }

    /// The next grant, 30 minutes before this one ends, while the cast plays and the app runs.
    private func keepGranted(_ grant: CastGrant, item: CastItem) {
        refreshTask?.cancel()
        guard let at = grant.refreshAt() else { return }
        refreshTask = Task { [weak self] in
            let wait = max(0, at.timeIntervalSinceNow)
            try? await Task.sleep(for: .seconds(wait))
            guard !Task.isCancelled, let self, self.isCasting, let client = self.client else { return }
            do {
                if case let .granted(next) = try await client.mintCastGrant(item) {
                    self.context?.send(.grant(next.grant))
                    self.keepGranted(next, item: item)
                }
            } catch {
                Log.failure("cast: renew the grant", error)
            }
        }
    }

    /// The app's language, for the receiver's screens.
    static var language: String {
        CastCustomData.language(AppLanguage.locale.language.languageCode?.identifier ?? "en")
    }

    /// The state the UI tests read (`cast.state`'s value in a Debug build).
    var testState: String {
        var parts = [
            "device=\(device?.name ?? "-")",
            "state=\(status?.state.rawValue ?? "-")",
            "position=\(Int(status?.position ?? 0))",
            "video=\(castVideoID ?? "-")",
            "oldHome=\(oldHome)",
        ]
        #if DEBUG
        if let fake = context as? FakeCastContext, let load = fake.lastLoad {
            parts.append("home=\(load.customData?["home"] as? String ?? "-")")
            parts.append("grant=\((load.customData?["grant"] as? String).map { _ in "yes" } ?? "no")")
            parts.append("lang=\(load.customData?["lang"] as? String ?? "-")")
            parts.append("contentURL=\(load.contentURL == nil ? "no" : "yes")")
        }
        #endif
        return parts.joined(separator: ";")
    }
}
