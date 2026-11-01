import Foundation
import Testing
@testable import MaktabaKit

// AirPlay, Now Playing and the remote commands (story 48.24): which URL a receiver is handed
// (never the loopback proxy), when the item is switched, the LAN proxy that serves one channel
// only, and the commands routed to the player.
@Suite struct AirPlayRoutes {
    let signed = URL(string: "http://192.168.50.177:8088/stream/3f1c7a0e-0000-4000-8000-000000000001/manifest.m3u8?sig=abc")!
    let proxied = URL(string: "http://127.0.0.1:50123/secret/stream/3f1c7a0e-0000-4000-8000-000000000001/manifest.m3u8?sig=abc")!

    @Test func aVideoPlaysThroughTheProxyOnTheDeviceAndSignedOnAReceiver() {
        #expect(RouteURL.video(signed: signed, proxied: proxied, route: .device) == proxied)
        let handed = RouteURL.video(signed: signed, proxied: proxied, route: .receiver)
        #expect(handed == signed)
        #expect(handed.host() != "127.0.0.1", "a receiver cannot reach the device's loopback")
        #expect(handed.query()?.contains("sig=") == true, "the signed URL passes the gate without a token")
    }

    @Test func aChannelAtHomeGoesToTheLANProxyOnAReceiver() {
        let loop = URL(string: "http://127.0.0.1:50123/s/stream/channel/c/index.m3u8")!
        let lan = URL(string: "http://192.168.50.20:50124/t/stream/channel/c/index.m3u8")!
        #expect(RouteURL.live(path: .lan, route: .device, proxied: loop, lanProxied: lan, relay: nil) == .url(loop))
        #expect(RouteURL.live(path: .lan, route: .receiver, proxied: loop, lanProxied: lan, relay: nil) == .url(lan))
        // no LAN address (cellular only): it stays where it was and the receiver says it failed
        #expect(RouteURL.live(path: .lan, route: .receiver, proxied: loop, lanProxied: nil, relay: nil) == .url(loop))
    }

    @Test func aChannelAwayHandsTheReceiverTheSignedRelayURL() {
        let cf = URL(string: "https://customer-x.cloudflarestream.com/tok2/manifest/video.m3u8")!
        #expect(RouteURL.live(path: .relay, route: .device, proxied: nil, lanProxied: nil, relay: cf) == .relayLoader)
        #expect(RouteURL.live(path: .relay, route: .receiver, proxied: nil, lanProxied: nil, relay: cf) == .url(cf))
    }

    @Test func theChannelScopeIsItsPlaylistsDirectory() {
        let u = URL(string: "http://192.168.50.177:8088/stream/channel/0b5e/index.m3u8")!
        #expect(RouteURL.channelScope(u) == "/stream/channel/0b5e/")
    }

    @Test func theSwitchFiresOnlyWhenTheRouteChanges() {
        var s = RouteSwitch()
        #expect(s.update(externalPlaybackActive: false) == nil, "starts on the device")
        #expect(s.update(externalPlaybackActive: true) == .receiver)
        #expect(s.update(externalPlaybackActive: true) == nil, "already handed over")
        #expect(s.route == .receiver)
        #expect(s.update(externalPlaybackActive: false) == .device, "back to the proxy when AirPlay ends")
        #expect(s.update(externalPlaybackActive: false) == nil)
    }

    @Test func theLANAddressIsAPrivateWiFiOrEthernetOne() {
        let all = [
            LANAddress.Interface(name: "lo0", address: "127.0.0.1"),
            LANAddress.Interface(name: "pdp_ip0", address: "10.64.1.2"),   // cellular
            LANAddress.Interface(name: "en5", address: "169.254.3.4"),     // link-local
            LANAddress.Interface(name: "en2", address: "172.20.0.9"),
            LANAddress.Interface(name: "en0", address: "192.168.50.20"),
        ]
        #expect(LANAddress.pick(from: all) == "192.168.50.20", "Wi-Fi first")
        #expect(LANAddress.pick(from: Array(all.dropLast())) == "172.20.0.9")
        #expect(LANAddress.pick(from: Array(all.prefix(3))) == nil, "no LAN: no address")
        #expect(!LANAddress.isPrivate("8.8.8.8") && !LANAddress.isPrivate("172.32.0.1"))
    }
}

@Suite struct AirPlayLANProxy {
    let home = URL(string: "http://192.168.50.177:8088")!

    @Test func itServesOnlyTheChannelUnderItsSecretAtTheLANAddress() throws {
        let proxy = try StreamProxy(client: APIClient(home: home, tokens: TokenStore(app: "test.proxy.lan", home: home)),
                                    reach: .lan(host: "192.168.50.20"), onlyUnder: "/stream/channel/c1/")
        let s = proxy.secret
        #expect(proxy.remote(forTarget: "/\(s)/stream/channel/c1/index.m3u8")?.absoluteString
            == "http://192.168.50.177:8088/stream/channel/c1/index.m3u8")
        #expect(proxy.remote(forTarget: "/\(s)/stream/channel/c1/seg-12.ts") != nil)
        for refused in [
            "/\(s)/stream/channel/c2/index.m3u8",         // another channel
            "/\(s)/stream/3f1c7a0e/manifest.m3u8",         // a video
            "/stream/channel/c1/index.m3u8",               // no secret
            "/\(s)/stream/channel/c1/../c2/index.m3u8",
        ] {
            #expect(proxy.remote(forTarget: refused) == nil, "\(refused)")
        }
    }

    @Test func itsAddressIsTheLANOneNotTheLoopback() async throws {
        let proxy = try StreamProxy(client: APIClient(home: home, tokens: TokenStore(app: "test.proxy.lan2", home: home)),
                                    reach: .lan(host: "192.168.50.20"))
        try await proxy.start()
        defer { proxy.stop() }
        let local = try #require(proxy.local(home.appending(path: "/stream/channel/c1/index.m3u8")))
        #expect(local.host() == "192.168.50.20")
        #expect(local.path().hasPrefix("/\(proxy.secret)/stream/channel/c1/"))
    }
}

@MainActor @Suite(.serialized) struct RemoteCommands {
    final class Owner {}

    @Test func theCommandsReachTheOwnersPlayer() {
        let np = NowPlaying()
        let me = Owner()
        var did: [String] = []
        np.begin(me, handlers: RemoteHandlers(play: { did.append("play") }, pause: { did.append("pause") },
                                              toggle: { did.append("toggle") }, skip: { did.append("skip \(Int($0))") },
                                              next: nil, seek: { did.append("seek \(Int($0))") }))
        #expect(np.perform(.play) == .success)
        #expect(np.perform(.pause) == .success)
        #expect(np.perform(.toggle) == .success)
        #expect(np.perform(.skip(-10)) == .success)
        #expect(np.perform(.seek(600)) == .success)
        #expect(np.perform(.next) == .noSuchContent, "no next episode: Next is off")
        #expect(did == ["play", "pause", "toggle", "skip -10", "seek 600"])
        #expect(np.enabled["next"] == false && np.enabled["skipForward"] == true && np.enabled["seek"] == true)
        np.update(me, handlers: RemoteHandlers(play: {}, pause: {}, toggle: {}, next: { did.append("next") }))
        #expect(np.perform(.next) == .success && did.last == "next")
        #expect(np.enabled["next"] == true && np.enabled["skipForward"] == false, "a live channel has no skip")
        np.end(me)
        #expect(np.perform(.play) == .noActionableNowPlayingItem)
        #expect(np.enabled["play"] == false)
    }

    @Test func onlyTheOwnerUpdatesOrEnds() {
        let np = NowPlaying()
        let channel = Owner(), video = Owner()
        np.begin(channel, handlers: RemoteHandlers(play: {}, pause: {}, toggle: {}))
        np.begin(video, handlers: RemoteHandlers(play: {}, pause: {}, toggle: {}))
        np.show(video, NowPlayingItem(title: "الدرس ١١", duration: 3120, position: 1240, playing: true))
        np.show(channel, NowPlayingItem(title: "late channel", playing: true))
        np.end(channel) // the channel closing late
        #expect(np.isOwned(by: video))
        #expect(np.item?.title == "الدرس ١١")
        np.end(video)
    }

    @Test func theInfoHasTitlePositionAndLive() {
        let v = NowPlayingItem(title: "Seerah", subtitle: "Season 1 · Episode 11", duration: 3120, position: 1240, playing: true).info
        #expect(v["title"] as? String == "Seerah")
        #expect(v["artist"] as? String == "Season 1 · Episode 11")
        #expect(v["playbackDuration"] as? Double == 3120)
        #expect(v["MPNowPlayingInfoPropertyElapsedPlaybackTime"] as? Double == 1240)
        #expect(v["MPNowPlayingInfoPropertyPlaybackRate"] as? Double == 1)
        #expect(v["MPNowPlayingInfoPropertyIsLiveStream"] as? Bool == false)
        let live = NowPlayingItem(title: "Quran TV", subtitle: "Tafsir", playing: false).info
        #expect(live["MPNowPlayingInfoPropertyIsLiveStream"] as? Bool == true)
        #expect(live["playbackDuration"] == nil && live["MPNowPlayingInfoPropertyPlaybackRate"] as? Double == 0)
    }
}
