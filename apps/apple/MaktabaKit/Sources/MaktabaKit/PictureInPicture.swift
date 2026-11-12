import AVKit
import Observation

/// Picture in Picture for a player drawn in an `AVPlayerLayer` (Epic 48 story 48.24, FR-37):
/// the Player's own controls draw over a bare layer, so it drives
/// `AVPictureInPictureController` itself. On iPhone and iPad it also starts by itself when the
/// person goes home while the video plays.
///
/// The controller is taken apart on the main thread, by `close` when the player closes (or,
/// failing that, when this object goes), and kept a while after: AVKit's own queue holds it
/// for a moment while the player changes, and a controller let go at that moment was freed
/// on that queue, where taking its view out of the window raised an Auto Layout exception
/// that ended the app (an iPad leaving a live channel, CI 2026-11-12).
@MainActor @Observable
public final class PictureInPicture: NSObject {
    /// The window floats.
    public private(set) var isActive = false
    /// The layer is ready and the device can (false in a test host without a window).
    public private(set) var isPossible = false

    /// The controller and its observation, in a box `deinit` may hand to the main thread.
    final class Held: @unchecked Sendable {
        var controller: AVPictureInPictureController?
        var possible: NSKeyValueObservation?
    }

    @ObservationIgnored private let held = Held()
    @ObservationIgnored private weak var layer: AVPlayerLayer?
    /// How long a closed controller is kept before it is let go, on the main thread.
    @ObservationIgnored private let linger: Duration

    /// The closed controllers still kept (`retire`).
    static private(set) var retired: [AVPictureInPictureController] = []

    public init(linger: Duration = .seconds(10)) {
        self.linger = linger
    }

    deinit {
        // not closed by its player: taken apart on the main thread all the same
        guard held.controller != nil else { return }
        let held = held, linger = linger
        DispatchQueue.main.async { MainActor.assumeIsolated { Self.retire(held, linger: linger) } }
    }

    /// Whether this device has Picture in Picture at all.
    public static var isSupported: Bool { AVPictureInPictureController.isPictureInPictureSupported() }

    /// A layer is attached and its controller made.
    var isAttached: Bool { held.controller != nil }
    /// The controller, for the tests of its teardown.
    var controllerForTests: AVPictureInPictureController? { held.controller }

    /// The layer the picture is drawn in (again when the view is made anew).
    public func attach(_ layer: AVPlayerLayer) {
        guard Self.isSupported, layer !== self.layer else { return }
        // the controller of the layer before goes as a closed one does
        Self.retire(held, linger: linger)
        self.layer = layer
        let c = AVPictureInPictureController(playerLayer: layer)
        c?.delegate = self
        #if os(iOS)
        c?.canStartPictureInPictureAutomaticallyFromInline = true
        #endif
        held.controller = c
        held.possible = c.map { Self.observePossible($0) { [weak self] now in self?.isPossible = now } }
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

    /// The player closes for good: the floating window comes back, and the controller is
    /// taken off its layer here, on the main thread, before anything lets it go. The same
    /// layer may be attached again after.
    public func close() {
        Self.retire(held, linger: linger)
        layer = nil
        if isActive { isActive = false }
        if isPossible { isPossible = false }
    }

    /// Takes a controller apart on the main thread (no observation, no delegate, no layer:
    /// nothing of UIKit is left for its `dealloc` to undo) and keeps it for `linger`, so
    /// that the last reference to it is never one AVKit's queue holds.
    static func retire(_ held: Held, linger: Duration) {
        held.possible?.invalidate()
        held.possible = nil
        guard let c = held.controller else { return }
        held.controller = nil
        c.delegate = nil
        if c.isPictureInPictureActive { c.stopPictureInPicture() }
        c.contentSource = nil
        retired.append(c)
        let id = ObjectIdentifier(c)
        Task { @MainActor in
            try? await Task.sleep(for: linger)
            retired.removeAll { ObjectIdentifier($0) == id }
        }
    }

    /// The button: start, or stop and come back.
    public func toggle() {
        guard let controller = held.controller else { return }
        if controller.isPictureInPictureActive {
            controller.stopPictureInPicture()
        } else {
            controller.startPictureInPicture()
        }
    }

    /// Back into the app (the player closes or the video changes screen).
    public func stop() {
        held.controller?.stopPictureInPicture()
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

extension AVPlayer {
    /// The sound plays on when the person leaves the app with Picture in Picture off (they
    /// turned off its automatic start in Settings, the device has none, or it is a sound-only
    /// item). By default (`.automatic`) iOS pauses a player drawn in a layer when the app goes
    /// to the background unless Picture in Picture takes the picture; this keeps it playing,
    /// the picture hidden until the app is back. Needs the audio background mode and the
    /// playback audio session (`PlaybackAudio`), as Picture in Picture does.
    public func keepPlayingInBackground() {
        audiovisualBackgroundPlaybackPolicy = .continuesIfPossible
    }
}

/// The audio background mode, which the sound in the background and Picture in Picture both
/// need on iPhone and iPad.
public enum BackgroundAudio {
    /// `info` (an app's Info.plist) lists `audio` in `UIBackgroundModes`.
    public static func isDeclared(in info: [String: Any]?) -> Bool {
        (info?["UIBackgroundModes"] as? [String])?.contains("audio") == true
    }
}
