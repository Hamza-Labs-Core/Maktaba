import SwiftUI

extension View {
    /// A sign-in form's focused field kept in sight above the keyboard: each time the focus
    /// moves (Return's Next, a tap), the form scrolls the field's row (its `.id(field)`) to the
    /// middle, and again once the keyboard has come up, since the keyboard can still be
    /// sliding in (and shrinking the form) when the focus changes.
    func keepsFocusInView<Field: Hashable>(_ focus: Field?, _ rows: ScrollViewProxy) -> some View {
        onChange(of: focus) { _, field in
            guard let field else { return }
            withAnimation { rows.scrollTo(field, anchor: .center) }
            Task { @MainActor in
                try? await Task.sleep(for: .milliseconds(350))
                withAnimation { rows.scrollTo(field, anchor: .center) }
            }
        }
    }
}
