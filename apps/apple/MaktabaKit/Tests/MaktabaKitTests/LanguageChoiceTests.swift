import Foundation
import Testing
@testable import MaktabaKit

/// Story 13.16 (the desktop design's D5): the Mac Player keeps the language the person chose
/// in its own defaults, or none to follow the system's; at every launch AppKit's layout is set
/// from the language it starts in; a failed restart puts the defaults back exactly.
@Suite struct LanguageChoiceTests {
    private func defaults(_ name: String) throws -> (UserDefaults, String) {
        let suite = "test.languageChoice.\(name)"
        let d = try #require(UserDefaults(suiteName: suite))
        d.removePersistentDomain(forName: suite)
        return (d, suite)
    }

    /// The app's own values only (a read through the defaults also sees the system's).
    private func own(_ d: UserDefaults, _ suite: String) -> [String: Any] {
        d.persistentDomain(forName: suite) ?? [:]
    }

    @Test func theKeptChoiceIsTheAppsOwnFirstLanguageOrTheSystem() {
        #expect(LanguageChoice.kept(in: nil) == .system)
        #expect(LanguageChoice.kept(in: [:]) == .system)
        #expect(LanguageChoice.kept(in: ["AppleLanguages": ["ar"]]) == .arabic)
        #expect(LanguageChoice.kept(in: ["AppleLanguages": ["ar-JO", "en"]]) == .arabic)
        #expect(LanguageChoice.kept(in: ["AppleLanguages": ["en-GB"]]) == .english)
        #expect(LanguageChoice.kept(in: ["AppleLanguages": ["fr"]]) == .english, "a language the app does not speak: English")
    }

    @Test func aChoiceKeepsOnlyTheLanguagesAndSystemTakesThemAway() throws {
        let (d, suite) = try defaults("keep")
        LanguageChoice.arabic.keep(in: d)
        #expect(own(d, suite)["AppleLanguages"] as? [String] == ["ar"])
        #expect(own(d, suite)["AppleTextDirection"] == nil, "the layout is set at launch, not here")
        LanguageChoice.english.keep(in: d)
        #expect(own(d, suite)["AppleLanguages"] as? [String] == ["en"])
        LanguageChoice.system.keep(in: d)
        #expect(own(d, suite)["AppleLanguages"] == nil, "System follows the system's languages again")
        #expect(LanguageChoice.kept(in: own(d, suite)) == .system)
    }

    /// The layout follows the language the app starts in, however it was chosen: the system's
    /// per-app language writes only AppleLanguages, so an English launch after Arabic must not
    /// keep the right-to-left layout.
    @Test func everyLaunchSetsTheLayoutFromItsLanguage() throws {
        let (d, suite) = try defaults("launch")
        LanguageChoice.applyLayout(["ar"], to: d)
        #expect((own(d, suite)["AppleTextDirection"] as? NSNumber)?.boolValue == true)
        LanguageChoice.applyLayout(["en"], to: d)
        #expect(own(d, suite)["AppleTextDirection"] == nil, "no right-to-left layout left behind")
        LanguageChoice.applyLayout([], to: d)
        #expect(own(d, suite)["AppleTextDirection"] == nil)
        #expect(LanguageChoice.isRightToLeft(["ar-JO", "en"]) && !LanguageChoice.isRightToLeft(["en", "ar"]))
    }

    @Test func aFailedRestartPutsBackExactlyWhatWasThere() throws {
        let (d, suite) = try defaults("restore")
        // nothing set: restored to nothing set
        let none = LanguageDefaults(domain: own(d, suite))
        LanguageChoice.arabic.keep(in: d)
        d.set(true, forKey: "AppleTextDirection")
        none.restore(in: d)
        #expect(own(d, suite)["AppleLanguages"] == nil && own(d, suite)["AppleTextDirection"] == nil)
        // both set: restored to the same values
        d.set(["ar"], forKey: "AppleLanguages")
        d.set(true, forKey: "AppleTextDirection")
        let both = LanguageDefaults(domain: own(d, suite))
        #expect(both == LanguageDefaults(domain: ["AppleLanguages": ["ar"], "AppleTextDirection": true]))
        LanguageChoice.english.keep(in: d)
        d.removeObject(forKey: "AppleTextDirection")
        both.restore(in: d)
        #expect(own(d, suite)["AppleLanguages"] as? [String] == ["ar"])
        #expect((own(d, suite)["AppleTextDirection"] as? NSNumber)?.boolValue == true)
    }

    @Test func onlyANewChoiceStartsTheAppAgain() {
        #expect(LanguageChoice.arabic.needsRelaunch(from: .english))
        #expect(LanguageChoice.system.needsRelaunch(from: .arabic))
        #expect(LanguageChoice.english.needsRelaunch(from: .system))
        for c in LanguageChoice.allCases { #expect(!c.needsRelaunch(from: c)) }
    }

    @Test func eachLanguageIsNamedInItself() {
        #expect(LanguageChoice.english.name == "English")
        #expect(LanguageChoice.arabic.name == "العربية")
        #expect(LanguageChoice.system.name == kit("System"))
        #expect(LanguageChoice.allCases.map(\.language) == ["en", "ar", nil])
    }
}
