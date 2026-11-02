import AVKit
import SwiftUI

/// The picture of `player`. With the system's controls (`showsControls`, the default):
/// SwiftUI's VideoPlayer on iPhone and iPad; on the Mac AppKit's AVPlayerView in a plain host
/// view (see `PlayerHostView`), with the Mac's own controls and full-screen button. Without
/// them (the Player's own controls, story 48.23): the bare picture in an `AVPlayerLayer`,
/// fitted to the view, on every platform.
#if os(macOS)
public struct PlayerSurface: NSViewRepresentable {
    let player: AVPlayer
    let showsControls: Bool

    public init(player: AVPlayer, showsControls: Bool = true) {
        self.player = player
        self.showsControls = showsControls
    }

    public func makeNSView(context: Context) -> NSView {
        showsControls ? PlayerHostView(player: player) : PlayerLayerView(player: player)
    }

    public func updateNSView(_ v: NSView, context: Context) {
        (v as? PlayerHostView)?.player = player
        (v as? PlayerLayerView)?.player = player
    }
}

/// The bare picture: an `AVPlayerLayer` in a plain NSView (no AVKit view class, which
/// SwiftUI on macOS 27 aborts on; see `PlayerHostView`).
public final class PlayerLayerView: NSView {
    public let playerLayer = AVPlayerLayer()

    public var player: AVPlayer? {
        get { playerLayer.player }
        set { if playerLayer.player !== newValue { playerLayer.player = newValue } }
    }

    public init(player: AVPlayer) {
        super.init(frame: .zero)
        wantsLayer = true
        layer?.backgroundColor = NSColor.black.cgColor
        playerLayer.videoGravity = .resizeAspect
        playerLayer.player = player
        layer?.addSublayer(playerLayer)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) { fatalError("not from a nib") }

    override public func layout() {
        super.layout()
        playerLayer.frame = bounds
    }
}
#else
public struct PlayerSurface: View {
    let player: AVPlayer
    let showsControls: Bool

    public init(player: AVPlayer, showsControls: Bool = true) {
        self.player = player
        self.showsControls = showsControls
    }

    public var body: some View {
        if showsControls {
            VideoPlayer(player: player)
        } else {
            PlayerLayerRepresentable(player: player)
        }
    }
}

private struct PlayerLayerRepresentable: UIViewRepresentable {
    let player: AVPlayer

    func makeUIView(context: Context) -> PlayerLayerView { PlayerLayerView(player: player) }

    func updateUIView(_ v: PlayerLayerView, context: Context) { v.player = player }
}

/// The bare picture: a view whose own layer is the `AVPlayerLayer`.
public final class PlayerLayerView: UIView {
    override public class var layerClass: AnyClass { AVPlayerLayer.self }

    public var playerLayer: AVPlayerLayer {
        // swiftlint:disable:next force_cast
        layer as! AVPlayerLayer
    }

    public var player: AVPlayer? {
        get { playerLayer.player }
        set { if playerLayer.player !== newValue { playerLayer.player = newValue } }
    }

    public init(player: AVPlayer) {
        super.init(frame: .zero)
        backgroundColor = .black
        playerLayer.videoGravity = .resizeAspect
        playerLayer.player = player
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) { fatalError("not from a nib") }
}
#endif
