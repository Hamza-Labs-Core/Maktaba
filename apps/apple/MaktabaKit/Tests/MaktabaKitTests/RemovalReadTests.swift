import Foundation
import Testing
@testable import MaktabaKit

/// A removal or toggle saved while its list is read again stays: that read was answered
/// before the save, so it still has the removed row (the old pin) and must not put it back on
/// screen while the home has let it go. Each test holds the read until the change is saved.
extension BrowseClient {
    @Suite struct RemovalReads {
        init() { BrowseStub.reset() }

        private static let history = #"""
        {"items":[
         {"video_id":"\#(BrowseHome.v2)","title":"one","duration_sec":3000,"position_sec":0,"completed":true,"last_watched_at":"2026-10-31T23:49:13Z"},
         {"video_id":"\#(BrowseHome.v4)","title":"two","duration_sec":3000,"position_sec":10,"completed":false,"last_watched_at":"2026-10-30T23:49:13Z"}
        ],"limit":50,"offset":0}
        """#

        private static func sessions(_ ids: [String]) -> String {
            let rows = ids.map {
                #"{"id":"\#($0)","created_at":"2026-10-30T10:00:00Z","last_seen_at":"2026-10-31T10:00:00Z","expires_at":"2026-11-30T10:00:00Z","ip":null,"user_agent":"Safari","current":false}"#
            }
            return #"{"items":[\#(rows.joined(separator: ","))]}"#
        }

        private static func recent(_ ids: [String]) -> String {
            let rows = ids.map {
                #"{"search_id":"\#($0)","q":"q \#($0)","mode":"hybrid","live":true,"created_at":"2026-10-31T23:49:13Z","last_used_at":"2026-10-31T23:49:13Z","expires_at":"2026-11-01T00:04:13Z","title_count":1,"transcript_count":0}"#
            }
            return #"{"items":[\#(rows.joined(separator: ","))]}"#
        }

        @MainActor private static func model() -> ProfileModel { ProfileModel(client: BrowseHome.client().0, signedOut: {}) }

        /// History's Forget while the history is read again (the page shown again).
        @MainActor @Test func aHistoryRowForgottenWhileTheHistoryIsReadAgainStaysGone() async throws {
            BrowseStub.on("GET", "/api/me/history", (200, Self.history))
            BrowseStub.on("DELETE", "/api/me/history/\(BrowseHome.v2)", (204, ""))
            let model = Self.model()
            await model.loadHistory()
            let row = try #require(model.history.value?.first { $0.videoID == BrowseHome.v2 })

            let read = BrowseStub.hold("GET", "/api/me/history")
            let reading = Task { await model.loadHistory() }
            #expect(await read.waitUntilAsked())
            await model.forget(row)
            #expect(model.problem == nil && model.history.value?.map(\.videoID) == [BrowseHome.v4])
            read.letGo()
            await reading.value

            #expect(model.history.value?.map(\.videoID) == [BrowseHome.v4], "the forgotten row stays gone")
        }

        /// Load more answered after a row was forgotten adds to the rows shown now.
        @MainActor @Test func loadMoreAnsweredAfterARowIsForgottenDoesNotPutItBack() async throws {
            BrowseStub.on("GET", "/api/me/history", (200, Self.history))
            BrowseStub.on("DELETE", "/api/me/history/\(BrowseHome.v2)", (204, ""))
            let model = Self.model()
            await model.loadHistory()
            let row = try #require(model.history.value?.first { $0.videoID == BrowseHome.v2 })

            let read = BrowseStub.hold("GET", "/api/me/history")
            let more = Task { await model.moreHistory() }
            #expect(await read.waitUntilAsked())
            await model.forget(row)
            read.letGo()
            await more.value

            #expect(model.history.value?.map(\.videoID) == [BrowseHome.v4], "the forgotten row stays gone")
        }

        /// Signing a session out while the sessions are read again.
        @MainActor @Test func aSessionSignedOutWhileTheSessionsAreReadAgainStaysGone() async throws {
            BrowseStub.on("GET", "/api/me/sessions", (200, Self.sessions(["s1", "s2"])))
            BrowseStub.on("DELETE", "/api/me/sessions/s1", (204, ""))
            let model = Self.model()
            await model.loadSessions()
            let s1 = try #require(model.sessions.value?.first { $0.id == "s1" })

            let read = BrowseStub.hold("GET", "/api/me/sessions")
            let reading = Task { await model.loadSessions() }
            #expect(await read.waitUntilAsked())
            await model.end(s1)
            #expect(model.problem == nil && model.sessions.value?.map(\.id) == ["s2"])
            read.letGo()
            await reading.value

            #expect(model.sessions.value?.map(\.id) == ["s2"], "the signed-out session stays gone")
        }

        /// A recent search forgotten (or all cleared) while the recent searches are read again
        /// (after a search, or Search shown again).
        @MainActor @Test(arguments: [false, true])
        func recentSearchesForgottenWhileTheyAreReadAgainStayGone(clearAll: Bool) async throws {
            BrowseStub.on("GET", "/api/search/recent", (200, Self.recent(["s1", "s2"])))
            BrowseStub.on("DELETE", "/api/search/recent/s1", (204, ""))
            BrowseStub.on("DELETE", "/api/search/recent", (204, ""))
            let model = SearchModel(client: BrowseHome.client().0, signedOut: {})
            await model.loadRecent()
            let s1 = try #require(model.recent.first { $0.searchID == "s1" })

            let read = BrowseStub.hold("GET", "/api/search/recent")
            let reading = Task { await model.loadRecent() }
            #expect(await read.waitUntilAsked())
            if clearAll { await model.clearRecent() } else { await model.forget(s1) }
            let want = clearAll ? [] : ["s2"]
            #expect(model.recent.map(\.searchID) == want)
            read.letGo()
            await reading.value

            #expect(model.recent.map(\.searchID) == want, "the forgotten searches stay gone")
        }

        /// Not interested while Home is read again (Home shown again, or pulled to refresh).
        @MainActor @Test func notInterestedWhileHomeIsReadAgainKeepsTheCardOut() async throws {
            let rails = #"{"rails":[{"id":"r1","reason_kind":"for_you","title":"For you","items":[{"video_id":"\#(BrowseHome.v4)"},{"video_id":"\#(BrowseHome.v9)"}]}]}"#
            try BrowseHome.answer("/api/home", "home")
            BrowseStub.on("GET", "/api/recommendations", (200, rails))
            BrowseStub.on("GET", "/api/channels/now", (404, ""))
            BrowseStub.on("DELETE", "/api/recommendations/items/\(BrowseHome.v4)", (204, ""))
            let model = HomeModel(client: BrowseHome.client().0, cache: nil, signedOut: {})
            await model.load()
            func recommended() -> [String] { model.shelves.filter(\.isRecommendation).flatMap(\.cards).map(\.videoID) }
            #expect(recommended() == [BrowseHome.v4, BrowseHome.v9])

            let read = BrowseStub.hold("GET", "/api/recommendations")
            let reading = Task { await model.load() }
            #expect(await read.waitUntilAsked())
            #expect(await model.dismiss(BrowseHome.v4))
            read.letGo()
            await reading.value

            #expect(recommended() == [BrowseHome.v9], "the card dismissed while Home was read again stays out")
        }

        /// A library pinned while the libraries are read again (the section shown again).
        @MainActor @Test func aPinWhileTheLibrariesAreReadAgainStays() async throws {
            try BrowseHome.answer("/api/libraries/summary", "libraries-summary")
            BrowseStub.on("PUT", "/api/me/pinned-libraries", (200, #"{"items":[],"library_ids":[]}"#))
            let model = LibrariesModel(client: BrowseHome.client().0, signedOut: {})
            await model.loadLibraries()
            #expect(model.isPinned(BrowseHome.lib))

            let read = BrowseStub.hold("GET", "/api/libraries/summary")
            let reading = Task { await model.loadLibraries() }
            #expect(await read.waitUntilAsked())
            await model.togglePin(BrowseHome.lib)
            #expect(model.problem == nil && !model.isPinned(BrowseHome.lib))
            read.letGo()
            await reading.value

            #expect(!model.isPinned(BrowseHome.lib), "the library unpinned while the libraries were read again stays unpinned")
        }
    }

    /// The order itself, as the web's and Android's.
    @Suite struct ReadOrders {
        @MainActor @Test func aReadThatBeganBeforeAChangeIsDropped() {
            let o = ReadOrder()
            let t = o.begin()
            o.changed()
            #expect(!o.fresh(t))
            #expect(o.fresh(o.begin()))
        }

        @MainActor @Test func anOlderAnswerIsDroppedNeverANewerOne() {
            let o = ReadOrder()
            let a = o.begin()
            let b = o.begin()
            #expect(o.fresh(b))
            #expect(!o.fresh(a))
            #expect(o.fresh(o.begin()))
        }

        @MainActor @Test func noReadAppliesWhileASaveIsUnderWay() async throws {
            let o = ReadOrder()
            var during = 0
            let saved = await o.saving { () async -> String in
                during = o.begin()
                #expect(!o.fresh(during))
                return "ok"
            }
            #expect(saved == "ok")
            #expect(!o.fresh(during))
            #expect(o.fresh(o.begin()))
        }

        @MainActor @Test func aFailedSaveStillMakesOlderReadsStale() async {
            struct No: Error {}
            let o = ReadOrder()
            let t = o.begin()
            await #expect(throws: No.self) { try await o.saving { throw No() } }
            #expect(!o.fresh(t))
        }
    }
}
