#if os(macOS)
import MaktabaKit
import Testing
@testable import Maktaba_Player_Mac

/// A search and a result's page both wait for the search field to let go of the keyboard
/// (`SearchScreen`, `LetGoFirst<SearchChange>`): the open gap of Trail #1425, a result tapped
/// after going back to the field to refine.
@Suite struct SearchLetsGoFirst {
    @Test func aResultTappedWithTheFieldLetGoOpensAtOnce() {
        var letGo = LetGoFirst<SearchChange>()
        #expect(letGo.begin(.open(.video(id: "v3")), focused: false) == .open(.video(id: "v3")))
        #expect(letGo.held == nil)
    }

    @Test func aResultTappedWhileRefiningOpensOnceTheFieldLetsGo() {
        var letGo = LetGoFirst<SearchChange>()
        // the field holds the keyboard: nothing opens yet
        #expect(letGo.begin(.open(.video(id: "v3")), focused: true) == nil)
        #expect(letGo.focusChanged(true) == nil, "still focused")
        // the field let go: the page opens, and only once
        #expect(letGo.focusChanged(false) == .open(.video(id: "v3")))
        #expect(letGo.focusChanged(false) == nil)
    }

    @Test func aResultTappedAfterASearchHeldOpensInstead() {
        var letGo = LetGoFirst<SearchChange>()
        #expect(letGo.begin(.search("seerah"), focused: true) == nil)
        // the passage tapped before the field let go is what the person asked for last
        #expect(letGo.begin(.open(.watch(videoID: "v1", at: 1622)), focused: true) == nil)
        #expect(letGo.focusChanged(false) == .open(.watch(videoID: "v1", at: 1622)))
    }
}
#endif
