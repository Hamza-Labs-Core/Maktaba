import SwiftUI

/// A line of words beside a symbol that only decorates them: a plain informational row.
///
/// Built as a bare `Label`, such a row gives accessibility two things, the symbol and the
/// words, and an identifier set on it lands on both. Some OS versions then read the symbol
/// first, by its system name ("person.2.badge.gearshape"), to VoiceOver and to a UI test.
/// Here the symbol is hidden from accessibility and the row is one element whose label is
/// its words; an identifier set on an `IconLine` names that one element.
public struct IconLine: View {
    private let text: String
    private let systemImage: String

    public init(_ text: String, systemImage: String) {
        self.text = text
        self.systemImage = systemImage
    }

    public var body: some View {
        Label {
            Text(verbatim: text)
        } icon: {
            Image(systemName: systemImage).accessibilityHidden(true)
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(Text(verbatim: text))
    }
}
