import SwiftUI

/// Update the home or Update this app (Epic 58 story 58.01), shown by `SessionGate` instead of
/// signing in when this app and the home in use are too far apart: what to do, then Try again
/// (asks the home again) or another home.
public struct CompatView: View {
    let block: CompatBlock
    let tryAgain: () -> Void
    let otherHome: () -> Void
    private let words: ConnectStrings
    private let errors: ErrorStrings

    public init(block: CompatBlock, words: ConnectStrings = .forPreferredLanguages(),
                tryAgain: @escaping () -> Void, otherHome: @escaping () -> Void) {
        self.block = block
        self.words = words
        errors = ErrorStrings(words.language)
        self.tryAgain = tryAgain
        self.otherHome = otherHome
    }

    public var body: some View {
        let text = block.text(words)
        NavigationStack {
            Form {
                StartBrandSection(appName: MaktabaApp.current?.name())
                Section {
                    Text(text.title)
                        .font(.title2.weight(.semibold))
                        .accessibilityAddTraits(.isHeader)
                        .accessibilityIdentifier("compat.title")
                    ForEach(Array(text.lines.enumerated()), id: \.offset) { i, line in
                        Text(line)
                            .foregroundStyle(.secondary)
                            .fixedSize(horizontal: false, vertical: true)
                            .accessibilityIdentifier("compat.line.\(i)")
                    }
                    Button(errors.text(.tryAgain), action: tryAgain)
                        .accessibilityIdentifier("compat.tryAgain")
                    Button(errors.text(.chooseAnotherHome), action: otherHome)
                        .accessibilityIdentifier("compat.otherHome")
                }
                .startPanelRows()
            }
            .formStyle(.grouped)
            .startScreen()
        }
        .environment(\.layoutDirection, words.isRightToLeft ? .rightToLeft : .leftToRight)
    }
}
