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

    var body: some View {
        ZStack {
            Color.black.ignoresSafeArea()
            if let playback {
                WatchView(playback: playback, session: context.session) { dismiss() }
                    .focusedSceneValue(\.playback, playback)
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
        .onDisappear { playback?.stop() }
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
