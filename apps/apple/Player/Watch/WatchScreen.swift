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
    @Environment(\.dismiss) private var dismiss
    /// The Mac's window-wide player: there the sidebar is beside the stack, so the picture is
    /// shown over the whole window by the shell and this screen keeps only its black.
    @Environment(WindowPlayer.self) private var window: WindowPlayer?

    var body: some View {
        ZStack {
            Color.black.ignoresSafeArea()
            if let playback {
                if window == nil {
                    WatchView(playback: playback, session: context.session) { dismiss() }
                        .focusedSceneValue(\.playback, playback)
                } else {
                    Color.clear.focusedSceneValue(\.playback, playback)
                }
            } else if let failure {
                HomeErrorView(failure) { action in
                    switch action {
                    case .tryAgain: Task { await load() }
                    case .back: dismiss()
                    case .signInAgain: context.session.signedOut()
                    case .chooseAnotherHome: context.session.switchHome()
                    }
                }
                .environment(\.colorScheme, .dark)
                .accessibilityIdentifier("player.error")
            } else {
                ProgressView().tint(.white)
            }
        }
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("screen.watch")
        .navigationBarBackButtonHidden()
        #if os(iOS)
        .toolbar(.hidden, for: .navigationBar, .tabBar)
        #else
        .toolbar(.hidden, for: .windowToolbar)
        #endif
        .task(id: videoID) { await load() }
        .onChange(of: playback.map(ObjectIdentifier.init), initial: true) {
            guard let window, let playback else { return }
            window.show(playback) { dismiss() }
        }
        .onDisappear {
            if let playback { window?.hide(playback) }
            playback?.stop()
        }
    }

    private func load() async {
        failure = nil
        do {
            let video = try await context.client.video(videoID)
            let p = playback ?? Playback(client: context.client)
            playback = p
            p.play(video, from: at)
        } catch is CancellationError {
            return
        } catch {
            failure = HomeFailure(error)
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
