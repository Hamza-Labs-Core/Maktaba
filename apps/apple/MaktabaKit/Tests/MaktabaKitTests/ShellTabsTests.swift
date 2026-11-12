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

    /// Live TV goes off while a channel is open on its tab: the tab stays through the
    /// change and through the way back out of the channel (the same selection), in its
    /// place, and goes with the first other tab chosen; the tabs' count never drops to one
    /// on the way (the shell would swap its tab bar for a single stack under the open screen).
    @Test func liveTVGoingOffUnderAnOpenChannelKeepsItsTabUntilAnotherIsChosen() {
        let on = order
        let off = ["home", "libraries", "search", "profile"]
        #expect(ShellTabs.shown(offered: on, selected: "live", order: order) == order)
        let kept = ShellTabs.shown(offered: off, selected: "live", order: order)
        #expect(kept == order)
        #expect(ShellTabs.shown(offered: off, selected: "live", order: order) == kept, "asked again: the same")
        #expect(ShellTabs.shown(offered: off, selected: "home", order: order) == off)
        // Live TV and one other section: two tabs before, two while it is kept, one after
        #expect(ShellTabs.shown(offered: ["libraries"], selected: "live", order: order) == ["libraries", "live"])
        #expect(ShellTabs.shown(offered: ["libraries"], selected: "libraries", order: order) == ["libraries"])
    }

    @Test func aTabOutsideTheOrderGoesLast() {
        #expect(ShellTabs.shown(offered: ["home"], selected: "extra", order: order) == ["home", "extra"])
    }
}
