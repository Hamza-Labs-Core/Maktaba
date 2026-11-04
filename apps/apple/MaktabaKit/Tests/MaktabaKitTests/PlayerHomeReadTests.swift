import Foundation
import Testing
@testable import MaktabaKit

/// The Player's Home (`HomeModel`): Not interested saved while Home is read again stays, as
/// the removals of `RemovalReads`.
extension BrowseClient {
    @Suite struct PlayerHomeReads {
        init() { BrowseStub.reset() }

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
    }
}
