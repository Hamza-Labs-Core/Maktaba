import SwiftUI
import AVKit

/// Full-screen HLS player. tvOS ships `VideoPlayer` (a SwiftUI wrapper
/// over `AVPlayerViewController`) which gives us the native transport
/// bar, scrubbing, subtitle/audio selection, and Siri-remote gestures
/// for free — re-implementing those for a 10-foot UI would be a mistake.
struct PlayerView: View {
    let media: Media

    @Environment(\.api) private var api
    @State private var player: AVPlayer?

    var body: some View {
        VideoPlayer(player: player)
            .ignoresSafeArea()
            .task { await start() }
            .onDisappear { player?.pause() }
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
        player.play()
    }
}
