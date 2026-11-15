import Testing
@testable import MaktabaKit

/// A screen change waits for the text field to let go of the keyboard (the search field after
/// Return; Trail #1419 for the sign-in form).
@Suite struct LetGoFirstTests {
    /// The field holds nothing (a recent search tapped, a plugin's chip): at once.
    @Test func runsAtOnceWhenTheFieldHoldsNothing() {
        var letGo = LetGoFirst<String>()
        #expect(letGo.begin("seerah", focused: false) == "seerah")
        #expect(letGo.held == nil)
        #expect(letGo.focusChanged(false) == nil, "nothing left to run")
    }

    /// Return in the field: held until it lets go, then run once.
    @Test func waitsForTheFieldToLetGo() {
        var letGo = LetGoFirst<String>()
        #expect(letGo.begin("seerah", focused: true) == nil, "held while the keyboard is up")
        #expect(letGo.held == "seerah")
        #expect(letGo.focusChanged(true) == nil, "still focused: still held")
        #expect(letGo.focusChanged(false) == "seerah")
        #expect(letGo.focusChanged(false) == nil, "only once")
        #expect(letGo.held == nil)
    }

    /// A suggestion tapped, then Return before the field let go: only the latest runs.
    @Test func theLatestChangeWins() {
        var letGo = LetGoFirst<String>()
        _ = letGo.begin("se", focused: true)
        _ = letGo.begin("seerah", focused: true)
        #expect(letGo.focusChanged(false) == "seerah")
    }

    /// A change that ran at once drops an older one still held (it would run late, over it).
    @Test func aChangeRunNowDropsAHeldOne() {
        var letGo = LetGoFirst<String>()
        _ = letGo.begin("se", focused: true)
        #expect(letGo.begin("cordoba", focused: false) == "cordoba")
        #expect(letGo.focusChanged(false) == nil)
    }
}
