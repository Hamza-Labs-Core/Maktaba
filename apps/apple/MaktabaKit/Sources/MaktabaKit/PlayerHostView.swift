#if os(macOS)
import AppKit
import AVKit

/// AppKit's player in a plain NSView: SwiftUI on macOS 27 aborts setting up a view whose own
/// type is an AVKit class (SwiftUI's VideoPlayer, or AVPlayerView represented directly), so
/// it sees only this host. No SwiftUI here, on purpose.
public final class PlayerHostView: NSView {
    private let playerView = AVPlayerView()

    public var player: AVPlayer? {
        get { playerView.player }
        set { if playerView.player !== newValue { playerView.player = newValue } }
    }

    public init(player: AVPlayer) {
        super.init(frame: .zero)
        playerView.controlsStyle = .floating
        playerView.showsFullScreenToggleButton = true
        // the controls' picture-in-picture button: the video floats above every app (FR-43)
        playerView.allowsPictureInPicturePlayback = true
        playerView.player = player
        playerView.frame = bounds
        playerView.autoresizingMask = [.width, .height]
        addSubview(playerView)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) { fatalError("not from a nib") }
}
#endif
