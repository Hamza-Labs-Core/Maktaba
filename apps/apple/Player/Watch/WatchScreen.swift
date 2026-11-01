import SwiftUI

/// The player (`PlayerRoute.watch`): a placeholder until story 48.23 builds it (this file is
/// that story's).
struct WatchScreen: View {
    let context: PlayerContext
    let videoID: String
    /// Seconds into the video; nil: where the person left off.
    let at: Double?

    var body: some View {
        ShellPlaceholder(title: LocalizedStringResource("Player", table: "Shell", comment: "The video player, not built yet"),
                         systemImage: "play.circle", id: "screen.watch", detail: at.map { "\(videoID) @ \(Int($0))" } ?? videoID)
    }
}
