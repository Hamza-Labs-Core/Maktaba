import Foundation
import Testing
@testable import MaktabaKit

/// Story 51.11: when a feature goes off, the shell's tabs change without closing the screen
/// in view; it goes on the next navigation.
@Suite struct ShellTabsTests {
    let order = ["home", "libraries", "live", "search", "profile"]

    @Test func theOfferedTabsWhenTheOneInViewIsAmongThem() {
        #expect(ShellTabs.shown(offered: ["home", "libraries", "search"], selected: "home", order: order)
            == ["home", "libraries", "search"])
    }

    @Test func theTabInViewStaysInItsPlaceWhenItsFeatureGoesOff() {
        #expect(ShellTabs.shown(offered: ["home", "libraries", "search", "profile"], selected: "live", order: order)
            == ["home", "libraries", "live", "search", "profile"])
        #expect(ShellTabs.shown(offered: ["libraries", "search"], selected: "home", order: order)
            == ["home", "libraries", "search"])
        #expect(ShellTabs.shown(offered: ["home", "libraries"], selected: "profile", order: order)
            == ["home", "libraries", "profile"])
    }

    @Test func anotherTabChosenLetsItGo() {
        // the shell asks again with the new selection: Live TV is no longer kept
        let offered = ["home", "libraries", "search", "profile"]
        #expect(ShellTabs.shown(offered: offered, selected: "search", order: order) == offered)
    }

    @Test func aTabOutsideTheOrderGoesLast() {
        #expect(ShellTabs.shown(offered: ["home"], selected: "extra", order: order) == ["home", "extra"])
    }
}
