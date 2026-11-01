import SwiftUI

/// A video's page (`PlayerRoute.video`): a placeholder until story 48.19 builds it (this file is
/// that story's).
struct VideoScreen: View {
    let context: PlayerContext
    let id: String

    var body: some View {
        ShellPlaceholder(title: LocalizedStringResource("Video", table: "Shell", comment: "A video's page, not built yet"),
                         systemImage: "play.rectangle", id: "screen.video", detail: id)
    }
}
