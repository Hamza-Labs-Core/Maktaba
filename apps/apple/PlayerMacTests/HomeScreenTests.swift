#if os(macOS)
import Foundation
import MaktabaKit
import Testing
@testable import Maktaba_Player_Mac

/// A Home card's context menu and its VoiceOver custom actions are one list
/// (`HomeCardAction.of`), named with the menu's words, and both call `HomeCardAction.run`.
@MainActor @Suite struct HomeCardActions {
    private func card(resumeAt: Double? = nil, recommended: Bool = false) -> HomeCard {
        HomeCard(HomeShelfCard(videoID: "v1", title: "A film", resumeAt: resumeAt), recommended: recommended)
    }

    @Test func eachCardOffersItsMenusActions() {
        #expect(HomeCardAction.of(card()) == [])
        #expect(HomeCardAction.of(card(resumeAt: 90)) == [.resume(at: 90)])
        #expect(HomeCardAction.of(card(recommended: true)) == [.notInterested])
        #expect(HomeCardAction.of(card(resumeAt: 12, recommended: true)) == [.resume(at: 12), .notInterested])
    }

    @Test func theActionsAreNamedAsTheMenuItemsInBothLanguages() {
        let en = Locale(identifier: "en"), ar = Locale(identifier: "ar")
        #expect(HomeCardAction.resume(at: 1).title.string(in: en) == "Resume")
        #expect(HomeCardAction.resume(at: 1).title.string(in: ar) == "استئناف")
        #expect(HomeCardAction.notInterested.title.string(in: en) == "Not interested")
        #expect(HomeCardAction.notInterested.title.string(in: ar) == "غير مهتم")
        // the menu items' identifiers the UI tests press
        #expect(HomeCardAction.resume(at: 1).identifier == "home.menu.resume")
        #expect(HomeCardAction.notInterested.identifier == "home.menu.notInterested")
        #expect(HomeCardAction.notInterested.isDestructive && !HomeCardAction.resume(at: 1).isDestructive)
    }

    /// What each handler was asked to do.
    private final class Calls {
        var opened: [PlayerRoute] = []
        var dismissed: [String] = []
        var failed = 0
    }

    private func handlers(_ calls: Calls, dismissWorks: Bool) -> HomeCardAction.Handlers {
        HomeCardAction.Handlers(open: { calls.opened.append($0) },
                                dismiss: { calls.dismissed.append($0); return dismissWorks },
                                dismissFailed: { calls.failed += 1 })
    }

    @Test func resumeOpensThePlayerAtThePosition() async {
        let calls = Calls()
        let task = HomeCardAction.resume(at: 90).run(on: card(resumeAt: 90), handlers(calls, dismissWorks: true))
        #expect(task == nil)
        #expect(calls.opened == [.watch(videoID: "v1", at: 90)])
        #expect(calls.dismissed.isEmpty && calls.failed == 0)
    }

    @Test func notInterestedHidesTheRecommendation() async {
        let calls = Calls()
        await HomeCardAction.notInterested.run(on: card(recommended: true), handlers(calls, dismissWorks: true))?.value
        #expect(calls.dismissed == ["v1"])
        #expect(calls.opened.isEmpty && calls.failed == 0)
    }

    @Test func notInterestedSaysSoWhenTheHomeCannotBeReached() async {
        let calls = Calls()
        await HomeCardAction.notInterested.run(on: card(recommended: true), handlers(calls, dismissWorks: false))?.value
        #expect(calls.dismissed == ["v1"])
        #expect(calls.failed == 1)
    }
}
#endif
