/// A screen change held back until a text field has let go of the keyboard.
///
/// A field that still holds the keyboard when the screen under it changes can have the
/// keyboard's hide animation cut off. That animation then never finishes, and the app never
/// counts as idle again: every XCUITest wait for idle runs its full 60 s (Trail #1419 for the
/// sign-in form, and the search field after Return). The screen drops the focus first, and the
/// change runs only once the field reports it let go. When the field held nothing, the change
/// runs at once.
///
/// ```swift
/// @FocusState private var typing: Bool
/// @State private var letGo = LetGoFirst<String>()
///
/// func submit(_ q: String) {
///     if let now = letGo.begin(q, focused: typing) { run(now) } else { typing = false }
/// }
/// // .onChange(of: typing) { _, now in if let q = letGo.focusChanged(now) { run(q) } }
/// ```
public struct LetGoFirst<Change> {
    /// The change waiting for the field to let go; nil when none is waiting.
    public private(set) var held: Change?

    public init() {}

    /// The change to run now, or nil when it is held. It is nil when `focused` (the field
    /// holds the keyboard): the caller then drops the focus, and `focusChanged` hands the change
    /// back. A newer change replaces an older one still held, so only the latest runs.
    public mutating func begin(_ change: Change, focused: Bool) -> Change? {
        guard focused else {
            held = nil
            return change
        }
        held = change
        return nil
    }

    /// The field's focus changed. Returns the held change once the field has let go, and only
    /// once. Gaining focus again hands back nothing.
    public mutating func focusChanged(_ focused: Bool) -> Change? {
        guard !focused, let change = held else { return nil }
        held = nil
        return change
    }
}
