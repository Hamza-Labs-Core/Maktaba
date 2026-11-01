import SwiftUI

/// The Home section's first screen: a placeholder until story 48.17 builds it (this file
/// is that story's). Shown only when `PlayerSection.enabled` lists the section.
struct HomeScreen: View {
    let context: PlayerContext

    var body: some View {
        ShellPlaceholder(title: PlayerSection.home.title, systemImage: PlayerSection.home.systemImage, id: "screen.home")
    }
}
