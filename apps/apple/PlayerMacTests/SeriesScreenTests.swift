#if os(macOS)
import Foundation
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
#endif
