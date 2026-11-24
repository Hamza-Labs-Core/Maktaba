import Foundation
import Testing
@testable import MaktabaKit

/// Story 13.16 (the desktop design's D5): the Mac Player keeps the language the person chose
/// in its own defaults, with the right-to-left layout for Arabic, and starts again in it.
@Suite struct LanguageChoiceTests {
    private func defaults(_ name: String) throws -> UserDefaults {
        let suite = "test.languageChoice.\(name)"
        let d = try #require(UserDefaults(suiteName: suite))
        d.removePersistentDomain(forName: suite)
        return d
    }

    @Test func theLanguageShownIsTheBundlesFirstLocalization() {
        #expect(LanguageChoice.current(["ar"]) == .arabic)
        #expect(LanguageChoice.current(["ar-JO", "en"]) == .arabic)
        #expect(LanguageChoice.current(["en"]) == .english)
        #expect(LanguageChoice.current(["en-GB", "ar"]) == .english)
        // a language the app does not speak, or none: English, its development language
        #expect(LanguageChoice.current(["fr"]) == .english)
        #expect(LanguageChoice.current([]) == .english)
    }

    @Test func arabicIsKeptWithTheRightToLeftLayout() throws {
        let d = try defaults("arabic")
        LanguageChoice.arabic.keep(in: d)
        #expect(d.stringArray(forKey: LanguageChoice.languagesKey) == ["ar"])
        #expect(d.bool(forKey: LanguageChoice.directionKey))
        #expect(LanguageChoice.languagesKey == "AppleLanguages")
        #expect(LanguageChoice.directionKey == "AppleTextDirection")
    }

    @Test func englishTakesTheRightToLeftLayoutAway() throws {
        let d = try defaults("english")
        LanguageChoice.arabic.keep(in: d)
        LanguageChoice.english.keep(in: d)
        #expect(d.stringArray(forKey: LanguageChoice.languagesKey) == ["en"])
        // the app's own domain no longer holds it (a read through `d` also sees the system's)
        let own = d.persistentDomain(forName: "test.languageChoice.english") ?? [:]
        #expect(own[LanguageChoice.directionKey] == nil, "no right-to-left layout left behind: \(own)")
    }

    @Test func onlyANewLanguageStartsTheAppAgain() {
        #expect(LanguageChoice.arabic.needsRelaunch(from: .english))
        #expect(LanguageChoice.english.needsRelaunch(from: .arabic))
        #expect(!LanguageChoice.english.needsRelaunch(from: .english))
        #expect(!LanguageChoice.arabic.needsRelaunch(from: .arabic))
    }

    @Test func eachLanguageIsNamedInItself() {
        #expect(LanguageChoice.english.name == "English")
        #expect(LanguageChoice.arabic.name == "العربية")
        #expect(LanguageChoice.arabic.isRightToLeft && !LanguageChoice.english.isRightToLeft)
        #expect(LanguageChoice.allCases.map(\.locale.identifier) == ["en", "ar"])
    }
}
