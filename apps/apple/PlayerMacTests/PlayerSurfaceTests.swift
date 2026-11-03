#if os(macOS)
import AppKit
import AVKit
import SwiftUI
import Testing
import MaktabaKit
@testable import Maktaba_Player_Mac

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

    /// The watch and live players play on when the person leaves the app with Picture in
    /// Picture off (the default policy pauses a video drawn in a layer; on iPhone and iPad that
    /// cut the sound on going home). Shared code: checked here, where it runs on every PR.
    @Test func theWatchAndLivePlayersPlayOnWhenTheAppLeaves() {
        #expect(Playback.newPlayer().audiovisualBackgroundPlaybackPolicy == .continuesIfPossible)
        let home = URL(string: "http://127.0.0.1:9")!
        let live = LivePlayback(client: APIClient(home: home, tokens: TokenStore(app: "test.mac.background", home: home)),
                                channel: "ch-1")
        #expect(live.player.audiovisualBackgroundPlaybackPolicy == .continuesIfPossible)
    }

    static func find<V: NSView>(_: V.Type, in view: NSView) -> V? {
        if let v = view as? V { return v }
        for sub in view.subviews { if let v = find(V.self, in: sub) { return v } }
        return nil
    }
}
#endif
