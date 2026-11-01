import AVFoundation
import MaktabaKit
import SwiftUI

/// One channel playing, and surfing to the next (FR-23). MaktabaKit's `LiveSource` picks the
/// path from `GET /api/tv/info`:
/// - at home the channel's playlist on the web port, read through `StreamProxy` (the port
///   wants the user's token on every request and AVPlayer cannot renew one), after waiting for
///   the encoder to write it (the first request starts the channel), as the web player does;
/// - away a remote session: tune, a heartbeat every 60 s, the session ended when the channel
///   changes or the player closes. The stream plays through `LiveRelayLoader`, so a
///   heartbeat's new token reaches the item already playing (its next playlist reload) with
///   no reopen, no black gap and no lost live edge.
///
/// Story 48.24: on an AirPlay receiver (which fetches the stream itself) a channel at home
/// plays from the proxy on this device's LAN address, serving that channel only; away, from
/// the remote session's signed URL, handed again on each new token. The heartbeat keeps
/// running here. Picture in Picture floats it; Now Playing shows the channel and programme,
/// and play, pause and next channel work from the lock screen and the remotes.
@MainActor @Observable
final class LivePlayback {
    enum Phase: Equatable {
        case starting
        case playing
        /// Why this channel does not play (another channel still can).
        case failed(HomeFailure)
        /// The home ended the remote session.
        case ended
    }

    let client: APIClient
    let player = AVPlayer()
    /// The lineup in number order (next and previous go through it, wrapping round).
    private(set) var channels: [LiveChannel] = []
    private(set) var channelID: String
    private(set) var phase = Phase.starting
    private(set) var path: LivePath?
    /// The person paused.
    private(set) var paused = false
    /// Behind the live edge since a pause: "Back to live" shows.
    private(set) var behind = false
    /// Bumped on every tune (the banner shows again).
    private(set) var tunes = 0

    private var running: Task<Void, Never>?
    private var proxy: StreamProxy?
    /// The playing remote stream's loader (the asset holds it weakly).
    private var relay: LiveRelayLoader?
    private let heartbeat: Duration
    /// The channel's URL as the source gave it.
    private var url: URL?
    /// Where the picture plays (story 48.24).
    private(set) var routes = RouteSwitch()
    @ObservationIgnored private var externalWatcher: ExternalPlaybackWatcher?
    /// The channel for an AirPlay receiver at home (this device's LAN address).
    private var lanProxy: StreamProxy?
    private var switching: Task<Void, Never>?
    /// Picture in Picture for the live picture.
    let pip = PictureInPicture()
    /// What is on now (the screen tells), for Now Playing.
    private var programme: String?

    /// Surfing: the channel tunes this long after the last next / previous, so passing a
    /// channel does not open its stream (the web's 300 ms).
    static let settle: Duration = .milliseconds(300)
    /// How long a channel's first playlist may take (the encoder starts on the first ask).
    static let warmUp: Duration = .seconds(60)

    init(client: APIClient, channel: String, heartbeat: Duration = LivePlayback.heartbeat()) {
        self.client = client
        channelID = channel
        self.heartbeat = heartbeat
        player.allowAirPlay()
        externalWatcher = ExternalPlaybackWatcher(player) { [weak self] active in self?.externalPlaybackChanged(active) }
    }

    /// The remote session's heartbeat: every 60 s; a Debug build takes
    /// `-maktaba-live-heartbeat <seconds>` so a UI test sees beats without waiting a minute.
    static func heartbeat(arguments: [String] = CommandLine.arguments) -> Duration {
        #if DEBUG
        if let s = ShellLaunch.value(of: "-maktaba-live-heartbeat", in: arguments), let n = Double(s), n > 0 {
            return .seconds(n)
        }
        #endif
        return .seconds(60)
    }

    var channel: LiveChannel? { channels.first { $0.id == channelID } }

    /// The lineup changed (asked again each minute): keep the same channel.
    func update(channels new: [LiveChannel]) {
        if new.map(\.id) != channels.map(\.id) { channels = new }
    }

    /// The next channel by number (after the last, the first).
    func next() { step(1) }

    /// The previous channel.
    func previous() { step(-1) }

    private func step(_ by: Int) {
        guard !channels.isEmpty else { return }
        let i = channels.firstIndex { $0.id == channelID } ?? 0
        let n = channels.count
        tune(channels[((i + by) % n + n) % n].id, settle: true)
    }

    /// The channel with `number` (the number pad); false when there is none.
    @discardableResult func tune(number: Int) -> Bool {
        guard let c = channels.first(where: { $0.number == number }) else { return false }
        if c.id != channelID { tune(c.id, settle: false) }
        return true
    }

    /// Plays `id` (at once, or after `settle` while surfing).
    func tune(_ id: String, settle: Bool = false) {
        channelID = id
        tunes += 1
        phase = .starting
        paused = false
        behind = false
        running?.cancel()
        let previous = running
        running = Task { [weak self] in
            // the channel being left ends its own session first
            await previous?.value
            if settle { try? await Task.sleep(for: Self.settle) }
            guard !Task.isCancelled else { return }
            await self?.play(id)
        }
    }

    /// Plays the current channel (the screen appearing, Try again, Watch again).
    func start() { tune(channelID) }

    /// Pause, or play on from where it was (still behind live).
    func togglePause() {
        if paused {
            player.play()
            paused = false
        } else {
            player.pause()
            paused = true
            behind = true
        }
        showNowPlaying()
    }

    /// Back to the live edge after a pause.
    func backToLive() {
        if let edge = player.currentItem?.seekableTimeRanges.last?.timeRangeValue.end, edge.isNumeric {
            player.seek(to: edge)
        }
        player.play()
        paused = false
        behind = false
        showNowPlaying()
    }

    /// Stops playing and ends a remote session; returns once the home was told.
    func close() async {
        running?.cancel()
        await running?.value
        running = nil
        switching?.cancel()
        player.pause()
        player.replaceCurrentItem(with: nil)
        proxy?.stop()
        proxy = nil
        lanProxy?.stop()
        lanProxy = nil
        relay = nil
        url = nil
        pip.stop()
        NowPlaying.shared.end(self)
    }

    /// Opens and watches one channel until the task is cancelled; its remote session (if it
    /// had one) is ended on the way out.
    private func play(_ id: String) async {
        let source = LiveSource(api: client, channel: id, heartbeat: heartbeat)
        await run(source)
        // in a task of its own: this one is cancelled by now (leaving, another channel), and a
        // cancelled task's request would never reach the home
        await Task { await source.close() }.value
    }

    private func run(_ source: LiveSource) async {
        do {
            let (url, path) = try await source.open()
            try Task.checkCancellation()
            self.path = path
            try await prepare(url, path: path)
            let item = try await freshItem()
            try Task.checkCancellation()
            player.replaceCurrentItem(with: item)
            PlaybackAudio.activate()
            player.play()
            phase = .playing
            startNowPlaying()
            await watch(source)
        } catch is CancellationError {
            return
        } catch {
            guard !Task.isCancelled else { return }
            player.replaceCurrentItem(with: nil)
            Log.failure("play live channel", error)
            phase = .failed(HomeFailure(error))
        }
    }

    /// Readies a channel's stream: at home the proxy, once the playlist exists; away the relay
    /// loader on the signed URL.
    private func prepare(_ url: URL, path: LivePath) async throws {
        self.url = url
        switch path {
        case .lan:
            relay = nil
            let auth = try await client.playbackHeaders()
            let proxy = try self.proxy ?? StreamProxy(client: client)
            self.proxy = proxy
            try await proxy.start()
            try await client.waitUntilPlayable(url, headers: auth.headers, timeout: Self.warmUp)
        case .relay:
            relay = LiveRelayLoader(url: Self.absolute(url, home: client.home))
        }
    }

    /// What AVPlayer plays now, for where it plays: on this device the proxy's address or the
    /// relay loader; on an AirPlay receiver the LAN proxy or the newest signed URL.
    private func freshItem() async throws -> AVPlayerItem {
        guard let url, let path else { throw URLError(.cannotConnectToHost) }
        var lan: URL?
        if path == .lan, routes.route == .receiver {
            lan = try await lanProxied(url)
        } else if lanProxy != nil {
            lanProxy?.stop()
            lanProxy = nil
        }
        let choice = RouteURL.live(path: path, route: routes.route, proxied: proxy?.local(url), lanProxied: lan,
                                   relay: relay?.current)
        switch choice {
        case let .url(u)?: return AVPlayerItem(url: u)
        case .relayLoader?:
            guard let relay else { throw URLError(.cannotConnectToHost) }
            return AVPlayerItem(asset: relay.makeAsset())
        case nil: throw URLError(.cannotConnectToHost)
        }
    }

    /// The channel through a proxy on this device's LAN address, serving that channel only
    /// (nil: no LAN address, e.g. cellular only).
    private func lanProxied(_ url: URL) async throws -> URL? {
        let scope = RouteURL.channelScope(url)
        if let p = lanProxy, p.onlyUnder == scope { return p.local(url) }
        lanProxy?.stop()
        lanProxy = nil
        guard let host = LANAddress.current() else { return nil }
        let p = try StreamProxy(client: client, reach: .lan(host: host), onlyUnder: scope)
        try await p.start()
        lanProxy = p
        return p.local(url)
    }

    // MARK: AirPlay (story 48.24)

    /// AirPlay took the channel to a receiver, or gave it back: a new item on the URL that
    /// route can read (a live item opens at the live edge).
    private func externalPlaybackChanged(_ active: Bool) {
        guard let route = routes.update(externalPlaybackActive: active), phase == .playing else { return }
        let to = route.rawValue
        Log.ui.info("live route: \(to, privacy: .public)")
        reopenForRoute()
    }

    private func reopenForRoute() {
        switching?.cancel()
        switching = Task { [weak self] in
            guard let self else { return }
            do {
                let item = try await self.freshItem()
                guard !Task.isCancelled, self.phase == .playing else { return }
                self.player.replaceCurrentItem(with: item)
                self.behind = false
                if !self.paused { self.player.play() }
            } catch {
                Log.failure("hand the live channel over", error)
            }
        }
    }

    /// A heartbeat's new token: a receiver reads the signed URL itself, so it is handed the new
    /// one (on the device the relay loader already carries it).
    private func tokenApplied() {
        guard routes.route == .receiver, path == .relay, phase == .playing else { return }
        reopenForRoute()
    }

    // MARK: Now Playing (story 48.24)

    /// What is on now, from the guide (the screen tells).
    func showProgramme(_ title: String?) {
        guard title != programme else { return }
        programme = title
        showNowPlaying()
    }

    private func startNowPlaying() {
        NowPlaying.shared.begin(self, handlers: RemoteHandlers(
            play: { [weak self] in if self?.paused == true { self?.togglePause() } },
            pause: { [weak self] in if self?.paused == false { self?.togglePause() } },
            toggle: { [weak self] in self?.togglePause() },
            next: { [weak self] in self?.next() }
        ))
        showNowPlaying()
        guard let c = channel, c.hasLogo else { return }
        let id = c.id
        Task { [client, weak self] in
            guard let data = try? await client.channelLogo(id), let image = PlatformImage(data: data),
                  let self, self.channelID == id else { return }
            NowPlaying.shared.show(self, artwork: image)
        }
    }

    private func showNowPlaying() {
        let name = channel.map { "\($0.number.formatted()) · \($0.name)" } ?? ""
        NowPlaying.shared.show(self, NowPlayingItem(title: name, subtitle: programme, playing: !paused && phase == .playing))
    }

    /// A relay URL as the home gave it; a path alone (a home serving its own relay) on the home.
    static func absolute(_ url: URL, home: URL) -> URL {
        guard url.host() == nil else { return url }
        return URL(string: url.relativeString, relativeTo: home)?.absoluteURL ?? url
    }

    /// While the channel plays: the remote session's news (each new token goes to the loader,
    /// the item keeps playing; a session the home ended ends here), and an item that failed
    /// (an encoder that restarted) opened again, twice, then said.
    private func watch(_ source: LiveSource) async {
        let follower = Task { @MainActor [weak self, relay] in
            guard let relay else { return }
            if await relay.follow(source, applied: { _ in self?.tokenApplied() }) { self?.endedByHome() }
        }
        defer { follower.cancel() }
        var reopened = 0
        while !Task.isCancelled, phase == .playing {
            try? await Task.sleep(for: .seconds(3))
            guard !Task.isCancelled, phase == .playing else { return }
            guard player.currentItem?.status == .failed else { continue }
            if reopened >= 2 {
                phase = .failed(player.currentItem?.error.map(HomeFailure.init) ?? HomeFailure(kind: .server))
                player.replaceCurrentItem(with: nil)
                return
            }
            reopened += 1
            guard let again = try? await freshItem() else { continue }
            player.replaceCurrentItem(with: again)
            if !paused { player.play() }
        }
    }

    private func endedByHome() {
        player.replaceCurrentItem(with: nil)
        phase = .ended
    }
}
