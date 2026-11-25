import Foundation
import SwiftUI
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

    /// Epic 57: what was read under an older limit is never handed out under a newer one, the
    /// first time it is asked for included.
    @MainActor @Test func aValueIsMadeAgainForEachParentalEpoch() {
        var made = 0
        let lists = UnderEpoch { made += 1; return Box("lists \(made)") }
        let first = lists.value(0)
        #expect(lists.value(0) === first)
        // the limit changed while another section was in view: the first ask finds new lists
        let second = lists.value(1)
        #expect(second !== first && second.value == "lists 2")
        #expect(lists.value(1) === second)
        #expect(lists.value(2) !== second)
        #expect(made == 3)
    }
}

/// Story 13.16: the sidebar arranged in a Player from before it (a sidebar the system drew,
/// kept as SwiftUI's `TabViewCustomization` under `player.tabs`) is carried over once.
///
/// The texts are what `@AppStorage` stored on macOS 27.0.1: a program set
/// `customization[tab:].sidebarVisibility` (the hidden sections) and, for the order, dropped a
/// row on the outline of a `TabView` in the sidebar style whose tabs are in a `TabSection`,
/// then read the defaults' data. The released Player's tabs were in no section: AppKit's
/// outline gave them no drag, so a Mac stored hidden sections only; an order is read all the
/// same.
@Suite struct SidebarCarryOverTests {
    static let known = ["home", "libraries", "live", "search"]
    static let hiddenOnly = #"{"identifier":"EA330BA3-F44B-4AFB-8E6F-529F3526FD2E","perTabState":[{"base":{"explicit":{"_0":"player.section.search"}}},{"tabBarVisibility":{"automatic":{}},"visibility":{"hidden":{}}},{"base":{"explicit":{"_0":"player.section.home"}}},{"tabBarVisibility":{"automatic":{}},"visibility":{"visible":{}}},{"base":{"explicit":{"_0":"player.section.live"}}},{"tabBarVisibility":{"automatic":{}},"visibility":{"hidden":{}}}],"perSectionState":[]}"#
    static let orderAndHidden = #"{"identifier":"73AAF60F-88E8-4B86-B59A-06505DE4DD35","perTabState":[{"base":{"explicit":{"_0":"player.section.live"}}},{"tabBarVisibility":{"automatic":{}},"visibility":{"hidden":{}}},{"base":{"explicit":{"_0":"player.section.home"}}},{"tabBarVisibility":{"automatic":{}},"visibility":{"visible":{}}}],"perSectionState":[{"base":{"explicit":{"_0":"sec"}}},{"childrenIDs":[{"base":{"explicit":{"_0":"player.section.search"}}},{"base":{"explicit":{"_0":"player.section.home"}}},{"base":{"explicit":{"_0":"player.section.libraries"}}},{"base":{"explicit":{"_0":"player.section.live"}}},{"base":{"explicit":{"_0":"player.section.profile"}}}]}]}"#
    static let reset = #"{"identifier":"EA330BA3-F44B-4AFB-8E6F-529F3526FD2E","perTabState":[],"perSectionState":[]}"#

    private func with(_ tabs: Any?, kept: String? = nil, _ body: (UserDefaults) throws -> Void) throws {
        let name = "maktaba.tests.carry.\(UUID().uuidString)"
        let defaults = try #require(UserDefaults(suiteName: name))
        defer { defaults.removePersistentDomain(forName: name) }
        if let tabs { defaults.set(tabs, forKey: "player.tabs") }
        if let kept { defaults.set(kept, forKey: "player.mac.sidebar") }
        try body(defaults)
    }

    private func carried(_ tabs: Any?) throws -> SidebarArrangement? {
        var out: SidebarArrangement?
        try with(tabs) { d in
            let did = SidebarArrangement.carryOver(in: d, known: Self.known)
            #expect(did == (d.object(forKey: "player.mac.sidebar") != nil))
            out = d.string(forKey: "player.mac.sidebar").map(SidebarArrangement.init(stored:))
        }
        return out
    }

    @Test func theHiddenSectionsStayHidden() throws {
        let a = try #require(try carried(Data(Self.hiddenOnly.utf8)))
        #expect(a.hidden == ["live", "search"])
        #expect(a.arrange(Self.known) == ["home", "libraries"])
    }

    @Test func theOrderIsKept() throws {
        let a = try #require(try carried(Data(Self.orderAndHidden.utf8)))
        #expect(a.order == ["search", "home", "libraries", "live"], "Profile, at the bottom, is not arranged")
        #expect(a.hidden == ["live"])
        #expect(a.arrange(Self.known) == ["search", "home", "libraries"])
    }

    /// The old value stays (an older Player opened again finds it), and is read once: the
    /// person's own arrangement since is not replaced.
    @Test func itIsCarriedOnceAndTheOldValueStays() throws {
        let old = Data(Self.orderAndHidden.utf8)
        try with(old) { d in
            #expect(SidebarArrangement.carryOver(in: d, known: Self.known))
            var a = SidebarArrangement(stored: d.string(forKey: "player.mac.sidebar") ?? "")
            a.showAll()
            d.set(a.stored, forKey: "player.mac.sidebar")
            #expect(!SidebarArrangement.carryOver(in: d, known: Self.known))
            #expect(d.string(forKey: "player.mac.sidebar") == a.stored)
            #expect(d.data(forKey: "player.tabs") == old)
        }
        // an arrangement already kept here, an empty one too, is the person's
        for kept in ["|home", ""] {
            try with(old, kept: kept) { d in
                #expect(!SidebarArrangement.carryOver(in: d, known: Self.known))
                #expect(d.string(forKey: "player.mac.sidebar") == kept)
            }
        }
    }

    @Test func nothingArrangedCarriesNothing() throws {
        #expect(try carried(nil) == nil)
        #expect(try carried(Data(Self.reset.utf8)) == nil)
    }

    @Test func unknownSectionsAreIgnored() throws {
        let odd = Self.orderAndHidden.replacingOccurrences(of: "player.section.home", with: "player.section.radio")
            .replacingOccurrences(of: "player.section.libraries", with: "other.libraries")
        let a = try #require(try carried(Data(odd.utf8)))
        #expect(a.order == ["search", "live"])
        #expect(a.hidden == ["live"])
    }

    @Test func everySectionHiddenHidesNone() throws {
        let entry = { (id: String) in #"{"base":{"explicit":{"_0":"player.section.\#(id)"}}},{"visibility":{"hidden":{}}}"# }
        let all = #"{"perTabState":[\#(Self.known.map(entry).joined(separator: ","))],"perSectionState":[]}"#
        #expect(try carried(Data(all.utf8)) == nil)
    }

    /// A shape this does not know (another macOS) carries nothing, and never fails.
    @Test(arguments: ["", "not json", "3", "[]", "{}", #"{"perTabState":3}"#, #"{"perTabState":[1,2,3]}"#,
                      #"{"perTabState":[{"base":{"explicit":{"_0":"player.section.home"}}}]}"#,
                      #"{"perTabState":{"player.section.home":"hidden"},"perSectionState":"x"}"#,
                      #"{"perTabState":[{"base":{"transient":{}}},{"visibility":[]}],"perSectionState":[{},{"childrenIDs":[3,{}]}]}"#])
    func anUnknownShapeCarriesNothing(_ text: String) throws {
        #expect(try carried(Data(text.utf8)) == nil)
        #expect(try carried(text) == nil)
    }

    @Test func aValueOfAnotherKindCarriesNothing() throws {
        #expect(try carried(7) == nil)
        #expect(try carried(["a": 1]) == nil)
    }

    #if os(macOS)
    /// The system's own encoding on the Mac running this, through `@AppStorage` as the Player
    /// stored it.
    @available(macOS 15.4, *)
    @MainActor @Test func whatThisSystemStoresIsRead() throws {
        try with(nil) { d in
            var tabs = TabViewCustomization()
            tabs[tab: "player.section.live"].sidebarVisibility = .hidden
            tabs[tab: "player.section.home"].sidebarVisibility = .visible
            AppStorage(wrappedValue: TabViewCustomization(), "player.tabs", store: d).wrappedValue = tabs
            #expect(d.data(forKey: "player.tabs") != nil, "SwiftUI keeps it as data")
            #expect(SidebarArrangement.carryOver(in: d, known: Self.known))
            #expect(SidebarArrangement(stored: d.string(forKey: "player.mac.sidebar") ?? "").hidden == ["live"])
        }
    }
    #endif
}
