#if os(macOS)
import Foundation
import Testing
@testable import Maktaba_Player_Mac

/// A sentence a page builds as a string (a card's detail, a series' episodes and year) speaks
/// the language the page is drawn in, its `\.locale`, as its `Text`s do, not the process's.
@Suite struct PageLocale {
    private let ar = Locale(identifier: "ar")
    private let en = Locale(identifier: "en")

    @Test func aSentenceFollowsTheLocaleItIsGiven() {
        #expect(SeriesGrid.episodes(3).string(in: en) == "3 episodes")
        #expect(SeriesGrid.episodes(3).string(in: ar) == "3 حلقات")
        #expect(SeriesGrid.episodes(1).string(in: ar) == "حلقة واحدة")
        #expect(SeriesGrid.untitled.string(in: en) == "Untitled series")
        #expect(SeriesGrid.untitled.string(in: ar) != "Untitled series")
    }

    @Test func lengthsFollowTheLocale() {
        #expect(VideoCard.length(2700, en) == "45:00")
        #expect(VideoCard.length(3930, en) == "1:05:30")
        #expect(VideoCard.length(2700, ar) == "45:00")
    }
}
#endif
