import Foundation
import Testing
@testable import MaktabaKit

/// The review of #467: one test per finding. Nested in `BrowseClient`, so it shares its one
/// stub one suite at a time.
extension BrowseClient {
    @Suite struct BrowseReview {
        init() { BrowseStub.reset() }

        /// 1. The grid nests the person's playback under `playback_state`.
        @Test func theGridReadsNestedPlayback() async throws {
            try BrowseHome.answer("/api/videos", "videos-page2")
            let page = try await BrowseHome.client().0.videos(VideoQuery(library: BrowseHome.lib, limit: 2))
            let v2 = try #require(page.items.first { $0.id == BrowseHome.v2 })
            #expect(v2.positionSec == 754.5 && v2.completed == false && v2.lastWatchedAt != nil)
            #expect(v2.progress == 754.5 / 3000)
            // a top-level value (Home's cards) wins over a nested one
            let json = #"{"id":"v","filename":"a","position_sec":10,"playback_state":{"position_sec":99,"completed":true}}"#
            let card = try JSONDecoder.home.decode(BrowseVideo.self, from: Data(json.utf8))
            #expect(card.positionSec == 10 && card.completed == true)
        }

        /// 2. Recently Added by series alone is something to show.
        @Test func groupedAddedAloneIsNotEmpty() throws {
            let json = #"{"recently_added_grouped":[{"kind":"video","video":{"id":"v","filename":"a.mp4"}}]}"#
            #expect(!(try JSONDecoder.home.decode(HomeRows.self, from: Data(json.utf8)).isEmpty))
        }

        /// 3 and 5. `favorite=false` is never sent (the home answers 400); the favorites
        /// filter is read on `/api/me/videos` (a joined machine refuses it on `/api/videos`).
        @Test func favoritesAskTheHomesOwnList() async throws {
            BrowseStub.on("GET", "/api/videos", (200, #"{"items":[],"next":null}"#))
            BrowseStub.on("GET", "/api/me/videos", (200, #"{"items":[],"next":null}"#))
            let c = BrowseHome.client().0
            _ = try await c.videos(VideoQuery(library: BrowseHome.lib, favorite: false))
            #expect(BrowseStub.last?.path == "/api/videos" && BrowseStub.last?.query["favorite"] == nil)
            _ = try await c.videos(VideoQuery(library: BrowseHome.lib, favorite: true))
            #expect(BrowseStub.last?.path == "/api/me/videos" && BrowseStub.last?.query["favorite"] == "true")
            #expect(BrowseStub.last?.query["library"] == BrowseHome.lib)
        }

        /// 4. A page the home sent short (hidden videos left out, or a row dropped here) is
        /// not the last: the next offset steps by the page's limit up to the total.
        @Test func searchPagesStepByTheLimit() throws {
            func page(offset: Int, limit: Int, total: Int, items: Int) throws -> SearchPage<Passage> {
                let rows = (0..<items).map { #"{"segment_id":\#($0),"start_sec":0}"# } + [#"{"bad":1}"#]
                let json = #"{"search_id":"s","offset":\#(offset),"limit":\#(limit),"total":\#(total),"items":[\#(rows.joined(separator: ","))]}"#
                return try JSONDecoder.home.decode(SearchPage<Passage>.self, from: Data(json.utf8))
            }
            #expect(try page(offset: 0, limit: 24, total: 50, items: 20).nextOffset == 24)
            #expect(try page(offset: 24, limit: 24, total: 50, items: 0).nextOffset == 48)
            #expect(try page(offset: 48, limit: 24, total: 50, items: 2).nextOffset == nil)
        }

        /// 6. The home pages `/api/series` by 60: every page is read, and one page can be asked.
        @Test func everySeriesPage() async throws {
            func page(_ from: Int, _ n: Int, bad: Bool = false) -> String {
                var rows = (from..<(from + n)).map { #"{"id":"s\#($0)","name":"S\#($0)"}"# }
                if bad { rows[rows.count - 1] = #"{"name":"no id"}"# }
                return #"{"items":[\#(rows.joined(separator: ","))]}"#
            }
            BrowseStub.on("GET", "/api/series", (200, page(0, 60)), (200, page(60, 60, bad: true)), (200, page(120, 5)))
            let c = BrowseHome.client().0
            let all = try await c.seriesList(library: BrowseHome.lib)
            // 60 + 59 readable + 5: the unreadable row still made page 1 a full page
            #expect(all.count == 124 && all.last?.id == "s124")
            #expect(BrowseStub.all.map { $0.query["page"] } == ["0", "1", "2"])
            #expect(BrowseStub.all.allSatisfy { $0.query["library_id"] == BrowseHome.lib })
            BrowseStub.on("GET", "/api/series", (200, page(120, 5)))
            #expect(try await c.seriesList(page: 2).count == 5)
            #expect(BrowseStub.last?.query == ["page": "2"])
        }

        /// 7. A hidden video's trickplay (403) is not available; missing thumbnails (404) are nil.
        @Test func aHiddenVideosTrickplay() async throws {
            let c = BrowseHome.client().0
            try BrowseHome.answer("/api/videos/\(BrowseHome.v9)/trickplay", "video-hidden-403", status: 403)
            let e = await BrowseClient.BrowseFailures.failure { _ = try await c.trickplay(video: BrowseHome.v9) }
            #expect(e as? BrowseError == .notAvailable)
            try BrowseHome.answer("/api/videos/\(BrowseHome.v1)/trickplay", "video-trickplay", status: 404)
            #expect(try await c.trickplay(video: BrowseHome.v1) == nil)
        }
    }
}
