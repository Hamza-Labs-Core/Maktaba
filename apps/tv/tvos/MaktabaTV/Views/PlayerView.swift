import SwiftUI
import AVKit

/// Full-screen HLS player. tvOS ships `VideoPlayer` (a SwiftUI wrapper
/// over `AVPlayerViewController`) which gives us the native transport
/// bar, scrubbing, subtitle/audio selection, and Siri-remote gestures
/// for free — re-implementing those for a 10-foot UI would be a mistake.
///
/// On top of playback this view drives the watch-analytics lifecycle
/// (Story 29.1): it opens a session when playback starts, beats every
/// 30 s via an `AVPlayer` periodic time observer, and closes the session
/// on playback-end or when the view disappears.
struct PlayerView: View {
    let media: Media

    @Environment(\.api) private var api
    @State private var player: AVPlayer?
    /// Mutable, reference-typed watch state so the escaping time-observer
    /// closure sees live values across SwiftUI re-renders (a `@State`
    /// struct field would be captured by value at install time).
    @State private var watch = WatchState()

    var body: some View {
        VideoPlayer(player: player)
            .ignoresSafeArea()
            .task { await start() }
            .onDisappear {
                stopWatch()
                player?.pause()
            }
            .onReceive(NotificationCenter.default.publisher(for: .AVPlayerItemDidPlayToEndTime)) { _ in
                stopWatch()
            }
    }

    private func start() async {
        // Prefer a server-provided absolute manifest; otherwise derive
        // the signed HLS URL from the video id.
        let manifest: URL
        if let hls = media.hlsURL, let url = URL(string: hls) {
            manifest = url
        } else {
            manifest = await api.hlsURL(for: media.id)
        }

        let item = AVPlayerItem(url: manifest)
        let player = AVPlayer(playerItem: item)
        self.player = player

        // Open the watch session. A nil id means tracking is paused —
        // we then skip heartbeats entirely.
        if let sessionID = try? await api.watchStart(videoID: media.id, quality: "auto") {
            watch.sessionID = sessionID
            // Capture only Sendable values (the id string + the actor) in
            // the @Sendable observer closure — never the WatchState box.
            // The id is fixed for the session's life and the observer is
            // detached in stopWatch(), so a late beat at most 409s.
            let apiRef = api
            let interval = CMTime(seconds: 30, preferredTimescale: 1)
            watch.observer = player.addPeriodicTimeObserver(
                forInterval: interval, queue: .main
            ) { time in
                guard time.seconds.isFinite else { return }
                let pos = Int(max(0, time.seconds.rounded(.down)))
                Task { try? await apiRef.watchHeartbeat(sessionID: sessionID, positionSec: pos) }
            }
        }

        player.play()
    }

    /// Close the session once: detach the observer and POST a final
    /// position. Idempotent so onDisappear-after-end is harmless.
    private func stopWatch() {
        guard let id = watch.sessionID else { return }
        watch.sessionID = nil
        if let obs = watch.observer {
            player?.removeTimeObserver(obs)
            watch.observer = nil
        }
        let secs = player?.currentTime().seconds ?? 0
        let pos = Int(max(0, (secs.isFinite ? secs : 0).rounded(.down)))
        let apiRef = api
        Task { try? await apiRef.watchStop(sessionID: id, positionSec: pos) }
    }
}

/// Reference box for the watch-session state shared with the periodic
/// time-observer closure.
private final class WatchState {
    var sessionID: String?
    var observer: Any?
}
