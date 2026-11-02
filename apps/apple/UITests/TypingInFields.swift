import XCTest

extension XCUIElement {
    /// Types `text` into this text field so that it holds exactly `text`, checked by reading
    /// the field back before the caller goes on.
    ///
    /// `typeText` can lose keystrokes: on CI an iPad's picker took "h" of the mock home's
    /// address and dropped the rest (its recording shows "h", then an empty field), and
    /// Connect then tried `https://h`. So the field is read after typing; when it holds
    /// something else, the test waits for the field to have the keyboard, clears it, and types
    /// again, three times at most, then fails with what the field holds.
    func typeExactly(_ text: String, in app: XCUIApplication, file: StaticString = #filePath, line: UInt = #line) {
        var holds = ""
        for attempt in 1 ... 3 {
            if attempt > 1 || !hasKeyboardFocus {
                tap()
                _ = app.keyboards.firstMatch.waitForExistence(timeout: 5)
                let end = Date().addingTimeInterval(5)
                while !hasKeyboardFocus, Date() < end { RunLoop.current.run(until: Date().addingTimeInterval(0.2)) }
            }
            let old = typedValue
            if !old.isEmpty { typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: old.count)) }
            typeText(text)
            holds = typedValue
            if holds == text { return }
        }
        XCTFail("the field holds \"\(holds)\", not \"\(text)\"", file: file, line: line)
    }

    private var hasKeyboardFocus: Bool { (value(forKey: "hasKeyboardFocus") as? Bool) ?? false }

    /// What was typed in the field: its value, unless that is the placeholder an empty field
    /// reports as its value.
    private var typedValue: String {
        let v = value as? String ?? ""
        return v == placeholderValue ? "" : v
    }
}
