import SwiftUI

/// The Live TV section's first screen: a placeholder until story 48.21 builds it (this file
/// is that story's). Shown only when `PlayerSection.enabled` lists the section.
struct LiveTVScreen: View {
    let context: PlayerContext

    var body: some View {
        ShellPlaceholder(title: PlayerSection.liveTV.title, systemImage: PlayerSection.liveTV.systemImage, id: "screen.liveTV")
    }
}
