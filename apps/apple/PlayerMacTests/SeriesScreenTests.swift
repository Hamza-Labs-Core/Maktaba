#if os(macOS)
import Foundation
import MaktabaKit
import Testing
@testable import Maktaba_Player_Mac

/// A series page's details line, its episode count and year, in the page's language.
@Suite struct SeriesScreenDetails {
    @Test func theDetailsSpeakThePagesLanguage() {
        #expect(SeriesScreen.details(episodes: 3, year: 2024, Locale(identifier: "en")) == "3 episodes · 2024")
        #expect(SeriesScreen.details(episodes: 3, year: 2024, Locale(identifier: "ar")) == "3 حلقات · 2024")
        #expect(SeriesScreen.details(episodes: 1, year: nil, Locale(identifier: "ar")) == "حلقة واحدة")
    }
}

/// A library's Collections tab badges a YouTube playlist and a smart collection; a YouTube
/// channel's collection has no source badge, and there is no Plex collection badge.
@Suite struct CollectionBadges {
    private func collection(kind: String, source: String?) throws -> LibraryCollection {
        let sk = source.map { ",\"source_kind\":\"\($0)\"" } ?? ""
        let json = "{\"kind\":\"\(kind)\",\"id\":\"c1\",\"name\":\"C\"\(sk)}"
        return try JSONDecoder().decode(LibraryCollection.self, from: Data(json.utf8))
    }

    @Test func onlyAPlaylistAndASmartCollectionHaveABadge() throws {
        #expect(CollectionsTab.badge(try collection(kind: "youtube", source: "youtube_playlist")) != nil)
        #expect(CollectionsTab.badge(try collection(kind: "smart", source: nil)) != nil)
        #expect(CollectionsTab.badge(try collection(kind: "youtube", source: "youtube_channel")) == nil)
        #expect(CollectionsTab.badge(try collection(kind: "curated", source: "plex")) == nil)
        #expect(CollectionsTab.badge(try collection(kind: "curated", source: nil)) == nil)
    }
}
#endif
