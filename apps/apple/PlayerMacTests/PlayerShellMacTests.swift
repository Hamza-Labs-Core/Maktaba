#if os(macOS)
import AppKit
import SwiftUI
import Testing
import MaktabaKit
@testable import Maktaba_Player_Mac

/// Story 13.16: the Mac shell's split view (`MacShellSplit`) puts the Player's sidebar at the
/// start of the window: the left in English, the right when AppKit lays the window out right
/// to left, as it does for a Player started in Arabic (`LanguageChoice`, the desktop design's
/// D5). Drawn in a window of the design's size; the sidebar found by its accessibility
/// identifier, as VoiceOver and the UI tests find it.
@MainActor @Suite(.serialized) struct PlayerShellMac {
    @Test(arguments: [false, true]) func theSidebarSitsAtTheStart(_ rightToLeft: Bool) async throws {
        let size = CGSize(width: 1200, height: 780)
        let view = MacShellSplit(rows: MacSidebarRows(PlayerSection.allCases), selection: .constant(.home),
                                 arrangement: .constant(SidebarArrangement())) {
            Color.mktBackground
        }
        .environment(\.layoutDirection, rightToLeft ? .rightToLeft : .leftToRight)
        .preferredColorScheme(.dark)
        .frame(width: size.width, height: size.height)
        let window = NSWindow(contentRect: NSRect(origin: .zero, size: size), styleMask: [.titled, .fullSizeContentView],
                              backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.appearance = NSAppearance(named: .darkAqua)
        defer { window.close() }
        let hosting = NSHostingView(rootView: view)
        hosting.userInterfaceLayoutDirection = rightToLeft ? .rightToLeft : .leftToRight
        window.contentView = hosting
        window.orderFrontRegardless()
        try await Task.sleep(for: .milliseconds(500))
        hosting.layoutSubtreeIfNeeded()

        let enhanced = NSAccessibility.Attribute(rawValue: "AXEnhancedUserInterface")
        NSApp.accessibilitySetValue(true, forAttribute: enhanced)
        defer { NSApp.accessibilitySetValue(false, forAttribute: enhanced) }
        try await Task.sleep(for: .milliseconds(300))
        let sidebar = try #require(Self.element(hosting, id: "player.sidebar"), "the sidebar by its identifier")
        let frame = sidebar.accessibilityFrame()
        let win = window.frame
        #expect(frame.width > 150 && frame.width < 400, "a sidebar column's width: \(frame)")
        // VoiceOver's name for it, and the chosen row said to be selected
        #expect((PageSnapshots.ax(sidebar as! NSObject, "accessibilityLabel") as? String) == "Sections")
        if rightToLeft {
            #expect(frame.midX > win.midX, "at the right of the window: \(frame) in \(win)")
        } else {
            #expect(frame.midX < win.midX, "at the left of the window: \(frame) in \(win)")
        }
    }

    /// The Libraries section at the product shots' size, in the shell's split view: the page has
    /// what the sidebar and the list leave (a split view inside the split view inset it by the
    /// sidebar's width again: 160 points of 1024).
    @Test func thePageHasTheRoomTheSidebarAndTheListLeave() async throws {
        let home = try MockHome()
        home.clock = PageSnapshots.clock
        try home.start()
        defer { home.stop() }
        let url = try #require(URL(string: home.url))
        let client = APIClient(home: url, tokens: TokenStore(app: "test.mac.shell", home: url))
        let defaults = try #require(UserDefaults(suiteName: "test.mac.shell"))
        let session = AppSession(app: "test.mac.shell", defaults: defaults, asksHomes: false)
        let user = try JSONDecoder().decode(User.self, from: Data(#"{"id":"u1","username":"viewer","is_admin":false}"#.utf8))
        let state = LibrariesState(session: session, client: client)
        state.choice = .library("lib-docs")
        let size = CGSize(width: 1024, height: 674)
        let view = MacShellSplit(rows: MacSidebarRows(PlayerSection.allCases), selection: .constant(.libraries),
                                 arrangement: .constant(SidebarArrangement())) {
            LibraryBrowser(session: session, user: user, client: client, playing: .constant(false), state: state)
        }
        .preferredColorScheme(.dark)
        .frame(width: size.width, height: size.height)
        let window = NSWindow(contentRect: NSRect(origin: .zero, size: size), styleMask: [.titled, .fullSizeContentView],
                              backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.appearance = NSAppearance(named: .darkAqua)
        defer { window.close() }
        let hosting = NSHostingView(rootView: view)
        window.contentView = hosting
        window.orderFrontRegardless()

        let enhanced = NSAccessibility.Attribute(rawValue: "AXEnhancedUserInterface")
        NSApp.accessibilitySetValue(true, forAttribute: enhanced)
        defer { NSApp.accessibilitySetValue(false, forAttribute: enhanced) }
        var page: NSAccessibilityProtocol?
        for _ in 0 ..< 100 where page == nil {
            try await Task.sleep(for: .milliseconds(100))
            hosting.layoutSubtreeIfNeeded()
            page = Self.element(hosting, id: "screen.library")
        }
        let frame = try #require(page, "the library's page by its identifier").accessibilityFrame()
        let list = try #require(Self.element(hosting, id: "screen.libraries"), "the list by its identifier").accessibilityFrame()
        #expect(frame.minX >= list.maxX - 1, "the page after the list: \(frame), \(list)")
        #expect(frame.width > 400, "the page's width: \(frame) in \(window.frame), the list \(list)")
    }

    /// The accessibility element with `id`, depth first.
    static func element(_ root: Any, id: String) -> NSAccessibilityProtocol? {
        guard let o = root as? NSObject else { return nil }
        if let e = o as? NSAccessibilityProtocol, (PageSnapshots.ax(o, "accessibilityIdentifier") as? String) == id { return e }
        for child in (PageSnapshots.ax(o, "accessibilityChildren") as? [Any]) ?? [] {
            if let found = element(child, id: id) { return found }
        }
        return nil
    }
}
#endif
