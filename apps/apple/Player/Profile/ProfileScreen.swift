import SwiftUI

/// The Profile section's first screen: a placeholder until story 48.22 builds it (this file
/// is that story's). Shown only when `PlayerSection.enabled` lists the section.
struct ProfileScreen: View {
    let context: PlayerContext

    var body: some View {
        ShellPlaceholder(title: PlayerSection.profile.title, systemImage: PlayerSection.profile.systemImage, id: "screen.profile")
    }
}
