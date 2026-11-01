import SwiftUI

/// The Search section's first screen: a placeholder until story 48.20 builds it (this file
/// is that story's). Shown only when `PlayerSection.enabled` lists the section.
struct SearchScreen: View {
    let context: PlayerContext

    var body: some View {
        ShellPlaceholder(title: PlayerSection.search.title, systemImage: PlayerSection.search.systemImage, id: "screen.search")
    }
}
