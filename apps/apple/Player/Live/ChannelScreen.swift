import AVFoundation
import MaktabaKit
import SwiftUI

/// The live player (`PlayerRoute.channel`, FR-23), full screen: the picture, the channel with
/// what is on and its progress, next and previous channel (the buttons, a vertical swipe, or
/// ▲ / ▼ on a keyboard), the number pad (a keyboard's digits; a keypad on a tablet), a
/// mini-guide (the channels around this one, now and next), pause and "Back to live", AirPlay
/// and Picture in Picture (story 48.24). Leaving ends a remote session.
struct ChannelScreen: View {
    let context: PlayerContext
    let id: String

    @State private var playback: LivePlayback
    @State private var model = LiveModel()
    @State private var controls = true
    @State private var miniGuide = false
    @State private var keypad = false
    /// Digits typed so far (the number pad); the channel tunes a moment after the last one.
    @State private var typed = ""
    /// Epic 57 story 57.10: the programme on now is above the account's limit
    /// (`GET /api/channels/{id}/access`): the slate shows and nothing plays until the home
    /// says otherwise.
    @State private var slated = false
    /// The channel was started (an account with no limit starts it once; a change of
    /// channel after that is the playback's own).
    @State private var begun = false
    /// Moved when the time's-up screen says there is time again: the home is asked afresh.
    @State private var again = 0
    @Environment(\.dismiss) private var dismiss
    @Environment(\.horizontalSizeClass) private var sizeClass

    init(context: PlayerContext, id: String) {
        self.context = context
        self.id = id
        _playback = State(initialValue: LivePlayback(client: context.client, channel: id))
    }

    var body: some View {
        ZStack {
            Color.black
            LiveVideoLayer(player: playback.player) { playback.pip.attach($0) }
                .accessibilityHidden(true)
            // the picture: a tap shows or hides the controls; a vertical swipe changes channel
            Color.clear
                .contentShape(.rect)
                .onTapGesture { withAnimation { controls.toggle() } }
                .gesture(DragGesture(minimumDistance: 40).onEnded(swiped))
                .accessibilityHidden(true)
            status
            if controls { overlay.transition(.opacity) }
            if slated { LiveSlate { dismiss() } }
            if !typed.isEmpty {
                Text(verbatim: typed)
                    .font(.maktabaHeading(56, relativeTo: .largeTitle))
                    .foregroundStyle(Color.mktBrassText)
                    .padding(MaktabaTokens.Spacing.s4)
                    .background(.black.opacity(0.6), in: .rect(cornerRadius: MaktabaTokens.Radius.lg))
                    .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topTrailing)
                    .padding(MaktabaTokens.Spacing.s8)
                    .accessibilityIdentifier("live.player.typed")
            }
        }
        .ignoresSafeArea()
        .environment(\.colorScheme, .dark)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("screen.channel")
        #if os(iOS)
        .toolbar(.hidden, for: .navigationBar, .tabBar)
        .statusBarHidden()
        .persistentSystemOverlays(.hidden)
        #else
        .toolbar(.hidden, for: .windowToolbar)
        #endif
        .focusable()
        .focusEffectDisabled()
        .onKeyPress(.upArrow) { playback.next(); return .handled }
        .onKeyPress(.downArrow) { playback.previous(); return .handled }
        .onKeyPress(.space) { playback.togglePause(); return .handled }
        .onKeyPress(.escape) { dismiss(); return .handled }
        .onKeyPress(characters: .decimalDigits) { press in
            digit(press.characters)
            return .handled
        }
        .task(id: typed) {
            // the number pad: tune a moment after the last digit
            guard !typed.isEmpty else { return }
            try? await Task.sleep(for: .seconds(1.5))
            guard !Task.isCancelled else { return }
            if let n = Int(typed) { playback.tune(number: n) }
            typed = ""
        }
        .task { await model.keepFresh(context.client, guide: false) }
        // under a parental limit the channel starts once the home says its programme may be
        // watched; anyone else's starts as soon as the home has said the account has no
        // limit. Asked again when the channel changes and when the limit becomes known or
        // changes: a channel opened in the first moments after sign-in came before the
        // home's answer about the limit, started as if there were none and showed no slate
        .task(id: Watching(channel: playback.channelID, known: context.session.parental.answered,
                           limited: context.session.parental.current.limited, again: again)) {
            await watchAccess(playback.channelID)
        }
        // the home refused the programme itself: the slate at once; `watchAccess` goes on
        // asking, and starts the channel again when the home says it may be watched
        .onChange(of: playback.phase) { _, phase in phaseChanged(phase) }
        .onChange(of: model.channels.map(\.id), initial: true) { _, _ in playback.update(channels: model.channels) }
        .onChange(of: model.onNow(playback.channelID)?.current?.displayTitle(), initial: true) { _, title in playback.showProgramme(title) }
        .onDisappear {
            let p = playback
            Task { await p.close() }
        }
        .sheet(isPresented: $keypad) {
            LiveKeypad { digit($0) } done: { keypad = false }
                .presentationDetents([.height(360)])
        }
    }

    /// Asks the home whether the programme on `channel` now may be watched, before it starts
    /// and again when the answer may change (`until`): the slate while it may not, the
    /// channel again when it may. The home's streaming service refuses the stream itself, so
    /// this only decides what the screen says.
    private func watchAccess(_ channel: String) async {
        let parental = context.session.parental
        // the home has not said yet whether this account has a limit: nothing starts (an
        // answer that failed counts as one, and keeps what was known)
        guard parental.answered || parental.current.limited else { return }
        guard parental.current.limited else {
            // no limit (or it was just lifted over a slate): the channel plays
            if slated || !begun {
                slated = false
                begun = true
                playback.start()
            }
            return
        }
        var started = false
        while !Task.isCancelled {
            let wait: TimeInterval
            do {
                let access = try await context.client.channelAccess(channel)
                let blocked = access.restricted && !access.allowed
                if blocked {
                    if !slated {
                        slated = true
                        await playback.hold()
                    }
                } else if slated || !started {
                    slated = false
                    started = true
                    begun = true
                    playback.start()
                }
                wait = access.askAgain()
            } catch {
                if Log.isCancellation(error) { return }
                if let up = TimeUp(error) {
                    // 57.14: the account's time is over (403 `parental-time`): time's up, not
                    // the slate; asked again when that screen says there is time
                    slated = false
                    await playback.stopForTime(up)
                    return
                }
                if let after = HomeFailure(error).kind.timeUnknownRetrySeconds {
                    // the home cannot read the account's time yet: nothing starts, nothing
                    // is said to be over; asked again as its Retry-After says
                    wait = TimeInterval(after)
                } else {
                    // the home did not say (a hidden channel answers 404): the channel is
                    // asked for as before, and the player says what the home answers
                    if !started, !slated {
                        started = true
                        begun = true
                        playback.start()
                    }
                    wait = 30
                }
            }
            do { try await Task.sleep(for: .seconds(wait)) } catch { return }
        }
    }

    /// The playback's phase, for the slate. The home refused the programme itself (`held`):
    /// the slate at once; `watchAccess` goes on asking and starts the channel again when
    /// the home says it may be watched. A channel that stopped under a limit may be the
    /// home ending a programme that is now above it, before this device's clock said to ask
    /// again: the home is asked now, once, and the slate replaces the error if that is why.
    private func phaseChanged(_ phase: LivePlayback.Phase) {
        switch phase {
        case .held:
            if !slated { slated = true }
        case let .failed(failure):
            // (a channel the home does not give is the blocked page: nothing to ask)
            guard context.session.parental.current.limited, failure.kind != .notFound else { return }
            let channel = playback.channelID
            Task {
                guard let access = try? await context.client.channelAccess(channel), access.restricted, !access.allowed,
                      playback.channelID == channel, playback.phase == phase else { return }
                slated = true
                await playback.hold()
            }
        case .starting, .playing, .ended, .timeUp:
            break
        }
    }

    /// What `watchAccess` answers for: the channel, and what is known of the limit.
    private struct Watching: Equatable {
        let channel: String
        let known: Bool
        let limited: Bool
        let again: Int
    }

    private func digit(_ s: String) {
        let d = s.filter(\.isNumber)
        guard !d.isEmpty, typed.count < 4 else { return }
        typed += d
    }

    private func swiped(_ v: DragGesture.Value) {
        let dy = v.translation.height
        guard abs(dy) > abs(v.translation.width) * 1.5, abs(dy) > 60 else { return }
        // a swipe up brings the next channel up from below
        if dy < 0 { playback.next() } else { playback.previous() }
    }

    // MARK: what shows over the picture

    @ViewBuilder private var status: some View {
        switch playback.phase {
        case .starting:
            VStack(spacing: MaktabaTokens.Spacing.s3) {
                ProgressView().tint(.white).controlSize(.large)
                Text("Tuning…", tableName: "Live", comment: "The live player while a channel starts")
                    .foregroundStyle(.white.opacity(0.85))
            }
            .accessibilityElement(children: .combine)
            .accessibilityIdentifier("live.player.starting")
        case let .failed(failure) where failure.kind == .notFound && context.session.parental.current.limited:
            // 57.05: a channel the home does not give, under a limit, is the blocked page
            BlockedPageView(context: context, subject: .channel(playback.channelID), back: { dismiss() }) { playback.start() }
        case let .failed(failure):
            HomeErrorView(failure) { act($0) }
                .padding(MaktabaTokens.Spacing.s8)
                .accessibilityIdentifier("live.player.error")
        case .held:
            EmptyView()
        case let .timeUp(up):
            // 57.14: the account's watching time is over; the channel comes back by itself
            // when the home says there is time again (a parent gave some, the slot opened)
            TimeUpView(context: context, up: up, back: { dismiss() }) { again += 1 }
        case .ended:
            VStack(spacing: MaktabaTokens.Spacing.s3) {
                Image(systemName: "stop.circle").font(.system(size: 48)).foregroundStyle(Color.brass)
                Text("The home ended this stream.", tableName: "Live",
                     comment: "The live player: the home ended the remote session")
                    .foregroundStyle(.white)
                Button { playback.start() } label: {
                    Text("Watch again", tableName: "Live", comment: "The live player: tune the same channel again")
                }
                .buttonStyle(.borderedProminent)
                .accessibilityIdentifier("live.player.again")
            }
            .accessibilityElement(children: .contain)
            .accessibilityIdentifier("live.player.ended")
        case .playing:
            EmptyView()
        }
    }

    private func act(_ action: ErrorAction) {
        switch action {
        case .tryAgain: playback.retry()
        case .signInAgain: context.session.signedOut()
        case .chooseAnotherHome: context.session.switchHome()
        case .back: dismiss()
        }
    }

    private var overlay: some View {
        VStack(spacing: 0) {
            topBar
            Spacer(minLength: 0)
            HStack(alignment: .bottom) {
                Spacer(minLength: 0)
                if miniGuide {
                    LiveMiniGuide(model: model, channels: playback.channels, current: playback.channelID,
                                  client: context.client) { playback.tune($0) }
                        .frame(maxWidth: 520)
                }
                surfButtons
            }
            .padding(.horizontal, MaktabaTokens.Spacing.s4)
            bottomBar
        }
        .padding(.horizontal, MaktabaTokens.Spacing.s2)
        .padding(.vertical, MaktabaTokens.Spacing.s4)
        .safeAreaPadding(.all)
        .background {
            LinearGradient(colors: [.black.opacity(0.7), .clear, .clear, .black.opacity(0.7)],
                           startPoint: .top, endPoint: .bottom)
                .allowsHitTesting(false)
        }
    }

    private var topBar: some View {
        HStack(alignment: .top, spacing: MaktabaTokens.Spacing.s3) {
            Button { dismiss() } label: {
                Label { Text("Back", tableName: "Live", comment: "Leaves the live player") }
                    icon: { Image(systemName: "chevron.backward") }
                    .labelStyle(.iconOnly)
                    .font(.title2)
                    .frame(width: 44, height: 44)
                    .background(.ultraThinMaterial, in: .circle)
            }
            .buttonStyle(.plain)
            .accessibilityIdentifier("live.player.back")
            if let channel = playback.channel ?? model.channels.first(where: { $0.id == id }) {
                LiveBanner(channel: channel, now: model.onNow(channel.id), logo: context.client.channelLogoURL(channel))
            }
            Spacer(minLength: 0)
        }
    }

    private var surfButtons: some View {
        VStack(spacing: MaktabaTokens.Spacing.s3) {
            roundButton("chevron.up", id: "live.player.next") { playback.next() } label: {
                Text("Next channel", tableName: "Live", comment: "The live player: the next channel by number")
            }
            roundButton("chevron.down", id: "live.player.previous") { playback.previous() } label: {
                Text("Previous channel", tableName: "Live", comment: "The live player: the previous channel by number")
            }
        }
    }

    private var bottomBar: some View {
        HStack(spacing: MaktabaTokens.Spacing.s3) {
            if playback.phase == .playing {
                roundButton(playback.paused ? "play.fill" : "pause.fill", id: "live.player.pause") { playback.togglePause() } label: {
                    playback.paused
                        ? Text("Play", tableName: "Live", comment: "The live player: play on after a pause")
                        : Text("Pause", tableName: "Live", comment: "The live player: pause")
                }
            }
            if playback.behind {
                Button { playback.backToLive() } label: {
                    Label { Text("Back to live", tableName: "Live", comment: "The live player: jump to the live edge after a pause") }
                        icon: { Image(systemName: "dot.radiowaves.left.and.right") }
                }
                .buttonStyle(.borderedProminent)
                .accessibilityIdentifier("live.player.backToLive")
            }
            Spacer(minLength: 0)
            AirPlayButton(player: playback.player,
                          label: String(localized: "AirPlay", table: "Live",
                                        comment: "The live player: plays the channel on an Apple TV or another AirPlay screen"),
                          identifier: "live.player.airplay")
                .frame(width: 48, height: 48)
                .background(.ultraThinMaterial, in: .circle)
            if PictureInPicture.isSupported, playback.phase == .playing {
                roundButton(playback.pip.isActive ? "pip.exit" : "pip.enter", id: "live.player.pip") { playback.pip.toggle() } label: {
                    Text("Picture in Picture", tableName: "Live", comment: "The live player: floats the channel in a small window over other apps")
                }
            }
            if sizeClass == .regular {
                roundButton("circle.grid.3x3", id: "live.player.keypad") { keypad = true } label: {
                    Text("Number pad", tableName: "Live", comment: "The live player: type a channel's number")
                }
            }
            roundButton("list.bullet.rectangle", id: "live.player.miniGuide") { withAnimation { miniGuide.toggle() } } label: {
                Text("Mini guide", tableName: "Live", comment: "The live player: the channels around this one, now and next")
            }
        }
        .padding(.horizontal, MaktabaTokens.Spacing.s4)
        .padding(.top, MaktabaTokens.Spacing.s3)
    }

    private func roundButton(_ symbol: String, id: String, action: @escaping () -> Void,
                             @ViewBuilder label: () -> Text) -> some View {
        Button(action: action) {
            Label { label() } icon: { Image(systemName: symbol) }
                .labelStyle(.iconOnly)
                .font(.title2)
                .foregroundStyle(.white)
                .frame(width: 48, height: 48)
                .background(.ultraThinMaterial, in: .circle)
        }
        .buttonStyle(.plain)
        .accessibilityIdentifier(id)
    }
}

extension LivePlayback {
    /// Opens the same channel again (Try again).
    func retry() { start() }
}

/// The channel and what is on: number, logo, name, programme, its progress, what is next.
struct LiveBanner: View {
    let channel: LiveChannel
    let now: ChannelNow?
    let logo: URL?

    var body: some View {
        TimelineView(.periodic(from: .now, by: 30)) { context in
            HStack(alignment: .top, spacing: MaktabaTokens.Spacing.s3) {
                ChannelLogo(name: channel.name, number: channel.number, logo: logo, size: 48)
                VStack(alignment: .leading, spacing: 2) {
                    Text(verbatim: "\(channel.number.formatted()) · \(channel.name)")
                        .font(.maktabaBody(MaktabaTokens.TypeSize.sm, weight: .semibold))
                        .foregroundStyle(Color.mktBrassText)
                        .accessibilityIdentifier("live.player.channel")
                    if let current = now?.current {
                        Text(verbatim: current.displayTitle())
                            .font(.maktabaBody(MaktabaTokens.TypeSize.lg, weight: .semibold))
                            .foregroundStyle(.white)
                            .lineLimit(1)
                            .accessibilityIdentifier("live.player.programme")
                        LiveProgress(block: current, at: context.date)
                            .frame(maxWidth: 320)
                    }
                    if let next = now?.next { LiveNextLine(block: next) }
                }
            }
        }
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("live.player.banner")
    }
}

/// The mini-guide: up to five channels centred on this one, each with what is on now and
/// next, this one marked; tapping another tunes it.
struct LiveMiniGuide: View {
    let model: LiveModel
    let channels: [LiveChannel]
    let current: String
    let client: APIClient
    let tune: (String) -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: MaktabaTokens.Spacing.s1) {
            ForEach(around) { channel in
                let on = model.onNow(channel.id)
                let isCurrent = channel.id == current
                Button { tune(channel.id) } label: {
                    HStack(spacing: MaktabaTokens.Spacing.s2) {
                        Text(channel.number, format: .number)
                            .font(.maktabaBody(MaktabaTokens.TypeSize.sm, weight: .semibold))
                            .foregroundStyle(isCurrent ? Color.black : Color.mktBrassText)
                            .frame(minWidth: 28)
                        VStack(alignment: .leading, spacing: 0) {
                            Text(channel.name)
                                .font(.maktabaBody(MaktabaTokens.TypeSize.sm, weight: .semibold))
                                .lineLimit(1)
                            Text(verbatim: on?.current?.displayTitle() ?? " ")
                                .font(.maktabaBody(12))
                                .lineLimit(1)
                            if let next = on?.next {
                                LiveNextLine(block: next)
                            }
                        }
                        Spacer(minLength: 0)
                    }
                    .foregroundStyle(isCurrent ? Color.black : Color.white)
                    .padding(.horizontal, MaktabaTokens.Spacing.s2)
                    .padding(.vertical, 6)
                    .background(isCurrent ? Color.brass : .clear, in: .rect(cornerRadius: MaktabaTokens.Radius.md))
                    .contentShape(.rect)
                }
                .buttonStyle(.plain)
                .accessibilityElement(children: .combine)
                .accessibilityAddTraits(isCurrent ? .isSelected : [])
                .accessibilityIdentifier(isCurrent ? "live.miniguide.current" : "live.miniguide.\(channel.id)")
            }
        }
        .padding(MaktabaTokens.Spacing.s2)
        .background(.ultraThinMaterial, in: .rect(cornerRadius: MaktabaTokens.Radius.lg))
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("live.miniguide")
    }

    private var around: [LiveChannel] {
        guard !channels.isEmpty else { return [] }
        let n = min(5, channels.count)
        let i = channels.firstIndex { $0.id == current } ?? 0
        let first = i - n / 2
        return (0..<n).map { k in channels[((first + k) % channels.count + channels.count) % channels.count] }
    }
}

/// A tablet's number pad: the digits, then the channel tunes a moment after the last one.
struct LiveKeypad: View {
    let digit: (String) -> Void
    let done: () -> Void

    var body: some View {
        VStack(spacing: MaktabaTokens.Spacing.s3) {
            Grid(horizontalSpacing: MaktabaTokens.Spacing.s3, verticalSpacing: MaktabaTokens.Spacing.s3) {
                ForEach([[1, 2, 3], [4, 5, 6], [7, 8, 9]], id: \.self) { row in
                    GridRow { ForEach(row, id: \.self) { key($0) } }
                }
                GridRow {
                    Color.clear.frame(width: 64, height: 52)
                    key(0)
                    Button(action: done) {
                        Text("Done", tableName: "Live", comment: "Closes the number pad")
                            .frame(width: 64, height: 52)
                    }
                    .accessibilityIdentifier("live.keypad.done")
                }
            }
        }
        .padding(MaktabaTokens.Spacing.s4)
        .environment(\.layoutDirection, .leftToRight) // a keypad reads 1 2 3 in every language
        .accessibilityIdentifier("live.keypad")
    }

    private func key(_ n: Int) -> some View {
        Button { digit(String(n)) } label: {
            Text(n, format: .number)
                .font(.title)
                .frame(width: 64, height: 52)
                .background(Color.mktRaised, in: .rect(cornerRadius: MaktabaTokens.Radius.md))
        }
        .buttonStyle(.plain)
        .accessibilityIdentifier("live.keypad.\(n)")
    }
}

/// The picture alone (no system transport bar: live TV has nothing to scrub; the screen has
/// its own pause and Back to live).
struct LiveVideoLayer {
    let player: AVPlayer
    /// The layer once made (Picture in Picture draws from it).
    var onLayer: ((AVPlayerLayer) -> Void)?
}

#if os(macOS)
extension LiveVideoLayer: NSViewRepresentable {
    func makeNSView(context: Context) -> LayerView {
        let v = LayerView()
        v.playerLayer.player = player
        onLayer?(v.playerLayer)
        return v
    }

    func updateNSView(_ v: LayerView, context: Context) {
        if v.playerLayer.player !== player { v.playerLayer.player = player }
    }

    final class LayerView: NSView {
        let playerLayer = AVPlayerLayer()
        override init(frame: NSRect) {
            super.init(frame: frame)
            wantsLayer = true
            playerLayer.videoGravity = .resizeAspect
            layer = playerLayer
        }

        required init?(coder: NSCoder) { nil }
    }
}
#else
extension LiveVideoLayer: UIViewRepresentable {
    func makeUIView(context: Context) -> LayerView {
        let v = LayerView()
        v.playerLayer.player = player
        v.playerLayer.videoGravity = .resizeAspect
        v.isUserInteractionEnabled = false
        onLayer?(v.playerLayer)
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
#endif
