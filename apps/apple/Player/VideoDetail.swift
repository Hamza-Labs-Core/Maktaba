import MaktabaKit
import SwiftUI

/// A video's page: its poster (tap to play), title and description. Playing hands the video
/// to the app's Playback; where it shows (full screen, or the other half of a half-folded
/// iPhone Duo) is LibraryBrowser's call.
struct VideoDetail: View {
    let client: APIClient
    let video: Video
    @Environment(Playback.self) private var playback

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                Button { playback.play(video) } label: {
                    AuthorizedImage(client: client, video: video.id)
                        .overlay {
                            Image(systemName: "play.circle.fill")
                                .font(.system(size: 64))
                                .foregroundStyle(.white, Color.brass)
                                .shadow(radius: 8)
                        }
                        .clipShape(.rect(cornerRadius: 12))
                }
                .buttonStyle(.plain)
                .accessibilityLabel("Play \(video.displayTitle)")

                Text(video.displayTitle)
                    .font(.title2.bold())
                    .multilineTextAlignment(.leading)
                if let d = video.description, !d.isEmpty {
                    Text(d).foregroundStyle(.secondary)
                }
            }
            .padding()
            .frame(maxWidth: 900, alignment: .leading)
        }
        .maktabaSurface()
        .navigationTitle(video.displayTitle)
        #if os(iOS)
        .navigationBarTitleDisplayMode(.inline)
        #endif
        .toolbar {
            ToolbarItem(placement: .primaryAction) {
                Button("Play", systemImage: "play.fill") { playback.play(video) }
            }
        }
    }
}
