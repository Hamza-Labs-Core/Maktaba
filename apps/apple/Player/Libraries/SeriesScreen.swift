import SwiftUI

/// A series' page (`PlayerRoute.series`): a placeholder until story 48.18 builds it (this file is
/// that story's).
struct SeriesScreen: View {
    let context: PlayerContext
    let id: String

    var body: some View {
        ShellPlaceholder(title: LocalizedStringResource("Series", table: "Shell", comment: "A series' page, not built yet"),
                         systemImage: "rectangle.stack", id: "screen.series", detail: id)
    }
}
