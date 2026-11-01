import AVKit
import MediaPlayer

#if os(macOS)
import AppKit
public typealias PlatformImage = NSImage
#else
import UIKit
public typealias PlatformImage = UIImage
#endif

// Now Playing and the remote commands (Epic 48 story 48.24, FR-39): the lock screen, Control
// Centre, a headset, CarPlay's and the Mac's media keys show what plays and drive it.

/// What the system shows as playing.
public struct NowPlayingItem: Equatable, Sendable {
    public var title: String
    /// A series and episode, or the programme on a channel.
    public var subtitle: String?
    /// Seconds (nil: live, no end).
    public var duration: Double?
    public var position: Double
    public var playing: Bool

    public init(title: String, subtitle: String? = nil, duration: Double? = nil, position: Double = 0, playing: Bool) {
        self.title = title
        self.subtitle = subtitle
        self.duration = duration
        self.position = position
        self.playing = playing
    }

    public var isLive: Bool { duration == nil }

    /// `MPNowPlayingInfoCenter`'s dictionary (without the artwork). The system moves the
    /// position on by itself from the rate, so it is sent again only on a change.
    public var info: [String: Any] {
        var d: [String: Any] = [
            MPMediaItemPropertyTitle: title,
            MPNowPlayingInfoPropertyPlaybackRate: playing ? 1.0 : 0.0,
            MPNowPlayingInfoPropertyDefaultPlaybackRate: 1.0,
            MPNowPlayingInfoPropertyIsLiveStream: isLive,
            MPNowPlayingInfoPropertyMediaType: MPNowPlayingInfoMediaType.video.rawValue,
        ]
        if let subtitle { d[MPMediaItemPropertyArtist] = subtitle }
        if let duration, duration.isFinite {
            d[MPMediaItemPropertyPlaybackDuration] = duration
            d[MPNowPlayingInfoPropertyElapsedPlaybackTime] = position.isFinite ? max(0, position) : 0
        }
        return d
    }
}

/// A command from outside the app.
public enum RemoteCommand: Equatable, Sendable {
    case play, pause, toggle
    /// Seconds forward (negative: back).
    case skip(Double)
    case next
    /// To this position, in seconds.
    case seek(Double)
}

/// What the app does for each remote command; nil leaves the command off.
public struct RemoteHandlers {
    public var play: () -> Void
    public var pause: () -> Void
    public var toggle: () -> Void
    public var skip: ((Double) -> Void)?
    /// Off while there is no next episode.
    public var next: (() -> Void)?
    public var seek: ((Double) -> Void)?

    public init(play: @escaping () -> Void, pause: @escaping () -> Void, toggle: @escaping () -> Void,
                skip: ((Double) -> Void)? = nil, next: (() -> Void)? = nil, seek: ((Double) -> Void)? = nil) {
        self.play = play
        self.pause = pause
        self.toggle = toggle
        self.skip = skip
        self.next = next
        self.seek = seek
    }
}

/// The app's one entry in Now Playing and the remote commands. Whoever plays (a video, a
/// channel) `begin`s it; only the current owner updates or ends it, so a channel closing late
/// does not clear the video that opened after it.
@MainActor
public final class NowPlaying {
    public static let shared = NowPlaying()

    /// Seconds the skip commands move.
    public static let skipInterval = 10.0

    private let commands = MPRemoteCommandCenter.shared()
    private let center = MPNowPlayingInfoCenter.default()
    private var owner: ObjectIdentifier?
    private var handlers: RemoteHandlers?
    private var targets: [(MPRemoteCommand, Any)] = []
    private var shown: NowPlayingItem?
    private var artwork: MPMediaItemArtwork?

    init() {}

    /// The current item (tests read it).
    public var item: NowPlayingItem? { shown }

    /// Whether `owner` holds Now Playing.
    public func isOwned(by owner: AnyObject) -> Bool { self.owner == ObjectIdentifier(owner) }

    /// `owner` plays now: its handlers answer the remote commands.
    public func begin(_ owner: AnyObject, handlers: RemoteHandlers) {
        self.owner = ObjectIdentifier(owner)
        self.handlers = handlers
        artwork = nil
        shown = nil
        register()
    }

    /// New handlers for the same owner (a next episode found, or gone).
    public func update(_ owner: AnyObject, handlers: RemoteHandlers) {
        guard isOwned(by: owner) else { return }
        self.handlers = handlers
        enable()
    }

    /// What plays now (sent only when it changed).
    public func show(_ owner: AnyObject, _ item: NowPlayingItem) {
        guard isOwned(by: owner), item != shown else { return }
        shown = item
        publish()
    }

    /// The poster or channel logo.
    public func show(_ owner: AnyObject, artwork image: PlatformImage) {
        guard isOwned(by: owner) else { return }
        artwork = MPMediaItemArtwork(boundsSize: image.size) { _ in image }
        publish()
    }

    /// `owner` stopped: Now Playing clears and the commands go off.
    public func end(_ owner: AnyObject) {
        guard isOwned(by: owner) else { return }
        self.owner = nil
        handlers = nil
        shown = nil
        artwork = nil
        unregister()
        center.nowPlayingInfo = nil
        #if os(macOS)
        center.playbackState = .stopped
        #endif
    }

    /// Runs a command as the system would (the system's handlers call this; tests too).
    @discardableResult public func perform(_ command: RemoteCommand) -> MPRemoteCommandHandlerStatus {
        guard let h = handlers else { return .noActionableNowPlayingItem }
        switch command {
        case .play: h.play()
        case .pause: h.pause()
        case .toggle: h.toggle()
        case let .skip(s):
            guard let skip = h.skip else { return .commandFailed }
            skip(s)
        case .next:
            guard let next = h.next else { return .noSuchContent }
            next()
        case let .seek(to):
            guard let seek = h.seek else { return .commandFailed }
            seek(to)
        }
        return .success
    }

    /// Which commands are on now (tests read it).
    public var enabled: [String: Bool] {
        ["play": commands.playCommand.isEnabled, "pause": commands.pauseCommand.isEnabled,
         "skipForward": commands.skipForwardCommand.isEnabled, "skipBackward": commands.skipBackwardCommand.isEnabled,
         "next": commands.nextTrackCommand.isEnabled, "seek": commands.changePlaybackPositionCommand.isEnabled]
    }

    private func publish() {
        guard var info = shown?.info else { return }
        if let artwork { info[MPMediaItemPropertyArtwork] = artwork }
        center.nowPlayingInfo = info
        #if os(macOS)
        center.playbackState = shown?.playing == true ? .playing : .paused
        #endif
    }

    private func register() {
        unregister()
        func add(_ c: MPRemoteCommand, _ run: @escaping @MainActor (MPRemoteCommandEvent) -> MPRemoteCommandHandlerStatus) {
            let t = c.addTarget { e in MainActor.assumeIsolated { run(e) } }
            targets.append((c, t))
        }
        add(commands.playCommand) { [weak self] _ in self?.perform(.play) ?? .commandFailed }
        add(commands.pauseCommand) { [weak self] _ in self?.perform(.pause) ?? .commandFailed }
        add(commands.togglePlayPauseCommand) { [weak self] _ in self?.perform(.toggle) ?? .commandFailed }
        commands.skipForwardCommand.preferredIntervals = [NSNumber(value: Self.skipInterval)]
        commands.skipBackwardCommand.preferredIntervals = [NSNumber(value: Self.skipInterval)]
        add(commands.skipForwardCommand) { [weak self] e in
            self?.perform(.skip((e as? MPSkipIntervalCommandEvent)?.interval ?? Self.skipInterval)) ?? .commandFailed
        }
        add(commands.skipBackwardCommand) { [weak self] e in
            self?.perform(.skip(-((e as? MPSkipIntervalCommandEvent)?.interval ?? Self.skipInterval))) ?? .commandFailed
        }
        add(commands.nextTrackCommand) { [weak self] _ in self?.perform(.next) ?? .commandFailed }
        add(commands.changePlaybackPositionCommand) { [weak self] e in
            guard let to = (e as? MPChangePlaybackPositionCommandEvent)?.positionTime else { return .commandFailed }
            return self?.perform(.seek(to)) ?? .commandFailed
        }
        enable()
    }

    private func enable() {
        let h = handlers
        commands.playCommand.isEnabled = h != nil
        commands.pauseCommand.isEnabled = h != nil
        commands.togglePlayPauseCommand.isEnabled = h != nil
        commands.skipForwardCommand.isEnabled = h?.skip != nil
        commands.skipBackwardCommand.isEnabled = h?.skip != nil
        commands.nextTrackCommand.isEnabled = h?.next != nil
        commands.changePlaybackPositionCommand.isEnabled = h?.seek != nil
    }

    private func unregister() {
        for (c, t) in targets { c.removeTarget(t) }
        targets = []
        enable()
    }
}
