import MaktabaKit
import SwiftUI

/// Update the home or Update this app on Apple TV (Epic 58 story 58.01), instead of signing
/// in: what to do, then Try again (focused first) or another home. The words are MaktabaKit's,
/// as on the phones (`CompatBlock.text`).
struct TVCompatView: View {
    let block: CompatBlock
    let tryAgain: () -> Void
    let otherHome: () -> Void
    private let words = ConnectStrings.forPreferredLanguages()
    @FocusState private var tryAgainFocused: Bool

    var body: some View {
        let text = block.text(words)
        let errors = ErrorStrings(words.language)
        ZStack {
            LibraryBackdrop()
            VStack(alignment: .leading, spacing: 28) {
                SectionTitle(text.title, size: 52)
                    .accessibilityAddTraits(.isHeader)
                    .accessibilityIdentifier("compat.title")
                ForEach(Array(text.lines.enumerated()), id: \.offset) { i, line in
                    Text(line)
                        .font(Theme.body(30))
                        .foregroundStyle(Theme.muted)
                        .fixedSize(horizontal: false, vertical: true)
                        .accessibilityIdentifier("compat.line.\(i)")
                }
                HStack(spacing: 32) {
                    Button(errors.text(.tryAgain), action: tryAgain)
                        .buttonStyle(.gold)
                        .focused($tryAgainFocused)
                        .accessibilityIdentifier("compat.tryAgain")
                    Button(errors.text(.chooseAnotherHome), action: otherHome)
                        .accessibilityIdentifier("compat.otherHome")
                }
                .padding(.top, 8)
            }
            .frame(maxWidth: 1200)
            .readingPanel(padding: 56)
            .padding(80)
        }
        .onAppear { tryAgainFocused = true }
        .environment(\.layoutDirection, words.isRightToLeft ? .rightToLeft : .leftToRight)
        .accessibilityIdentifier("compat.page")
    }
}
