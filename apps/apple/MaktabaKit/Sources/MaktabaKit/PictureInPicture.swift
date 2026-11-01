import AVKit
import Observation

/// Picture in Picture for a player drawn in an `AVPlayerLayer` (Epic 48 story 48.24, FR-37):
/// the Player's own controls draw over a bare layer, so it drives
/// `AVPictureInPictureController` itself. On iPhone and iPad it also starts by itself when the
/// person goes home while the video plays.
@MainActor @Observable
public final class PictureInPicture: NSObject {
    /// The window floats.
    public private(set) var isActive = false
    /// The layer is ready and the device can (false in a test host without a window).
    public private(set) var isPossible = false

    @ObservationIgnored private var controller: AVPictureInPictureController?
    @ObservationIgnored private var possible: NSKeyValueObservation?
    @ObservationIgnored private weak var layer: AVPlayerLayer?

    override public init() {}

    /// Whether this device has Picture in Picture at all.
    public static var isSupported: Bool { AVPictureInPictureController.isPictureInPictureSupported() }

    /// The layer the picture is drawn in (again when the view is made anew).
    public func attach(_ layer: AVPlayerLayer) {
        guard Self.isSupported, layer !== self.layer else { return }
        self.layer = layer
        let c = AVPictureInPictureController(playerLayer: layer)
        c?.delegate = self
        #if os(iOS)
        c?.canStartPictureInPictureAutomaticallyFromInline = true
        #endif
        controller = c
        possible = c.map { Self.observePossible($0) { [weak self] now in self?.isPossible = now } }
    }

    /// KVO may call back on any thread: the handler is made outside the main actor (no
    /// isolation for Swift to check there) and hops on.
    nonisolated private static func observePossible(_ c: AVPictureInPictureController,
                                                    _ changed: @escaping @MainActor @Sendable (Bool) -> Void) -> NSKeyValueObservation {
        c.observe(\.isPictureInPicturePossible, options: [.initial, .new]) { c, _ in
            let now = c.isPictureInPicturePossible
            Task { @MainActor in changed(now) }
        }
    }

    /// The button: start, or stop and come back.
    public func toggle() {
        guard let controller else { return }
        if controller.isPictureInPictureActive {
            controller.stopPictureInPicture()
        } else {
            controller.startPictureInPicture()
        }
    }

    /// Back into the app (the player closes or the video changes screen).
    public func stop() {
        controller?.stopPictureInPicture()
    }
}

extension PictureInPicture: AVPictureInPictureControllerDelegate {
    nonisolated public func pictureInPictureControllerDidStartPictureInPicture(_ c: AVPictureInPictureController) {
        Task { @MainActor in self.isActive = true }
    }

    nonisolated public func pictureInPictureControllerDidStopPictureInPicture(_ c: AVPictureInPictureController) {
        Task { @MainActor in self.isActive = false }
    }

    nonisolated public func pictureInPictureController(_ c: AVPictureInPictureController,
                                                       failedToStartPictureInPictureWithError error: Error) {
        Log.failure("start picture in picture", error)
        Task { @MainActor in self.isActive = false }
    }

    nonisolated public func pictureInPictureController(
        _ c: AVPictureInPictureController,
        restoreUserInterfaceForPictureInPictureStopWithCompletionHandler done: @escaping (Bool) -> Void
    ) {
        // the player screen is still open behind the floating window
        done(true)
    }
}
