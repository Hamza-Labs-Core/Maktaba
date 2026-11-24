import Foundation
import Testing
@testable import MaktabaKit

/// Story 13.16: the Mac sidebar keeps the person's order and hidden sections, and a section
/// keeps its state while another one is in view.
@Suite struct SidebarArrangementTests {
    let natural = ["home", "libraries", "live", "search"]

    @Test func nothingArrangedIsTheNaturalOrder() {
        #expect(SidebarArrangement().arrange(natural) == natural)
    }

    @Test func aMoveIsKeptAndStoredAndReadBack() {
        var a = SidebarArrangement()
        // Search dragged to the top
        a.move(natural, fromOffsets: IndexSet(integer: 3), toOffset: 0)
        #expect(a.arrange(natural) == ["search", "home", "libraries", "live"])
        // Home dragged below Live TV (onMove's destination counts the row itself)
        a.move(a.arrange(natural), fromOffsets: IndexSet(integer: 1), toOffset: 4)
        #expect(a.arrange(natural) == ["search", "libraries", "live", "home"])
        let back = SidebarArrangement(stored: a.stored)
        #expect(back == a)
        #expect(back.arrange(natural) == ["search", "libraries", "live", "home"])
    }

    @Test func aHiddenSectionIsLeftOutButTheOneInViewStays() {
        var a = SidebarArrangement()
        a.hide("live")
        #expect(a.arrange(natural) == ["home", "libraries", "search"])
        #expect(a.arrange(natural, keep: "live") == natural, "hidden while in view: it goes with the next choice")
        a.showAll()
        #expect(a.arrange(natural) == natural)
    }

    @Test func everySectionHiddenStillLeavesASidebar() {
        var a = SidebarArrangement()
        for s in natural { a.hide(s) }
        #expect(a.arrange(natural) == natural)
    }

    /// Live TV comes and goes with the home (51.11): a section never placed lands among its
    /// natural neighbours, and one the home took away is not listed.
    @Test func aSectionNeverPlacedFallsInItsNaturalPlace() {
        var a = SidebarArrangement()
        let withoutLive = ["home", "libraries", "search"]
        a.move(withoutLive, fromOffsets: IndexSet(integer: 2), toOffset: 0)
        #expect(a.arrange(withoutLive) == ["search", "home", "libraries"])
        #expect(a.arrange(natural) == ["search", "home", "libraries", "live"])
    }

    @Test func storedTextSurvivesOddInput() {
        #expect(SidebarArrangement(stored: "") == SidebarArrangement())
        #expect(SidebarArrangement(stored: "search,home").order == ["search", "home"])
        #expect(SidebarArrangement(stored: "|live").hidden == ["live"])
    }

    final class Box { var value: String; init(_ v: String) { value = v } }

    @MainActor @Test func aSectionKeepsItsStateWhileAnotherIsInView() {
        let keeper = SectionKeeper<String>(lasting: ["libraries"])
        let search = keeper.state("search", epoch: 0) { Box("") }
        search.value = "mecca"
        _ = keeper.state("home", epoch: 0) { Box("home") }
        #expect(keeper.state("search", epoch: 0) { Box("") } === search, "back to Search: the same model")
        #expect(keeper.state("search", epoch: 0) { Box("") }.value == "mecca")
    }

    @MainActor @Test func aNewParentalEpochDropsWhatWasKeptButTheLastingSections() {
        let keeper = SectionKeeper<String>(lasting: ["libraries"])
        let search = keeper.state("search", epoch: 0) { Box("a") }
        let libraries = keeper.state("libraries", epoch: 0) { Box("path") }
        #expect(keeper.state("search", epoch: 1) { Box("b") } !== search, "read again under the new limit")
        #expect(keeper.state("libraries", epoch: 1) { Box("") } === libraries, "Libraries reads again itself")
    }
}
