import Foundation
import Testing
@testable import MaktabaKit

/// Epic 56: the library view's answers, read as the home writes them.
struct LibraryView56Tests {
    func decode<T: Decodable>(_ t: T.Type, _ json: String) throws -> T {
        try JSONDecoder.home.decode(T.self, from: Data(json.utf8))
    }

    @Test func aCollectionCardAndItsQueueSource() throws {
        let topic = try decode(LibraryCollection.self, #"{"kind":"topic","id":"t1","name":"الصيام","videos":3}"#)
        #expect(topic.queueSource == QueueSource(kind: "collection", id: "topic:t1"))
        let yt = try decode(LibraryCollection.self, #"{"kind":"youtube","subkind":"playlist","id":"c9","name":"PL","source_kind":"youtube_playlist"}"#)
        #expect(yt.queueID == "c9" && yt.sourceKind == "youtube_playlist")
    }

    @Test func aSmartPlaylistHasItsRulesInWords() throws {
        let p = try decode(LibraryPlaylist.self, #"{"id":"p","name":"X","kind":"smart","smart_query":{"filters":{"speaker":"X","watched":"unwatched"},"sort":"created_at","limit":50}}"#)
        #expect(p.smart)
        #expect(p.rules == ["speaker: X", "watched: unwatched", "created_at", "≤ 50"])
    }

    @Test func aSeasonNamesItsRowAndTheGridViewIsALibraryQueue() throws {
        let e = try decode(SeriesEpisodes.self, #"{"seasons":[{"season":1,"id":"ss1","kind":"ongoing","episodes":[]}]}"#)
        #expect(e.seasons.first?.seasonID == "ss1" && e.seasons.first?.kind == "ongoing")
        var q = VideoQuery(library: "L1", sort: .title)
        q.watched = "unwatched"
        let src = try #require(q.queueSource)
        #expect(src.kind == "library" && src.id == "L1")
        #expect(src.filters == ["sort": "title", "order": "asc", "watched": "unwatched"])
        let g = try decode(GroupedPage.self, #"{"items":[{"kind":"series","id":"s1","name":"Tafsir","poster_video_id":"v1","episode_count":3},{"kind":"video","id":"v9","title":"Loose"}],"next":null}"#)
        #expect(g.items.map(\.label) == ["Tafsir", "Loose"] && g.items[0].isSeries && g.items[0].picture == "v1")
    }

    @Test func promotedRowsAndCategories() throws {
        let c = try decode(LibraryCategories.self, #"{"speakers":[{"value":"A","label":"A","count":4}],"topics":[],"languages":[],"years":[{"value":"2024","label":"2024","count":2}]}"#)
        #expect(c.speakers.first?.count == 4 && c.years.first?.value == "2024")
        let row = try decode(CollectionRow.self, #"{"collection":{"id":"c1","name":"Picked"},"items":[{"id":"v1","filename":"a.mp4"}]}"#)
        #expect(row.name == "Picked" && row.items.count == 1)
    }
}
