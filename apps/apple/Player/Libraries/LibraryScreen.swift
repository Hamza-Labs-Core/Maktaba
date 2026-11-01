import SwiftUI

/// A library's page (`PlayerRoute.library`): a placeholder until story 48.18 builds it (this file is
/// that story's).
struct LibraryScreen: View {
    let context: PlayerContext
    let id: String

    var body: some View {
        ShellPlaceholder(title: LocalizedStringResource("Library", table: "Shell", comment: "A library's page, not built yet"),
                         systemImage: "books.vertical", id: "screen.library", detail: id)
    }
}
