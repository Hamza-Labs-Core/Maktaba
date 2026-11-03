import AVKit
import MaktabaKit
import SwiftUI

/// One channel playing, and surfing to the next: MaktabaKit's `LiveSource` picks the path
/// from `GET /api/tv/info` (FR-23). At home the channel's playlist comes through the web
/// port, read through `StreamProxy` (the port wants the user's token on every request and
/// AVPlayer cannot renew one), after waiting for the encoder to write it (a first request
/// starts the channel). Away, a remote session: tune, a heartbeat every 60 s, a new token
/// taken without reopening, the session ended when the channel changes or the player
/// closes.
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
    private(set) var channels: [LiveChannel]
    private(set) var index: Int
    private(set) var phase = Phase.starting
    /// Bumped on every tune: the banner shows again.
    private(set) var tunes = 0
    /// The picture waits for data while the channel plays (a buffering stall): AVPlayer's
    /// `timeControlStatus` waiting after the picture moved, or its stall notice. The phase
    /// stays `.playing`; `LiveOverlays` turns a lasting wait into the buffering state.
    private(set) var waiting = false
    /// The current item's picture has moved: a wait from then on is a stall, not the start.
    private var moved = false

    @ObservationIgnored private var statusObservation: NSKeyValueObservation?
    @ObservationIgnored private var stallObserver: NSObjectProtocol?

    private var running: Task<Void, Never>?
    private var proxy: StreamProxy?
    /// A remote session's newest URL (its token swapped by a heartbeat), for a reopen.
    private var relayURL: URL?

    /// Surfing: the channel tunes this long after the last ▲ / ▼, so passing a channel does
    /// not open its stream (the web's 300 ms).
    static let settle: Duration = .milliseconds(300)
    /// How long a channel's first playlist may take (the encoder starts on the first ask).
    static let warmUp: Duration = .seconds(60)

    init(client: APIClient, channels: [LiveChannel], start: String) {
        self.client = client
        self.channels = channels
        index = channels.firstIndex { $0.id == start } ?? 0
        statusObservation = Self.observeStatus(player) { [weak self] status in self?.timeControl(status) }
        stallObserver = NotificationCenter.default.addObserver(
            forName: AVPlayerItem.playbackStalledNotification, object: nil, queue: .main
        ) { [weak self] note in
            // only the item's identity crosses over (a Notification is not Sendable)
            let stalled = (note.object as? AVPlayerItem).map(ObjectIdentifier.init)
            MainActor.assumeIsolated {
                guard let self, let stalled, let item = self.player.currentItem,
                      ObjectIdentifier(item) == stalled else { return }
                self.waiting = true
            }
        }
    }

    /// KVO may call back on any thread: the handler is made outside the main actor and hops on.
    nonisolated private static func observeStatus(
        _ player: AVPlayer, _ changed: @escaping @MainActor @Sendable (AVPlayer.TimeControlStatus) -> Void
    ) -> NSKeyValueObservation {
        player.observe(\.timeControlStatus, options: [.initial, .new]) { p, _ in
            let status = p.timeControlStatus
            Task { @MainActor in changed(status) }
        }
    }

    /// The picture moved, waits, or was paused.
    private func timeControl(_ status: AVPlayer.TimeControlStatus) {
        switch status {
        case .playing:
            moved = true
            waiting = false
        case .waitingToPlayAtSpecifiedRate:
            if moved { waiting = true }
        case .paused:
            waiting = false
        @unknown default:
            break
        }
    }

    /// A new item plays (a tune, or one opened again): it starts, it does not stall.
    private func replaceItem(_ item: AVPlayerItem?) {
        moved = false
        waiting = false
        player.replaceCurrentItem(with: item)
    }

    var channel: LiveChannel? { channels.indices.contains(index) ? channels[index] : nil }

    /// The lineup changed (the Live tab asked again): keep the same channel.
    func update(channels new: [LiveChannel]) {
        guard !new.isEmpty, new.map(\.id) != channels.map(\.id) else { return }
        let id = channel?.id
        channels = new
        index = new.firstIndex { $0.id == id } ?? min(index, new.count - 1)
    }

    /// ▲: the next channel by number (after the last, the first).
    func next() { tune(index + 1) }

    /// ▼: the previous channel.
    func previous() { tune(index - 1) }

    /// Plays the channel at `i` (wrapping round the lineup).
    func tune(_ i: Int) {
        guard !channels.isEmpty else { return }
        index = ((i % channels.count) + channels.count) % channels.count
        retune(settle: tunes > 0)
    }

    /// Plays the current channel again (Try again).
    func retry() { retune(settle: false) }

    /// Stops playing and ends a remote session; returns once the home was told.
    func close() async {
        running?.cancel()
        await running?.value
        running = nil
        player.pause()
        replaceItem(nil)
        proxy?.stop()
        proxy = nil
        statusObservation?.invalidate()
        if let stallObserver { NotificationCenter.default.removeObserver(stallObserver) }
        stallObserver = nil
    }

    private func retune(settle: Bool) {
        guard let id = channel?.id else { return }
        tunes += 1
        phase = .starting
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

    /// Opens and watches one channel until the task is cancelled; the channel's remote
    /// session (if it had one) is ended on the way out.
    private func play(_ id: String) async {
        let source = LiveSource(api: client, channel: id)
        await run(source)
        // in a task of its own: this one is cancelled by now (leaving, another channel), and a
        // cancelled task's request would never reach the home
        await Task { await source.close() }.value
    }

    private func run(_ source: LiveSource) async {
        do {
            let (url, path) = try await source.open()
            try Task.checkCancellation()
            let itemURL = try await playable(url, path: path)
            try Task.checkCancellation()
            replaceItem(AVPlayerItem(url: itemURL))
            player.play()
            phase = .playing
            await watch(source, path: path, opened: itemURL)
        } catch is CancellationError {
            return
        } catch {
            guard !Task.isCancelled else { return }
            replaceItem(nil)
            Log.failure("play live channel", error)
            phase = .failed(HomeFailure.live(error))
        }
    }

    /// Where AVPlayer reads `url`: at home through the proxy, once its playlist exists;
    /// away, the signed URL itself.
    private func playable(_ url: URL, path: LivePath) async throws -> URL {
        guard path == .lan else { return url }
        let auth = try await client.playbackHeaders()
        let proxy = try self.proxy ?? StreamProxy(client: client)
        self.proxy = proxy
        try await proxy.start()
        guard let local = proxy.local(url) else {
            throw HomeFailureError(HomeFailure(kind: .server, detail: String(localized: "Could not start playback on this TV.")))
        }
        try await client.waitUntilPlayable(url, headers: auth.headers, timeout: Self.warmUp)
        return local
    }

    /// While the channel plays: the remote session's news (a new token is kept for a
    /// reopen; an ended session ends here), and an item that failed opened again (an
    /// encoder that restarted), twice, then said.
    private func watch(_ source: LiveSource, path: LivePath, opened: URL) async {
        relayURL = opened
        let events = Task { @MainActor [weak self] in
            for await e in source.events {
                switch e {
                case let .token(u): self?.relayURL = u
                case .ended: self?.endedByHome()
                }
            }
        }
        defer { events.cancel() }
        var reopened = 0
        while !Task.isCancelled, phase == .playing {
            try? await Task.sleep(for: .seconds(3))
            guard !Task.isCancelled, phase == .playing else { return }
            guard player.currentItem?.status == .failed else { continue }
            if reopened >= 2 {
                phase = .failed(player.currentItem?.error.map(HomeFailure.init)
                    ?? HomeFailure(kind: .server, detail: String(localized: "The channel stopped playing.")))
                replaceItem(nil)
                return
            }
            reopened += 1
            let url = path == .relay ? (relayURL ?? opened) : opened
            replaceItem(AVPlayerItem(url: url))
            player.play()
        }
    }

    private func endedByHome() {
        replaceItem(nil)
        phase = .ended
    }
}

/// A `HomeFailure` thrown where an error is expected.
struct HomeFailureError: Error {
    let failure: HomeFailure
    init(_ failure: HomeFailure) { self.failure = failure }
}

extension HomeFailure {
    /// A thrown `HomeFailureError` as itself; anything else as `init(_:)` reads it.
    static func live(_ error: Error) -> HomeFailure {
        (error as? HomeFailureError)?.failure ?? HomeFailure(error)
    }
}

/// The live player, full screen: the picture, a banner on each tune (number, logo, name,
/// what is on with its progress, what is next), ▲ / ▼ for the next and previous channel,
/// Select for the mini-guide (the channels around this one, now and next), Play/Pause for the
/// banner again, Menu to put the mini-guide or the banner away, then to go back to the Live
/// tab. When each overlay goes is `LiveOverlays`' (MaktabaKit, unit-tested): the banner
/// `LiveOverlays.bannerTime` after the channel actually plays, however long it took to start.
///
/// A buffering stall (the picture waits while the channel plays) brings the banner back with
/// a buffering line; both go when the picture moves again (`LiveOverlays`, a short wait shows
/// nothing).
///
/// The home burns the channel's corner logo (its watermark) into the picture itself, top
/// right by default: the picture is shown whole (`.resizeAspect`, edge to edge, nothing
/// cropped) and every overlay sits at the bottom or in the middle, clear of that corner. A
/// logo in a bottom corner (`watermark_position`) moves the banner and the mini-guide to the
/// top (`LiveOverlays.edge(clearing:)`).
struct LivePlayerScreen: View {
    let model: LiveModel
    @State private var playback: LivePlayback
    @State private var overlays = LiveOverlays()
    /// The moment the overlays are drawn for (moved on each event and at each deadline).
    @State private var now = ContinuousClock.now
    @Environment(\.dismiss) private var dismiss
    @Environment(\.appSession) private var session

    init(client: APIClient, model: LiveModel, start: String) {
        self.model = model
        _playback = State(initialValue: LivePlayback(client: client, channels: model.channels, start: start))
    }

    var body: some View {
        ZStack {
            Color.black
            LiveVideoLayer(player: playback.player)
                .accessibilityElement()
                .accessibilityLabel(Text("Live picture"))
                .accessibilityIdentifier("live.player.video")
            // the one focusable thing: it takes Select, and ▲ / ▼ / Menu reach the screen
            // through it (the banner and the mini-guide are beside it, so each keeps its
            // own accessibility element)
            Button(action: select) {
                Color.clear.frame(maxWidth: .infinity, maxHeight: .infinity)
            }
            .buttonStyle(LiveSurfaceStyle())
            .accessibilityLabel(Text(playback.channel?.name ?? ""))
            .accessibilityIdentifier("live.player.surface")
            Group {
                status
                if overlays.miniGuide(at: now) {
                    LiveMiniGuide(model: model, channels: playback.channels, current: playback.index, edge: edge)
                        .frame(maxHeight: .infinity, alignment: edge == .top ? .top : .bottom)
                        .transition(.opacity)
                } else if overlays.banner(at: now), let channel = playback.channel {
                    LiveBanner(channel: channel, now: model.onNow(channel.id), edge: edge)
                        .frame(maxHeight: .infinity, alignment: edge == .top ? .top : .bottom)
                        .transition(.opacity)
                }
            }
            .allowsHitTesting(false)
        }
        .ignoresSafeArea()
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("live.player")
        .onMoveCommand { direction in
            switch direction {
            case .up: playback.next()
            case .down: playback.previous()
            default: break
            }
        }
        .onPlayPauseCommand { change { $0.showBanner(at: $1) } }
        .onExitCommand {
            var handled = false
            change { handled = $0.back(at: $1) }
            if !handled { dismiss() }
        }
        .onAppear { playback.tune(playback.index) }
        .onDisappear {
            let p = playback
            Task { await p.close() }
        }
        .onChange(of: model.channels.map(\.id)) { _, _ in playback.update(channels: model.channels) }
        .onChange(of: playback.tunes) { _, _ in change { $0.tuned(at: $1) } }
        .onChange(of: playback.phase) { _, phase in change { $0.playback(playing: phase == .playing, at: $1) } }
        .onChange(of: playback.waiting) { _, waiting in
            change { waiting ? $0.stalled(at: $1) : $0.resumed(at: $1) }
        }
        .task(id: Wake(overlays: overlays, now: now)) {
            // asleep until the next overlay goes (none while nothing plays), then redrawn
            guard let next = overlays.nextChange(after: now) else { return }
            try? await Task.sleep(until: next, clock: .continuous)
            guard !Task.isCancelled else { return }
            withAnimation { now = .now }
        }
    }

    /// Where the banner and the mini-guide sit: clear of the channel's burned-in logo.
    private var edge: LiveOverlays.Edge { LiveOverlays.edge(clearing: playback.channel?.watermark) }

    /// What the overlays' next redraw depends on.
    private struct Wake: Equatable {
        let overlays: LiveOverlays
        let now: ContinuousClock.Instant
    }

    /// One event for the overlays, at this moment.
    private func change(_ apply: (inout LiveOverlays, ContinuousClock.Instant) -> Void) {
        let t = ContinuousClock.now
        withAnimation {
            apply(&overlays, t)
            now = t
        }
    }

    /// Select: the failure's first action when the channel did not play, else the mini-guide.
    private func select() {
        switch playback.phase {
        case let .failed(f):
            switch f.actions.first {
            case .tryAgain: playback.retry()
            case .signInAgain:
                dismiss()
                session?.signedOut()
            case .chooseAnotherHome:
                dismiss()
                session?.switchHome()
            case .back, nil: dismiss()
            }
        case .ended:
            playback.retry()
        case .starting, .playing:
            change { $0.toggleMiniGuide(at: $1) }
        }
    }

    @ViewBuilder private var status: some View {
        switch playback.phase {
        case .starting:
            VStack(spacing: 24) {
                ProgressView().tint(.white)
                Text("Tuning…").foregroundStyle(.white.opacity(0.85))
            }
            .accessibilityIdentifier("live.player.starting")
        case let .failed(f):
            LiveMessage(symbol: f.symbol, text: f.message(video: true),
                        hint: f.actions.first == .tryAgain
                            ? String(localized: "Select to try again, or ▲ ▼ for another channel.")
                            : String(localized: "▲ ▼ for another channel."))
                .accessibilityIdentifier("live.player.error")
        case .ended:
            LiveMessage(symbol: "stop.circle", text: String(localized: "The home ended this stream."),
                        hint: String(localized: "Select to watch again, or ▲ ▼ for another channel."))
                .accessibilityIdentifier("live.player.ended")
        case .playing:
            if overlays.buffering(at: now) {
                VStack(spacing: 24) {
                    ProgressView().tint(.white)
                    Text("Buffering…").foregroundStyle(.white.opacity(0.85))
                }
                .accessibilityElement(children: .combine)
                .accessibilityIdentifier("live.player.buffering")
            }
        }
    }
}

/// The whole screen as one button: no lift, no ring (the picture is not a card).
private struct LiveSurfaceStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View { configuration.label }
}

/// Why nothing plays, over the black picture.
private struct LiveMessage: View {
    let symbol: String
    let text: String
    let hint: String

    var body: some View {
        VStack(spacing: 20) {
            Image(systemName: symbol).font(.system(size: 72)).foregroundStyle(Theme.gold)
            Text(text).font(Theme.body(34, weight: .semibold)).foregroundStyle(.white)
                .multilineTextAlignment(.center)
            Text(hint).font(Theme.body(28)).foregroundStyle(.white.opacity(0.75))
                .multilineTextAlignment(.center)
        }
        .padding(48)
        .frame(maxWidth: 1200)
        .environment(\.colorScheme, .dark)
    }
}

/// The channel, on each tune: number, logo, name, what is on with its progress, what is next.
struct LiveBanner: View {
    let channel: LiveChannel
    let now: ChannelNow?
    var edge: LiveOverlays.Edge = .bottom

    var body: some View {
        TimelineView(.periodic(from: .now, by: 30)) { context in
            HStack(alignment: .center, spacing: 32) {
                ChannelLogo(channel: channel, size: 120)
                VStack(alignment: .leading, spacing: 10) {
                    Text(verbatim: "\(channel.number.formatted()) · \(channel.name)")
                        .font(Theme.body(32, weight: .semibold))
                        .foregroundStyle(Theme.goldText)
                        .accessibilityIdentifier("live.player.channel")
                    if let current = now?.current {
                        Text(current.title)
                            .font(Theme.heading(48))
                            .foregroundStyle(Theme.text)
                            .lineLimit(1)
                            .accessibilityIdentifier("live.player.programme")
                        HStack(spacing: 20) {
                            Text(current.start, format: .dateTime.hour().minute())
                            ProgressView(value: current.progress(at: context.date))
                                .tint(Theme.gold)
                                .frame(width: 520)
                            Text(current.stop, format: .dateTime.hour().minute())
                        }
                        .font(Theme.body(26))
                        .foregroundStyle(Theme.muted)
                    }
                    if let next = now?.next {
                        Text("Next: \(next.start.formatted(date: .omitted, time: .shortened)) \(next.title)")
                            .font(Theme.body(26))
                            .foregroundStyle(Theme.muted)
                            .lineLimit(1)
                    }
                }
                Spacer(minLength: 0)
            }
            .readingPanel(padding: 36)
            .padding(.horizontal, 80)
            .padding(edge == .top ? .top : .bottom, 60)
        }
        .environment(\.colorScheme, .dark)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("live.player.banner")
    }
}

/// The mini-guide: the channels around this one (two before, two after), each with what is
/// on now and next; this one marked.
struct LiveMiniGuide: View {
    let model: LiveModel
    let channels: [LiveChannel]
    let current: Int
    var edge: LiveOverlays.Edge = .bottom

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Mini guide")
                .font(Theme.heading(36))
                .foregroundStyle(Theme.text)
            ForEach(around, id: \.offset) { _, channel in
                row(channel, current: channel.id == channels[safe: current]?.id)
            }
            Text("▲ ▼ to change channel · Menu to close")
                .font(Theme.body(24))
                .foregroundStyle(Theme.faint)
        }
        .readingPanel(padding: 32)
        .padding(.horizontal, 80)
        .padding(edge == .top ? .top : .bottom, 60)
        .environment(\.colorScheme, .dark)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("live.miniguide")
    }

    /// Up to five channels centred on the current one (fewer in a small lineup).
    private var around: [(offset: Int, element: LiveChannel)] {
        guard !channels.isEmpty else { return [] }
        let n = min(5, channels.count)
        let first = current - n / 2
        return (0..<n).map { k in
            let i = ((first + k) % channels.count + channels.count) % channels.count
            return (k, channels[i])
        }
    }

    private func row(_ channel: LiveChannel, current: Bool) -> some View {
        let now = model.onNow(channel.id)
        return HStack(spacing: 24) {
            Text(channel.number, format: .number)
                .font(Theme.body(28, weight: .semibold))
                .foregroundStyle(current ? Theme.onGold : Theme.goldText)
                .frame(width: 64)
            Text(channel.name)
                .font(Theme.body(28, weight: .semibold))
                .frame(width: 360, alignment: .leading)
                .lineLimit(1)
            Text(verbatim: now?.current?.title ?? " ")
                .font(Theme.body(28))
                .frame(maxWidth: .infinity, alignment: .leading)
                .lineLimit(1)
            Text(verbatim: now?.next.map { "\($0.start.formatted(date: .omitted, time: .shortened)) \($0.title)" } ?? " ")
                .font(Theme.body(24))
                .opacity(0.75)
                .frame(width: 560, alignment: .leading)
                .lineLimit(1)
        }
        .foregroundStyle(current ? Theme.onGold : Theme.text)
        .padding(.horizontal, 20)
        .padding(.vertical, 10)
        .background(current ? Theme.gold : .clear, in: .rect(cornerRadius: 12))
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier(current ? "live.miniguide.current" : "live.miniguide.\(channel.id)")
    }
}

extension Array {
    subscript(safe i: Int) -> Element? { indices.contains(i) ? self[i] : nil }
}

/// The picture alone (no transport bar: live TV has nothing to scrub).
private struct LiveVideoLayer: UIViewRepresentable {
    let player: AVPlayer

    func makeUIView(context: Context) -> LayerView {
        let v = LayerView()
        v.playerLayer.player = player
        v.playerLayer.videoGravity = .resizeAspect
        v.isUserInteractionEnabled = false
        return v
    }

    func updateUIView(_ v: LayerView, context: Context) {
        if v.playerLayer.player !== player { v.playerLayer.player = player }
    }

    final class LayerView: UIView {
        override static var layerClass: AnyClass { AVPlayerLayer.self }
        var playerLayer: AVPlayerLayer { layer as! AVPlayerLayer }
    }
}
