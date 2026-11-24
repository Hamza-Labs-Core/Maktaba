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
