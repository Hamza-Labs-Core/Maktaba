import Foundation
import Testing
@testable import MaktabaKit

// The player's stream session (story 48.23): a quality choice caps the session with
// `max_bitrate_kbps`, and the session's ladder is read for the Quality menu. Runs inside
// `BrowseClient`'s serialized suite, as it shares its stub.
extension BrowseClient {
    @Suite struct StreamSessions {
        init() { BrowseStub.reset() }

        static let session = #"""
        {"session_id":"ss-1","mode":"transcode","manifest_url":"/stream/ss-1/manifest.m3u8?sig=x",
         "expires_at":"2026-11-01T12:00:00Z",
         "ladder":[{"height":1080,"width":1920,"bitrate_kbps":6000,"codec":"h264"},
                   {"height":720,"width":1280,"bitrate_kbps":3000,"codec":"h264"}],
         "current_rendition":{"height":1080,"width":1920,"bitrate_kbps":6000,"codec":"h264"}}
        """#

        @Test func aQualityCapsTheStreamAndTheLadderIsRead() async throws {
            BrowseStub.on("POST", "/api/stream/sessions", (201, Self.session))
            let s = try await BrowseHome.client().0.openStream(video: BrowseHome.v1, startSec: 754, maxBitrateKbps: 3000)
            let sent = try #require(BrowseStub.last?.json)
            #expect(sent["video_id"] as? String == BrowseHome.v1)
            #expect(sent["start_sec"] as? Int == 754)
            #expect(sent["max_bitrate_kbps"] as? Int == 3000)
            #expect(s.ladder?.map(\.height) == [1080, 720])
            #expect(s.ladder?.last == Rendition(height: 720, width: 1280, bitrateKbps: 3000, codec: "h264"))
            #expect(s.currentRendition?.bitrateKbps == 6000)
        }

        @Test func noQualityLetsTheHomeChooseAndNoLadderReads() async throws {
            BrowseStub.on("POST", "/api/stream/sessions", (201, #"{"session_id":"ss-2","mode":"direct","direct_url":"/stream/ss-2/file"}"#))
            let s = try await BrowseHome.client().0.openStream(video: BrowseHome.v1)
            let sent = try #require(BrowseStub.last?.json)
            #expect(sent["max_bitrate_kbps"] == nil && sent["start_sec"] == nil)
            #expect(s.ladder == nil && s.currentRendition == nil)
        }
    }
}
