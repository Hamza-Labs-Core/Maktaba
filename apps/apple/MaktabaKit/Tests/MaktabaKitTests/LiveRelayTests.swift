import AVFoundation
import Foundation
import Testing
@testable import MaktabaKit

/// A Cloudflare-like origin: answers playlists by path, records every URL asked.
actor FakeRelayOrigin {
    private(set) var asked: [URL] = []
    var answers: [String: String]

    init(_ answers: [String: String]) { self.answers = answers }

    nonisolated var fetch: LiveRelayLoader.Fetch {
        { [self] url in try await self.answer(url) }
    }

    private func answer(_ url: URL) throws -> (Data, Int) {
        asked.append(url)
        // the path after the token segment
        let rest = url.path().split(separator: "/").dropFirst().joined(separator: "/")
        guard let body = answers[rest] else { return (Data("not found".utf8), 404) }
        return (Data(body.utf8), 200)
    }
}

/// Story 48.21: away from home, a heartbeat's new token reaches the stream that is already
/// playing (the player keeps its item; the next playlist reload carries the new token).
@Suite struct LiveRelayKeepsTheTokenFresh {
    static let cf = LiveFixtures.cf
    static let master = """
        #EXTM3U
        #EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="a",NAME="ar",URI="stream_a.m3u8?lang=ar"
        #EXT-X-STREAM-INF:BANDWIDTH=800000,AUDIO="a"
        stream_t1.m3u8?x=1
        """
    static let media = """
        #EXTM3U\r
        #EXT-X-TARGETDURATION:6\r
        #EXT-X-MAP:URI="init.mp4"\r
        #EXTINF:6.0,\r
        seg_1.ts\r
        #EXTINF:6.0,\r
        \(cf)/tok1/manifest/seg_2.ts\r

        """

    /// Tunes away, plays through the loader, beats on a fake clock: after the beat that
    /// brings `tok2`, the same asset's next reload of its variant and every segment it
    /// lists carry `tok2`; the asset is never replaced; leaving ends the session.
    @Test func aHeartbeatsTokenReachesThePlayingStream() async throws {
        let clock = FakeClock()
        let opened = URL(string: "\(Self.cf)/tok1/manifest/video.m3u8")!
        let api = FakeLiveAPI(info: LiveSourcePicksAndSwaps.relay,
                              tunes: [.success(RemoteTune(sessionID: "s1", state: "live", hlsURL: opened))],
                              beats: [.success(RemoteBeat(state: "live", token: "tok2"))])
        let origin = FakeRelayOrigin([
            "manifest/video.m3u8": Self.master,
            "manifest/stream_t1.m3u8": Self.media,
            "manifest/stream_a.m3u8": Self.media,
        ])
        let source = LiveSource(api: api, channel: "c1", sleep: clock.sleep)
        let (url, path) = try await source.open()
        #expect(path == .relay)

        let loader = LiveRelayLoader(url: url, fetch: origin.fetch)
        let asset = loader.makeAsset()
        #expect(asset.url.scheme == "maktaba-relay+https", "the player asks the loader, not the origin")
        #expect(asset.resourceLoader.delegate === loader)
        let following = Task { await loader.follow(source) }

        // the master: its variant and its audio come back to the loader, with tok1
        let master = String(decoding: try await loader.playlist(for: asset.url), as: UTF8.self)
        let variant = "maktaba-relay+https://customer-abc.cloudflarestream.com/tok1/manifest/stream_t1.m3u8?x=1"
        #expect(master.contains("\n\(variant)"))
        #expect(master.contains(#"URI="maktaba-relay+https://customer-abc.cloudflarestream.com/tok1/manifest/stream_a.m3u8?lang=ar""#))
        // the variant: segments straight from the origin, with tok1
        let first = String(decoding: try await loader.playlist(for: URL(string: variant)!), as: UTF8.self)
        #expect(first.contains("\(Self.cf)/tok1/manifest/seg_1.ts\r\n"), "the line ending kept")
        #expect(first.contains("\(Self.cf)/tok1/manifest/seg_2.ts\r\n"))
        #expect(first.contains(#"URI="\#(Self.cf)/tok1/manifest/init.mp4""#))
        #expect(await origin.asked.allSatisfy { $0.path().hasPrefix("/tok1/") })

        // 60 s on the fake clock: the heartbeat brings tok2
        await eventually("the heartbeat waits") { await clock.waiting == 1 }
        await clock.advance(by: .seconds(59))
        #expect(await api.beatCount == 0)
        await clock.advance(by: .seconds(1))
        await eventually("one beat") { await api.beatCount == 1 }
        await eventually("the loader has tok2") { loader.token == "tok2" }

        // the player reloads the same variant URL it was given (it still names tok1): the
        // origin is asked with tok2, and the segments it lists carry tok2
        let reload = String(decoding: try await loader.playlist(for: URL(string: variant)!), as: UTF8.self)
        #expect(await origin.asked.last?.absoluteString == "\(Self.cf)/tok2/manifest/stream_t1.m3u8?x=1")
        #expect(reload.contains("\(Self.cf)/tok2/manifest/seg_1.ts\r\n"), "the line ending kept")
        #expect(reload.contains("\(Self.cf)/tok2/manifest/seg_2.ts\r\n"), "an absolute segment gets the new token too")
        #expect(!reload.contains("tok1"))
        // the master asked again is the newest signed URL
        _ = try await loader.playlist(for: asset.url)
        #expect(await origin.asked.last?.absoluteString == "\(Self.cf)/tok2/manifest/video.m3u8")

        // one session, one tune, the asset unchanged
        #expect(await api.tuneCount == 1)
        #expect(loader.assetURL == asset.url)

        // leaving: the session ends, the follower returns (not ended by the home)
        await source.close()
        #expect(await api.ended == ["s1"])
        #expect(await following.value == false)
    }

    /// The home ending the session ends the follower with true (the player leaves).
    @Test func theHomeEndingTheSessionIsSaid() async throws {
        let clock = FakeClock()
        let opened = URL(string: "\(Self.cf)/tok1/manifest/video.m3u8")!
        let api = FakeLiveAPI(info: LiveSourcePicksAndSwaps.relay,
                              tunes: [.success(RemoteTune(sessionID: "s1", state: "live", hlsURL: opened))],
                              beats: [.success(RemoteBeat(state: "ended"))])
        let source = LiveSource(api: api, channel: "c1", sleep: clock.sleep)
        _ = try await source.open()
        let loader = LiveRelayLoader(url: opened, fetch: FakeRelayOrigin([:]).fetch)
        let following = Task { await loader.follow(source) }
        await eventually("the heartbeat waits") { await clock.waiting == 1 }
        await clock.advance(by: .seconds(60))
        #expect(await following.value == true)
    }

    /// A refused playlist fails the request with the origin's status (AVPlayer says it).
    @Test func aRefusedPlaylistFails() async throws {
        let loader = LiveRelayLoader(url: URL(string: "\(Self.cf)/tok1/manifest/video.m3u8")!,
                                     fetch: FakeRelayOrigin([:]).fetch)
        await #expect(throws: APIError.self) { try await loader.playlist(for: loader.assetURL) }
        await #expect(throws: URLError.self) { try await loader.playlist(for: URL(string: "https://elsewhere.test/x.m3u8")!) }
    }

    /// A URL that is not a Cloudflare Stream URL (the mock home's relative relay URL) passes
    /// through unchanged, its playlists still through the loader.
    @Test func anotherHostKeepsItsURLs() {
        let base = URL(string: "http://127.0.0.1:8088/stream/remote/rs-1/index.m3u8")!
        let out = LiveRelayLoader.rewrite(playlist: "#EXTM3U\nlive.m3u8\nseg1.ts\n", base: base, token: "tok9")
        #expect(out == "#EXTM3U\nmaktaba-relay+http://127.0.0.1:8088/stream/remote/rs-1/live.m3u8\nhttp://127.0.0.1:8088/stream/remote/rs-1/seg1.ts\n")
        #expect(LiveRelayLoader.unwrap(LiveRelayLoader.wrap(base)) == base)
        #expect(LiveRelayLoader.unwrap(base) == nil)
    }

    @Test func theSameResourceWhateverItsToken() {
        let a = URL(string: "\(Self.cf)/tok1/manifest/video.m3u8")!
        #expect(LiveRelayLoader.sameResource(a, URL(string: "\(Self.cf)/tok2/manifest/video.m3u8")!))
        #expect(!LiveRelayLoader.sameResource(a, URL(string: "\(Self.cf)/tok1/manifest/stream_t1.m3u8")!))
    }
}
