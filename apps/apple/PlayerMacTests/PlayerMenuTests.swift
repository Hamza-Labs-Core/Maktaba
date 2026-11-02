#if os(macOS)
import AppKit
import SwiftUI
import Testing
@testable import Maktaba_Player_Mac

/// The Mac Player's menu bar as the app builds it (story 48.25, FR-42): each item's key, read
/// from the running app's main menu. `PlayerMacMenuUITests` drives the same menus from outside.
@MainActor @Suite struct PlayerMenus {
    /// Whichever sections show get ⌘1, ⌘2 … in their order, never more than five.
    @Test func theShownSectionsTakeTheNumbers() {
        let all = PlayerViewMenu.items(PlayerSection.allCases)
        #expect(all.map(\.section) == [.home, .libraries, .liveTV, .search, .profile])
        #expect(all.map(\.key.character) == ["1", "2", "3", "4", "5"])
        let some = PlayerViewMenu.items([.libraries, .search])
        #expect(some.map(\.section) == [.libraries, .search])
        #expect(some.map(\.key.character) == ["1", "2"])
        #expect(PlayerViewMenu.items([]).isEmpty)
    }

    @Test func theViewMenuHasTheSectionsSearchAndFullScreen() throws {
        let view = try Self.menu("View")
        for (i, section) in PlayerSection.visible().prefix(5).enumerated() {
            let item = try #require(view.items.first { $0.title == String(localized: section.title) }, "\(section)")
            #expect(item.keyEquivalent == "\(i + 1)")
            #expect(Self.modifiers(item) == .command)
        }
        let search = try #require(view.items.first { $0.title == "Search…" })
        #expect(search.keyEquivalent == "f")
        #expect(Self.modifiers(search) == .command)
        let full = try #require(view.items.first { $0.title == "Enter Full Screen" })
        #expect(full.keyEquivalent == "f")
        #expect(Self.modifiers(full) == [.control, .command])
    }

    /// FR-42's keys: space, ←/→ 10 s, ⇧←/⇧→ 30 s, N next, S subtitles, ⌘. stop.
    @Test func thePlaybackMenuHasItsKeys() throws {
        let playback = try Self.menu("Playback")
        let left = String(UnicodeScalar(NSLeftArrowFunctionKey)!)
        let right = String(UnicodeScalar(NSRightArrowFunctionKey)!)
        let expected: [(String, String, NSEvent.ModifierFlags)] = [
            ("Play/Pause", " ", []),
            ("Skip Forward 10 Seconds", right, []),
            ("Skip Back 10 Seconds", left, []),
            ("Skip Forward 30 Seconds", right, .shift),
            ("Skip Back 30 Seconds", left, .shift),
            ("Next Episode", "n", []),
            ("Subtitles", "s", []),
            ("Stop", ".", .command),
        ]
        for (title, key, mods) in expected {
            let item = try #require(playback.items.first { $0.title == title }, "\(title)")
            #expect(item.keyEquivalent == key, "\(title)")
            #expect(Self.modifiers(item) == mods, "\(title)")
            // nothing plays in the test host: every key stays the lists' and the fields'
            #expect(!item.isEnabled, "\(title) is off while nothing plays")
        }
    }

    /// The window goes full screen on a space of its own (FR-43); a panel does not.
    @Test func fullScreenIsTheMainWindowsNotAPanels() async throws {
        try await Task.sleep(for: .seconds(1))
        let main = try #require(NSApp.windows.first { $0.styleMask.contains(.resizable) && $0.isVisible })
        #expect(MacFullScreen.canFullScreen(main))
        let panel = NSPanel(contentRect: .init(x: 0, y: 0, width: 200, height: 100), styleMask: [.titled, .resizable],
                            backing: .buffered, defer: true)
        panel.collectionBehavior = .fullScreenAuxiliary
        #expect(!MacFullScreen.canFullScreen(panel))
    }

    static func menu(_ title: String) throws -> NSMenu {
        let menu = try #require(NSApp.mainMenu?.item(withTitle: title)?.submenu, "the \(title) menu")
        menu.update()
        return menu
    }

    static func modifiers(_ item: NSMenuItem) -> NSEvent.ModifierFlags {
        item.keyEquivalentModifierMask.intersection([.command, .shift, .control, .option])
    }
}
#endif
