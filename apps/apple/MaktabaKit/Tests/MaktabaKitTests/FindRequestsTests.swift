import Testing
@testable import MaktabaKit

/// Story 13.16: ⌘F is taken once, by the search field in view or the one it opens, and a press
/// that found no taker is not kept for a field that comes later.
@MainActor @Suite struct FindRequestsTests {
    @Test func theFieldInViewTakesEachPressOnce() {
        let find = FindRequests()
        find.fieldAppeared()
        #expect(!find.take(), "no ⌘F yet")
        find.ask(opening: false)
        #expect(find.take())
        #expect(!find.take())
        find.ask(opening: false)
        find.ask(opening: false)
        #expect(find.take(), "two presses before the field took one")
        #expect(!find.take())
        #expect(find.asked == 3)
    }

    @Test func thePressThatOpensSearchWaitsForItsField() {
        let find = FindRequests()
        find.ask(opening: true)
        find.fieldAppeared()
        #expect(find.take())
        find.fieldDisappeared()
        // Search shown again: nothing left
        find.fieldAppeared()
        #expect(!find.take())
    }

    @Test func aPressWithNoFieldAndNoSearchIsNotKept() {
        let find = FindRequests()
        find.ask(opening: false)
        find.fieldAppeared()
        #expect(!find.take(), "a field that opened later is not the one asked for")
        find.fieldDisappeared()
        find.ask(opening: false)
        find.fieldAppeared()
        #expect(!find.take(), "nor after a field has gone")
    }

    @Test func leavingSearchBeforeItsFieldTookThePressDropsIt() {
        let find = FindRequests()
        find.ask(opening: true)
        find.drop()
        find.fieldAppeared()
        #expect(!find.take())
    }
}
