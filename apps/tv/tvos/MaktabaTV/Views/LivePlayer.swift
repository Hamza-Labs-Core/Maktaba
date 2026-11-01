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
        player.replaceCurrentItem(with: nil)
        proxy?.stop()
        proxy = nil
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
        await source.close()
    }

    private func run(_ source: LiveSource) async {
        do {
            let (url, path) = try await source.open()
            try Task.checkCancellation()
            let itemURL = try await playable(url, path: path)
            try Task.checkCancellation()
            player.replaceCurrentItem(with: AVPlayerItem(url: itemURL))
            player.play()
            phase = .playing
            await watch(source, path: path, opened: itemURL)
        } catch is CancellationError {
            return
        } catch {
            guard !Task.isCancelled else { return }
            player.replaceCurrentItem(with: nil)
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
                player.replaceCurrentItem(with: nil)
                return
            }
            reopened += 1
            let url = path == .relay ? (relayURL ?? opened) : opened
            player.replaceCurrentItem(with: AVPlayerItem(url: url))
            player.play()
        }
    }

    private func endedByHome() {
        player.replaceCurrentItem(with: nil)
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
/// Select for the mini-guide (the channels around this one, now and next), Menu to close
/// the mini-guide, then to go back to the Live tab.
struct LivePlayerScreen: View {
    let model: LiveModel
    @State private var playback: LivePlayback
    @State private var miniGuide = false
    @State private var banner = true
    @Environment(\.dismiss) private var dismiss
    @Environment(\.appSession) private var session

    /// How long the banner stays after a tune.
    static let bannerTime: Duration = .seconds(6)

    init(client: APIClient, model: LiveModel, start: String) {
        self.model = model
        _playback = State(initialValue: LivePlayback(client: client, channels: model.channels, start: start))
    }

    var body: some View {
        ZStack {
            Color.black
            LiveVideoLayer(player: playback.player)
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
                if miniGuide {
                    LiveMiniGuide(model: model, channels: playback.channels, current: playback.index)
                        .frame(maxHeight: .infinity, alignment: .bottom)
                } else if banner, let channel = playback.channel {
                    LiveBanner(channel: channel, now: model.onNow(channel.id))
                        .frame(maxHeight: .infinity, alignment: .bottom)
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
        .onExitCommand {
            if miniGuide { miniGuide = false } else { dismiss() }
        }
        .onAppear { playback.tune(playback.index) }
        .onDisappear {
            let p = playback
            Task { await p.close() }
        }
        .onChange(of: model.channels.map(\.id)) { _, _ in playback.update(channels: model.channels) }
        .task(id: playback.tunes) {
            // the banner on each tune, gone a moment later (it stays while nothing plays)
            banner = true
            try? await Task.sleep(for: Self.bannerTime)
            guard !Task.isCancelled, playback.phase == .playing else { return }
            withAnimation { banner = false }
        }
        .onChange(of: playback.phase) { _, phase in
            if phase != .playing { banner = true }
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
            miniGuide.toggle()
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
            EmptyView()
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
            .padding(.bottom, 60)
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
        .padding(.bottom, 60)
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
