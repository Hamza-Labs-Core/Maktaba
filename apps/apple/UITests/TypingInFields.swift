import XCTest

/// Every UI test types through `typeExactly`: a raw `typeText` outside this file fails
/// `tools/apple/check-ui-typing.py` (CI), except `typeText("\n")`, the key that submits a field.
extension XCUIElement {
    /// Types `text` into this text field so that it holds exactly `text`, checked by reading
    /// the field back before the caller goes on. `secure`: a password field, whose value is
    /// masked and does not keep the text's length (iOS 26 reads "••" after 16 characters),
    /// so it is checked to hold something, not the empty field's placeholder.
    ///
    /// `typeText` can lose keystrokes: on CI an iPad's picker took "h" of the mock home's
    /// address and dropped the rest (its recording shows "h", then an empty field), and
    /// Connect then tried `https://h`. So the field is read after typing; when it holds
    /// something else, the test waits for the field to have the keyboard, clears it, and types
    /// again, three times at most, then fails with what the field holds.
    ///
    /// On tvOS a field is typed in the system's keyboard screen: `open` brings it up (focus
    /// the field, press Select), and the text goes to the app, not to the field.
    func typeExactly(_ text: String, in app: XCUIApplication, secure: Bool = false, open: (() -> Void)? = nil,
                     file: StaticString = #filePath, line: UInt = #line) {
        var holds = ""
        for attempt in 1 ... 3 {
            if attempt > 1 || !hasKeyboardFocus {
                guard focus(app, open: open) else {
                    XCTFail("the field never had the keyboard: \(debugDescription)", file: file, line: line)
                    return
                }
            }
            // emptied first, and read back empty: a delete can be lost like any key (CI: the
            // home's address typed three times over what the deletes left)
            for _ in 1 ... 3 where !typedValue.isEmpty {
                let deletes = String(repeating: XCUIKeyboardKey.delete.rawValue, count: typedValue.count)
                #if os(tvOS)
                app.typeText(deletes)
                #else
                typeText(deletes)
                #endif
            }
            #if os(tvOS)
            app.typeText(text)
            #else
            typeText(text)
            #endif
            // the field's value can trail the last key by a moment on a loaded runner
            let end = Date().addingTimeInterval(2)
            holds = typedValue
            while !(secure ? !holds.isEmpty : holds == text), Date() < end {
                RunLoop.current.run(until: Date().addingTimeInterval(0.2))
                holds = typedValue
            }
            if secure ? !holds.isEmpty : holds == text { return }
        }
        XCTFail("the field holds \"\(holds)\", not \(secure ? "the password" : "\"\(text)\"")", file: file, line: line)
    }

    /// The field gets the keyboard: tapped (clicked on the Mac; on tvOS `open`) and waited on
    /// until it has it, tapped again when a tap did not give it (CI: a sign-in field tapped as
    /// its screen was still changing, "Neither element nor any descendant has keyboard
    /// focus"), three taps at most. Text is typed only into a field that has the keyboard.
    private func focus(_ app: XCUIApplication, open: (() -> Void)?) -> Bool {
        for _ in 1 ... 3 {
            #if os(tvOS)
            open?()
            #elseif os(macOS)
            click()
            #else
            tap()
            _ = app.keyboards.firstMatch.waitForExistence(timeout: 3)
            #endif
            let end = Date().addingTimeInterval(3)
            while !hasKeyboardFocus, Date() < end { RunLoop.current.run(until: Date().addingTimeInterval(0.2)) }
            if hasKeyboardFocus { return true }
        }
        return false
    }

    private var hasKeyboardFocus: Bool { (value(forKey: "hasKeyboardFocus") as? Bool) ?? false }

    /// What was typed in the field: its value, unless that is the placeholder an empty field
    /// reports as its value.
    private var typedValue: String {
        let v = value as? String ?? ""
        return v == placeholderValue ? "" : v
    }
}
