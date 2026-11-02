import MaktabaKit
import SwiftUI

/// The video with the Player's own controls (story 48.23): full screen, the Mac's whole
/// window, or one half of a half-folded iPhone Duo.
/// - A tap shows or hides the controls; they hide 4 s after the last touch while it plays.
/// - A double-tap seeks 10 s: back on the start side, forward on the end side (the sides
///   mirror in Arabic, as the layout does).
/// - Up next counts down over the video in an episode's last seconds.
/// - Esc closes (`onClose`, else stopping the playback, which closes the cover).
struct WatchView: View {
    let playback: Playback
    let session: AppSession
    /// What Close does besides stopping (the watch route goes back).
    var onClose: (() -> Void)?

    @State private var controls = true
    @State private var hideTask: Task<Void, Never>?
    @State private var sheet: WatchSheet?
    @State private var feedback: SeekFeedback?

    var body: some View {
        ZStack {
            Color.black.ignoresSafeArea()
            if let player = playback.player {
                PlayerSurface(player: player, showsControls: false)
                    .ignoresSafeArea()
                    .accessibilityHidden(true)
                tapZones
                if let feedback { SeekFeedbackView(feedback: feedback) }
                if controls {
                    WatchControls(playback: playback, close: close, touched: touched, open: { sheet = $0 })
                        .transition(.opacity)
                }
                if let left = playback.upNextCountdown, let next = playback.next {
                    UpNextCard(episode: next, secondsLeft: left, playNow: playback.playNext, cancel: playback.cancelUpNext)
                }
            } else {
                Group {
                    if let failure = playback.failure {
                        HomeErrorView(failure) { act($0) }
                            .accessibilityIdentifier("player.error")
                    } else {
                        ProgressView().tint(.white)
                    }
                }
                VStack {
                    HStack {
                        CloseButton(action: close)
                        Spacer()
                    }
                    Spacer()
                }
                .padding()
            }
        }
        .background(.black)
        .environment(\.colorScheme, .dark)
        .animation(.easeInOut(duration: 0.2), value: controls)
        .sheet(item: $sheet, onDismiss: touched) { s in
            switch s {
            case .tracks: TracksSheet(playback: playback)
            case .quality: QualitySheet(playback: playback)
            }
        }
        #if os(iOS)
        .statusBarHidden(!controls)
        .persistentSystemOverlays(controls ? .automatic : .hidden)
        #endif
        .onAppear(perform: touched)
        .onChange(of: playback.isPlaying) { touched() }
        .onDisappear { hideTask?.cancel() }
    }

    /// The two halves of the video: a tap toggles the controls, a double-tap seeks. The
    /// first half is the start side (left in English, right in Arabic).
    private var tapZones: some View {
        HStack(spacing: 0) {
            zone(startSide: true)
            zone(startSide: false)
        }
        .ignoresSafeArea()
        .accessibilityElement()
        .accessibilityLabel(Text("Video", tableName: "Watch", comment: "The video picture, for VoiceOver"))
        .accessibilityIdentifier("player.surface")
        #if DEBUG
        .accessibilityValue(Text(verbatim: playback.testState))
        #endif
        .accessibilityAction(named: Text("Back 10 seconds", tableName: "Watch", comment: "Seeks 10 seconds back")) { playback.skip(by: -10) }
        .accessibilityAction(named: Text("Forward 10 seconds", tableName: "Watch", comment: "Seeks 10 seconds forward")) { playback.skip(by: 10) }
    }

    private func zone(startSide: Bool) -> some View {
        Color.clear
            .contentShape(Rectangle())
            .gesture(
                TapGesture(count: 2).onEnded { doubleTap(startSide: startSide) }
                    .exclusively(before: TapGesture().onEnded { toggleControls() })
            )
    }

    private func doubleTap(startSide: Bool) {
        let seconds = WatchLogic.doubleTapSeek(onStartSide: startSide)
        playback.skip(by: seconds)
        let shown = SeekFeedback(startSide: startSide)
        feedback = shown
        Task {
            try? await Task.sleep(for: .milliseconds(900))
            if feedback == shown { feedback = nil }
        }
    }

    private func toggleControls() {
        controls.toggle()
        touched()
    }

    /// A touch: the controls stay 4 s more (while it plays and no sheet is open).
    private func touched() {
        hideTask?.cancel()
        guard controls, playback.isPlaying, sheet == nil else { return }
        hideTask = Task {
            try? await Task.sleep(for: WatchLogic.controlsHideAfter)
            guard !Task.isCancelled, playback.isPlaying, sheet == nil else { return }
            controls = false
        }
    }

    private func close() {
        playback.stop()
        onClose?()
    }

    private func act(_ a: ErrorAction) {
        switch a {
        case .tryAgain: playback.retry()
        case .back: close()
        case .signInAgain:
            close()
            session.signedOut()
        case .chooseAnotherHome:
            close()
            session.switchHome()
        }
    }
}

enum WatchSheet: String, Identifiable {
    case tracks, quality
    var id: String { rawValue }
}

/// A double-tap's mark on its side, for a moment.
struct SeekFeedback: Equatable {
    let startSide: Bool
    let id = UUID()
}

private struct SeekFeedbackView: View {
    let feedback: SeekFeedback

    var body: some View {
        HStack {
            if !feedback.startSide { Spacer() }
            Label {
                if feedback.startSide {
                    Text("Back 10 seconds", tableName: "Watch", comment: "Seeks 10 seconds back")
                } else {
                    Text("Forward 10 seconds", tableName: "Watch", comment: "Seeks 10 seconds forward")
                }
            } icon: {
                Image(systemName: feedback.startSide ? "gobackward.10" : "goforward.10")
            }
            .labelStyle(.iconOnly)
            .font(.largeTitle)
            .foregroundStyle(.white)
            .padding(24)
            .background(.black.opacity(0.4), in: .circle)
            .accessibilityIdentifier("player.seekFeedback")
            .padding(.horizontal, 48)
            if feedback.startSide { Spacer() }
        }
        .allowsHitTesting(false)
        .transition(.opacity)
    }
}

struct CloseButton: View {
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            Label { Text("Close", tableName: "Watch", comment: "Closes the player") } icon: { Image(systemName: "xmark") }
        }
        .keyboardShortcut(.cancelAction)
        .labelStyle(.iconOnly)
        .font(.title2)
        .foregroundStyle(.white)
        .padding(12)
        .background(.ultraThinMaterial, in: .circle)
        .buttonStyle(.plain)
        .accessibilityIdentifier("player.close")
    }
}
