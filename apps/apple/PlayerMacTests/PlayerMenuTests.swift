#if os(macOS)
import AppKit
import OSLog
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
        // one: the Player's, not AppKit's beside it (toggleFullScreen:)
        #expect(view.items.filter { $0.title == "Enter Full Screen" }.count == 1)
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

    /// What View's full-screen item says and does follows its window (FR-43): Exit while it is full
    /// screen, and choosing it asks the window to leave or enter. The UI test reads the item itself
    /// and uses its key: in full screen macOS hides the menu bar.
    @Test func fullScreenFollowsItsWindowAndTogglesIt() {
        let full = MacFullScreen.shared
        let was = full.window
        let window = FullScreenWindow(contentRect: .init(x: 0, y: 0, width: 400, height: 300),
                                      styleMask: [.titled, .resizable], backing: .buffered, defer: true)
        window.isReleasedWhenClosed = false
        defer {
            full.window = was
            full.refresh()
        }
        full.window = { window }
        full.refresh()
        #expect(!full.isFullScreen)
        full.toggle()
        #expect(window.toggles == 1, "asked to enter")

        window.isFull = true
        NotificationCenter.default.post(name: NSWindow.didEnterFullScreenNotification, object: window)
        #expect(full.isFullScreen, "Exit, once the window is full screen")
        full.toggle()
        #expect(window.toggles == 2, "asked to leave")

        window.isFull = false
        NotificationCenter.default.post(name: NSWindow.didExitFullScreenNotification, object: window)
        #expect(!full.isFullScreen, "Enter again, once it has left")
    }

    /// A window that says it is full screen when told to, and counts what it is asked.
    private final class FullScreenWindow: NSWindow {
        var isFull = false
        private(set) var toggles = 0
        override var styleMask: NSWindow.StyleMask {
            get { isFull ? super.styleMask.union(.fullScreen) : super.styleMask }
            set { super.styleMask = newValue }
        }
        override func toggleFullScreen(_ sender: Any?) { toggles += 1 }
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

/// The app logged its launch (PlayerApp's init calls `Log.launched`): the info line the CI
/// jobs' app-log check (tools/apple/check-app-log.py) looks for, read from the log of this
/// process, the app hosting these tests. The Mac job keeps the subsystem at persist:debug.
@Suite struct PlayerLaunchLog {
    @Test func theLaunchIsLogged() throws {
        let bundle = try #require(Bundle.main.bundleIdentifier)
        #expect(bundle == "com.hamzalabs.maktaba.player.mac")
        let store = try OSLogStore(scope: .currentProcessIdentifier)
        let lines = try store.getEntries(matching: NSPredicate(format: "subsystem == %@ AND category == %@", bundle, "session"))
            .compactMap { $0 as? OSLogEntryLog }
        let launched = lines.filter { $0.composedMessage.hasPrefix("launched \(bundle) ") }
        #expect(!launched.isEmpty, "no launch line among \(lines.count) session lines")
        #expect(launched.allSatisfy { $0.level == .info })
    }
}
#endif
