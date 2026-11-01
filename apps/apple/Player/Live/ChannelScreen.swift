import SwiftUI

/// A channel's live player (`PlayerRoute.channel`): a placeholder until story 48.21 builds it (this file is
/// that story's).
struct ChannelScreen: View {
    let context: PlayerContext
    let id: String

    var body: some View {
        ShellPlaceholder(title: LocalizedStringResource("Channel", table: "Shell", comment: "A channel's page, not built yet"),
                         systemImage: "tv", id: "screen.channel", detail: id)
    }
}
