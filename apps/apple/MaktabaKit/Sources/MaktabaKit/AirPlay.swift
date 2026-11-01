import AVFoundation
import AVKit
import Darwin
import SwiftUI

// AirPlay (Epic 48 story 48.24, FR-38). A receiver (an Apple TV) is handed the URL and
// fetches the stream itself, so it can't read the app's loopback proxy:
// - a video gets the session's signed URL, which the home's web port lets through without a
//   token (story 48.12) and whose `?sig=` the streaming service copies onto every child URI;
// - a live channel at home gets the proxy on the device's LAN address (`StreamProxy.Reach`),
//   serving that channel only: the web port opens channel paths to a signed-in caller only;
// - a live channel away gets the remote session's signed Cloudflare URL itself (the relay
//   loader's private scheme can't leave the device either), handed again on each new token.
// The device keeps reporting progress (and, away, the heartbeat): AVPlayer's time is the
// receiver's while it plays there.

/// Where the picture plays.
public enum PlaybackRoute: String, Sendable, Equatable {
    /// On this device (its screen, its speakers, AirPlay audio alone).
    case device
    /// On an AirPlay receiver that fetches the stream itself.
    case receiver

    public init(externalPlaybackActive: Bool) {
        self = externalPlaybackActive ? .receiver : .device
    }
}

/// Follows the player's external playback and says when the stream must be handed over.
public struct RouteSwitch: Sendable, Equatable {
    public private(set) var route = PlaybackRoute.device

    public init() {}

    /// The player's `isExternalPlaybackActive` now: the route to switch to, nil when the item
    /// already plays for this route.
    public mutating func update(externalPlaybackActive: Bool) -> PlaybackRoute? {
        let now = PlaybackRoute(externalPlaybackActive: externalPlaybackActive)
        guard now != route else { return nil }
        route = now
        return now
    }
}

/// The URL an item opens, for where it plays.
public enum RouteURL {
    /// A video: the proxy's loopback address on the device; the signed URL on a receiver.
    public static func video(signed: URL, proxied: URL, route: PlaybackRoute) -> URL {
        route == .receiver ? signed : proxied
    }

    /// What a live channel's item opens.
    public enum Live: Equatable, Sendable {
        /// This URL as it is.
        case url(URL)
        /// The remote session through `LiveRelayLoader` (its private scheme; the device only).
        case relayLoader
    }

    /// A live channel: at home the loopback proxy, or on a receiver the LAN proxy (nil when the
    /// device has no LAN address: the item then stays on the device's proxy and the receiver
    /// fails it, which the player says); away the relay loader, or on a receiver the newest
    /// signed URL.
    public static func live(path: LivePath, route: PlaybackRoute, proxied: URL?, lanProxied: URL?, relay: URL?) -> Live? {
        switch (path, route) {
        case (.lan, .device): proxied.map(Live.url)
        case (.lan, .receiver): (lanProxied ?? proxied).map(Live.url)
        case (.relay, .device): .relayLoader
        case (.relay, .receiver): relay.map(Live.url)
        }
    }

    /// The home path a channel's LAN proxy serves: the directory of its playlist
    /// (`/stream/channel/{id}/`), so its variants and segments pass and nothing else does.
    public static func channelScope(_ playlist: URL) -> String {
        let path = playlist.path(percentEncoded: true)
        guard let slash = path.lastIndex(of: "/") else { return path }
        return String(path[...slash])
    }
}

/// The device's address on the local network, for an AirPlay receiver to reach the LAN proxy.
public enum LANAddress {
    /// One interface's IPv4 address.
    public struct Interface: Equatable, Sendable {
        public let name: String
        public let address: String

        public init(name: String, address: String) {
            self.name = name
            self.address = address
        }
    }

    /// The Wi-Fi or Ethernet IPv4 address (nil: none, e.g. cellular only).
    public static func current() -> String? { pick(from: interfaces()) }

    /// The best of `all`: a private IPv4 address on an `en` interface (`en0`, Wi-Fi on an
    /// iPhone, first); never a loopback, link-local or cellular one.
    public static func pick(from all: [Interface]) -> String? {
        let usable = all.filter { $0.name.hasPrefix("en") && isPrivate($0.address) }
        return (usable.first { $0.name == "en0" } ?? usable.first)?.address
    }

    static func isPrivate(_ a: String) -> Bool {
        let p = a.split(separator: ".").compactMap { Int($0) }
        guard p.count == 4 else { return false }
        return p[0] == 10 || (p[0] == 172 && (16 ... 31).contains(p[1])) || (p[0] == 192 && p[1] == 168)
    }

    static func interfaces() -> [Interface] {
        var head: UnsafeMutablePointer<ifaddrs>?
        guard getifaddrs(&head) == 0, let first = head else { return [] }
        defer { freeifaddrs(head) }
        var out: [Interface] = []
        for p in sequence(first: first, next: { $0.pointee.ifa_next }) {
            let i = p.pointee
            let up = (Int32(i.ifa_flags) & IFF_UP) != 0 && (Int32(i.ifa_flags) & IFF_RUNNING) != 0
            guard up, let sa = i.ifa_addr, sa.pointee.sa_family == UInt8(AF_INET) else { continue }
            var host = [CChar](repeating: 0, count: Int(NI_MAXHOST))
            guard getnameinfo(sa, socklen_t(sa.pointee.sa_len), &host, socklen_t(host.count), nil, 0, NI_NUMERICHOST) == 0 else { continue }
            let address = host.withUnsafeBufferPointer { String(cString: $0.baseAddress!) }
            out.append(Interface(name: String(cString: i.ifa_name), address: address))
        }
        return out
    }
}

/// Calls back on the main actor when a player's external playback turns on or off.
@MainActor
public final class ExternalPlaybackWatcher {
    private var observation: NSKeyValueObservation?

    public init(_ player: AVPlayer, changed: @escaping @MainActor (Bool) -> Void) {
        observation = player.observe(\.isExternalPlaybackActive, options: [.initial, .new]) { p, _ in
            let active = p.isExternalPlaybackActive
            Task { @MainActor in changed(active) }
        }
    }

    deinit { observation?.invalidate() }
}

extension AVPlayer {
    /// Lets AirPlay take the video to a receiver (not just mirror the screen or send the sound).
    public func allowAirPlay() {
        allowsExternalPlayback = true
        #if os(iOS)
        usesExternalPlaybackWhileExternalScreenIsActive = true
        #endif
    }
}

#if !os(tvOS)
/// The AirPlay route button (`AVRoutePickerView`): the system's list of receivers. `label` is
/// what VoiceOver reads (the app's catalog, English and Arabic).
public struct AirPlayButton {
    let player: AVPlayer
    let label: String
    let identifier: String

    public init(player: AVPlayer, label: String, identifier: String) {
        self.player = player
        self.label = label
        self.identifier = identifier
    }
}

#if os(macOS)
extension AirPlayButton: NSViewRepresentable {
    public func makeNSView(context: Context) -> AVRoutePickerView {
        let v = AVRoutePickerView()
        v.isRoutePickerButtonBordered = false
        v.setRoutePickerButtonColor(.white, for: .normal)
        update(v)
        return v
    }

    public func updateNSView(_ v: AVRoutePickerView, context: Context) { update(v) }

    @MainActor private func update(_ v: AVRoutePickerView) {
        // the Mac lists video receivers only for a player it is given
        if v.player !== player { v.player = player }
        v.setAccessibilityLabel(label)
        v.setAccessibilityIdentifier(identifier)
    }
}
#elseif os(iOS) || os(visionOS)
extension AirPlayButton: UIViewRepresentable {
    public func makeUIView(context: Context) -> Picker {
        let v = Picker()
        v.prioritizesVideoDevices = true
        v.tintColor = .white
        update(v)
        return v
    }

    public func updateUIView(_ v: Picker, context: Context) { update(v) }

    @MainActor private func update(_ v: Picker) {
        v.label = label
        v.identifier = identifier
        v.setNeedsLayout()
    }

    /// The picker, its inner button named (it is the accessibility element, added by the
    /// system once the view lays out).
    public final class Picker: AVRoutePickerView {
        var label = ""
        var identifier = ""

        override public func layoutSubviews() {
            super.layoutSubviews()
            for b in subviews.compactMap({ $0 as? UIButton }) {
                b.accessibilityLabel = label
                b.accessibilityIdentifier = identifier
            }
        }
    }
}
#endif
#endif // !os(tvOS): the TV is the receiver, it has no route button
