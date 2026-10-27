import AVKit
import SwiftUI

/// The system player for `player`: SwiftUI's VideoPlayer on iPhone and iPad; on the Mac
/// AppKit's AVPlayerView in a plain host view (see `PlayerHostView`), with the Mac's own
/// controls and full-screen button.
#if os(macOS)
public struct PlayerSurface: NSViewRepresentable {
    let player: AVPlayer

    public init(player: AVPlayer) {
        self.player = player
    }

    public func makeNSView(context: Context) -> NSView {
        PlayerHostView(player: player)
    }

    public func updateNSView(_ v: NSView, context: Context) {
        (v as? PlayerHostView)?.player = player
    }
}
#else
public struct PlayerSurface: View {
    let player: AVPlayer

    public init(player: AVPlayer) {
        self.player = player
    }

    public var body: some View { VideoPlayer(player: player) }
}
#endif
