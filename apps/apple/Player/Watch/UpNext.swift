import MaktabaKit
import SwiftUI

/// Up next, in an episode's last seconds: the next episode's title and a countdown, with
/// Play now and Cancel. At zero the next episode plays (`Playback.checkUpNext`); with
/// Settings' autoplay off it never shows and the video stops at its end.
struct UpNextCard: View {
    let episode: Episode
    let secondsLeft: Int
    let playNow: () -> Void
    let cancel: () -> Void

    var body: some View {
        VStack {
            Spacer()
            HStack {
                Spacer()
                VStack(alignment: .leading, spacing: 8) {
                    Text("Up next in \(secondsLeft) s", tableName: "Watch", comment: "Up next's countdown, in seconds")
                        .font(.caption.bold())
                        .foregroundStyle(.secondary)
                        .accessibilityIdentifier("player.upNext.countdown")
                    Text(verbatim: episode.title)
                        .font(.headline)
                        .lineLimit(2)
                    HStack {
                        Button(action: playNow) {
                            Text("Play now", tableName: "Watch", comment: "Up next: plays the next episode at once")
                        }
                        .buttonStyle(.borderedProminent)
                        .accessibilityIdentifier("player.upNext.playNow")
                        Button(action: cancel) {
                            Text("Cancel", tableName: "Watch", comment: "Up next: stays on this video")
                        }
                        .buttonStyle(.bordered)
                        .accessibilityIdentifier("player.upNext.cancel")
                    }
                }
                .padding()
                .frame(maxWidth: 320, alignment: .leading)
                .background(.ultraThinMaterial, in: .rect(cornerRadius: 16))
                .accessibilityElement(children: .contain)
                .accessibilityIdentifier("player.upNext")
            }
        }
        .padding(.horizontal)
        .padding(.bottom, 96)
        .foregroundStyle(.white)
    }
}
