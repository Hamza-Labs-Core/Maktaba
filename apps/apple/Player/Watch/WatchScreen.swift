import MaktabaKit
import SwiftUI

/// The player (`PlayerRoute.watch`): the video full screen over its section, at the time asked
/// for, else where the person left off (a finished video starts again). The tab bar and the
/// navigation bar hide while it is open; Close (or Esc) goes back to the screen that opened it.
struct WatchScreen: View {
    let context: PlayerContext
    let videoID: String
    /// Seconds into the video; nil: where the person left off.
    let at: Double?

    @State private var playback: Playback?
    @State private var failure: HomeFailure?
    /// The home refused the video itself for the account's time (403 `parental-time`).
    @State private var timeStop: TimeUp?
    /// The home cannot read the account's watching time right now (503
    /// `parental_time_unknown`): a calm line while the video is asked for again, no error.
    @State private var checkingTime: HomeFailure?
    @Environment(\.scenePhase) private var scenePhase
    /// The home's status as the shell's strip read it (Epic 53 story 53.06)
    @Environment(\.homeStatus) private var homeStatus
    @Environment(\.dismiss) private var dismiss
    /// The Mac's window-wide player: there the sidebar is beside the stack, so the picture is
    /// shown over the whole window by the shell and this screen keeps only its black.
    @Environment(WindowPlayer.self) private var window: WindowPlayer?
    /// Casting (story 58.06): nil where this build cannot cast.
    @Environment(CastController.self) private var cast: CastController?

    // The body is in small typed pieces: as one expression it took the compiler too long to
    // type-check on CI ("unable to type-check this expression in reasonable time").
    var body: some View {
        screen
            .task(id: videoID) { await load() }
            .task(id: videoID) { await cast?.readReach(context.client) }
            // story 57.09: leaving the front sends the position at once; in the background a
            // sign-in under a parental limit pauses and does not start again by itself
            .onChange(of: scenePhase) { _, phase in sceneChanged(phase) }
            .onChange(of: playbackID, initial: true) {
                showInWindow()
                followCast()
            }
            .onAppear { cast?.watchShowing += 1 }
            .onChange(of: isStopped) { wasStopped, stopped in
                // Playback ▸ Stop (⌘.) stops the video: the player closes with it, as Close does,
                // rather than staying open on a stopped picture (`play` swaps a video in one step,
                // so the next video never shows as stopped here)
                if !wasStopped, stopped { dismiss() }
            }
            .onDisappear {
                cast?.watchShowing -= 1
                closed()
            }
    }

    /// The screen without what it reacts to.
    private var screen: some View {
        ZStack {
            Color.black.ignoresSafeArea()
            content
        }
        .modifier(CastSheetsIfAny(cast: cast, playback: playback))
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("screen.watch")
        .navigationBarBackButtonHidden()
        #if os(iOS)
        .toolbar(.hidden, for: .navigationBar, .tabBar)
        #else
        .toolbar(.hidden, for: .windowToolbar)
        #endif
    }

    /// Under a parental limit, a video the home does not give (a hidden one answers as a
    /// missing one).
    private var isBlocked: Bool {
        guard let failure else { return false }
        return failure.kind == .notFound && context.session.parental.current.limited
    }

    private var playbackID: ObjectIdentifier? { playback.map(ObjectIdentifier.init) }
    private var isStopped: Bool { playback?.video == nil }

    @ViewBuilder private var content: some View {
        if isBlocked {
            // Epic 57 story 57.05: a link, Continue or a stale queue item to a video the
            // home does not give, under a parental limit: the blocked page, not an error
            BlockedPageView(context: context, subject: .video(videoID), back: { dismiss() }) { Task { await load() } }
                .environment(\.colorScheme, .dark)
        } else if failure != nil, let timeStop {
            TimeUpView(context: context, up: timeStop, back: { dismiss() }) { Task { await load() } }
                .environment(\.colorScheme, .dark)
        } else if let playback {
            player(playback)
        } else if let failure {
            failed(failure)
        } else {
            starting
        }
    }

    /// Before the video is known: a spinner, with the home's calm line while it cannot read
    /// the account's watching time yet.
    private var starting: some View {
        VStack(spacing: 16) {
            ProgressView().tint(.white)
            if let checkingTime {
                Text(verbatim: checkingTime.message(video: true))
                    .foregroundStyle(.white)
                    .multilineTextAlignment(.center)
                    .accessibilityIdentifier("player.waiting")
            }
        }
        .padding(32)
    }

    @ViewBuilder private func player(_ playback: Playback) -> some View {
        if let cast, cast.isCasting, playback.castActive {
            // the TV plays it: its panel and the mini controller, not the picture
            CastPanel(cast: cast)
                .environment(\.colorScheme, .dark)
        } else if window == nil {
            WatchView(playback: playback, session: context.session, context: context) { dismiss() }
                .focusedSceneValue(\.playback, playback)
        } else {
            Color.clear.focusedSceneValue(\.playback, playback)
        }
    }

    @ViewBuilder private func failed(_ failure: HomeFailure) -> some View {
        // streaming is down on the home: that, not a generic error (story 53.06)
        if StatusStripRules.playbackDown(homeStatus) {
            Text(verbatim: StripWords.localized(StripWords.player))
                .foregroundStyle(.white)
                .padding()
                .accessibilityIdentifier("player.unavailable")
        }
        HomeErrorView(failure) { act($0) }
            .environment(\.colorScheme, .dark)
            .accessibilityIdentifier("player.error")
    }

    private func act(_ action: ErrorAction) {
        switch action {
        case .tryAgain: Task { await load() }
        case .back: dismiss()
        case .signInAgain: context.session.signedOut()
        case .chooseAnotherHome: context.session.switchHome()
        }
    }

    private func sceneChanged(_ phase: ScenePhase) {
        playback?.sceneChanged(active: phase == .active, background: phase == .background)
    }

    private func showInWindow() {
        guard let window, let playback else { return }
        window.show(playback) { dismiss() }
    }

    /// The cast takes this screen's video from the phone and gives it back (story 58.06).
    private func followCast() {
        guard let cast, let playback else { return }
        cast.handOver = { [weak playback] id in
            guard let playback, playback.video?.id == id else { return 0 }
            return playback.handOverToCast()
        }
        cast.giveBack = { [weak playback] id, at in
            guard let playback, playback.video?.id == id else { return }
            playback.returnFromCast(at: at)
        }
    }

    private func closed() {
        if let playback { window?.hide(playback) }
        playback?.stop()
    }

    private func load() async {
        failure = nil
        timeStop = nil
        do {
            let video = try await context.client.video(videoID)
            let p = playback ?? Playback(client: context.client)
            let parental = context.session.parental
            p.limited = { parental.current.limited }
            Log.ui.info("watch: \(playback == nil ? "opens" : "loads again", privacy: .public) \(videoID, privacy: .public)")
            playback = p
            checkingTime = nil
            p.play(video, from: at)
            // the TV already plays this video (the screen opened again while casting): the
            // phone follows it and posts no progress of its own
            if let cast, cast.isCasting, cast.castVideoID == videoID { _ = p.handOverToCast() }
        } catch is CancellationError {
            return
        } catch {
            let f = HomeFailure(error)
            if let after = f.kind.timeUnknownRetrySeconds {
                // not an error and not time's up: asked again as the home's Retry-After says
                checkingTime = f
                do { try await Task.sleep(for: .seconds(after)) } catch { return }
                await load()
                return
            }
            timeStop = TimeUp(error)
            failure = f
        }
    }
}

/// The Mac's player over the whole window (story 48.25): the shell's sidebar sits beside each
/// section's stack, so a pushed player would leave it showing. The watch screen hands its
/// playback here and the shell draws it over everything; Close goes back as on any device.
@MainActor @Observable
final class WindowPlayer {
    private(set) var playback: Playback?
    private(set) var close: (() -> Void)?

    func show(_ playback: Playback, close: @escaping () -> Void) {
        self.playback = playback
        self.close = close
    }

    /// Clears the player only if it is still `playback` (a newer one may have taken its place).
    func hide(_ playback: Playback) {
        guard self.playback === playback else { return }
        self.playback = nil
        close = nil
    }
}

/// The Cast screens for the watch screen, when this build casts.
private struct CastSheetsIfAny: ViewModifier {
    let cast: CastController?
    let playback: Playback?

    func body(content: Content) -> some View {
        if let cast {
            content.castSheets(cast, player: playback?.player, video: { playback?.video }, at: { playback?.position ?? 0 })
        } else {
            content
        }
    }
}
