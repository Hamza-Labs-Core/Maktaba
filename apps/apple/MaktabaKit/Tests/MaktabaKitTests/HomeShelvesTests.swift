import Foundation
import Testing
@testable import MaktabaKit

// The TV home's shelves (`HomeShelves`), from the mock home the UI tests serve
// (`apps/apple/UITests/Fixtures`): every row the Player's Home has, in its order, and a source
// that fails kept as a retry tile.
struct HomeShelvesTests {
    static let fixtures = URL(fileURLWithPath: #filePath)
        .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        .deletingLastPathComponent().appending(path: "UITests/Fixtures")

    /// A fixture as the mock home serves it: `{{now±s}}` is a time around now.
    static func fixture<T: Decodable>(_ name: String, as _: T.Type = T.self) throws -> T {
        var text = try String(contentsOf: fixtures.appending(path: "\(name).json"), encoding: .utf8)
        let now = Date()
        let pattern = try NSRegularExpression(pattern: #"\{\{now([+-]\d+)?\}\}"#)
        for m in pattern.matches(in: text, range: NSRange(text.startIndex..., in: text)).reversed() {
            let r = Range(m.range, in: text)!
            let offset = Range(m.range(at: 1), in: text).flatMap { Double(text[$0]) } ?? 0
            text.replaceSubrange(r, with: ISO8601DateFormatter().string(from: now.addingTimeInterval(offset)))
        }
        return try JSONDecoder.home.decode(T.self, from: Data(text.utf8))
    }

    struct Rails: Decodable { let rails: [Rail] }

    static func answers() throws -> (HomeRows, [Rail], [ChannelNow]) {
        (try fixture("home"), try fixture("recommendations", as: Rails.self).rails,
         try fixture("channels-now", as: ChannelsNow.self).channels)
    }

    @Test func everyRowOfThePlayersHomeInItsOrder() throws {
        let (home, rails, now) = try Self.answers()
        let shelves = HomeShelves.build(home: .success(home), rails: .success(rails), now: .success(now))
        #expect(shelves.map(\.key) == [
            HomeShelf.continueWatching, HomeShelf.upNext, HomeShelf.liveNow, HomeShelf.favorites,
            HomeShelf.recentlyAdded, HomeShelf.recentlyWatched, "pinned-lib-lectures",
        ] + rails.compactMap { r in
            HomeShelves.railKinds.first { $0.kind == r.reasonKind }?.key
        })
        #expect(shelves.count > 1, "more than Continue watching")
        guard case let .cards(cw) = shelves[0].content else { Issue.record("cards"); return }
        #expect(cw.map(\.videoID) == home.continueWatching.map(\.id))
        #expect(cw.allSatisfy { $0.title != nil })
        guard case let .channels(live) = shelves[2].content else { Issue.record("channels"); return }
        #expect(live.map(\.id) == now.map(\.id))
        #expect(shelves.contains { $0.kind == .library(id: "lib-lectures", name: "Lectures") })
    }

    @Test func recentlyAddedGroupsASeries() throws {
        let (home, _, _) = try Self.answers()
        let shelves = HomeShelves.build(home: .success(home), rails: .success([]), now: .success([]))
        guard let added = shelves.first(where: { $0.key == HomeShelf.recentlyAdded }),
              case let .cards(cards) = added.content else { Issue.record("Recently added"); return }
        #expect(cards.count == home.recentlyAddedGrouped.count)
        let series = home.recentlyAddedGrouped.filter { $0.kind == "series" }
        #expect(cards.filter { $0.seriesID != nil }.count == series.count)
    }

    @Test func aFailedRowStaysAsARetryTile() throws {
        let (home, rails, _) = try Self.answers()
        let down = APIError.transport("The network connection was lost.")
        let shelves = HomeShelves.build(home: .success(home), rails: .success(rails), now: .failure(down))
        let live = try #require(shelves.first { $0.key == HomeShelf.liveNow })
        guard case .failed = live.content else { Issue.record("a retry tile, not a vanished row"); return }
        #expect(shelves.firstIndex { $0.key == HomeShelf.liveNow } == 2, "where the row would be")

        let noRecs = HomeShelves.build(home: .success(home), rails: .failure(down), now: .success([]))
        #expect(noRecs.last?.key == HomeShelf.recommendations)
        let noHome = HomeShelves.build(home: .failure(down), rails: .success(rails), now: .success([]))
        #expect(noHome.first?.key == HomeShelf.home)
        guard case .failed = noHome.first?.content else { Issue.record("home's retry tile"); return }
    }

    @Test func noLiveTVIsNoShelf() throws {
        let (home, _, _) = try Self.answers()
        let shelves = HomeShelves.build(home: .success(home), rails: .success([]),
                                        now: .failure(APIError.status(403, "{}")))
        #expect(!shelves.contains { $0.key == HomeShelf.liveNow })
    }

    @Test func emptyRowsAreLeftOut() throws {
        let empty = try JSONDecoder.home.decode(HomeRows.self, from: Data("{}".utf8))
        #expect(HomeShelves.build(home: .success(empty), rails: .success([]), now: .success([])).isEmpty)
    }

    @Test func loadAsksAllThreeTogether() async throws {
        let (home, rails, _) = try Self.answers()
        let now = try Self.fixture("channels-now", as: ChannelsNow.self)
        let shelves = await HomeShelves.load(home: { home }, rails: { rails },
                                             now: { throw APIError.transport("down") })
        #expect(shelves.contains { $0.key == HomeShelf.recentlyAdded })
        #expect(shelves.contains { $0.key == HomeShelf.liveNow && $0.content != .channels(now.channels) })
    }
}
