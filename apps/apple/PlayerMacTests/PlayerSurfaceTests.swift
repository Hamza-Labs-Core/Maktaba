#if os(macOS)
import AppKit
import AVKit
import SwiftUI
import Testing
import MaktabaKit

/// The Mac's player surface is AppKit's AVPlayerView in our plain host view, on screen in
/// the app. Launched normally, SwiftUI's VideoPlayer (and AVPlayerView represented
/// directly) abort in `_AVKit_SwiftUI` on macOS 27 as soon as playback appears; a test run
/// does not reproduce that abort, so this guards the shape of the fix instead.
@MainActor @Suite struct MacPlayerSurface {
    @Test func showsInAWindow() throws {
        let player = AVPlayer()
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 640, height: 360),
                              styleMask: [.titled], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        defer { window.close() }
        let hosting = NSHostingView(rootView: PlayerSurface(player: player).frame(width: 640, height: 360))
        window.contentView = hosting
        window.orderFrontRegardless()
        hosting.layoutSubtreeIfNeeded()
        window.displayIfNeeded()
        RunLoop.main.run(until: .now.addingTimeInterval(0.2))
        let host = try #require(Self.find(PlayerHostView.self, in: hosting))
        #expect(host.player === player)
        let view = try #require(Self.find(AVPlayerView.self, in: host))
        #expect(view.player === player)
        // FR-43: the controls offer picture in picture, a window that floats above every app,
        // and full screen
        #expect(view.allowsPictureInPicturePlayback)
        #expect(view.showsFullScreenToggleButton)
    }

    static func find<V: NSView>(_: V.Type, in view: NSView) -> V? {
        if let v = view as? V { return v }
        for sub in view.subviews { if let v = find(V.self, in: sub) { return v } }
        return nil
    }
}
#endif
