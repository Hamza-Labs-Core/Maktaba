import SwiftUI

/// What the View menu acts on in the focused window (Epic 48, FR-42): the sections that
/// window shows, the one chosen, and a way to choose one or to go to search. The shell
/// publishes it (`playerMenuTarget(sections:selection:)`), so the menu never names a screen:
/// whichever sections are enabled get ⌘1, ⌘2 … in their order.
struct PlayerMenuTarget {
    let sections: [PlayerSection]
    let chosen: PlayerSection
    let choose: (PlayerSection) -> Void
    /// ⌘F: the Search section if it shows, and its search field focused (`playerFindFocus()`).
    let find: () -> Void
}

/// What the Playback menu does beyond `Playback`'s own play / pause, skip and stop: the
/// next episode (N) and the subtitles (S). The player screen publishes them while a video
/// plays (`focusedSceneValue(\.playNext, …)`); while nothing does, the items are off and the
/// letters stay the search field's.
struct PlayerMenuAction {
    let perform: () -> Void
}

extension FocusedValues {
    /// The focused window's sections (the View menu's ⌘1–⌘5 and ⌘F).
    @Entry var playerMenu: PlayerMenuTarget?
    /// Plays the next episode (Playback ▸ Next, N).
    @Entry var playNext: PlayerMenuAction?
    /// Shows the subtitle choices, or turns the chosen ones on and off (Playback ▸ Subtitles, S).
    @Entry var playerSubtitles: PlayerMenuAction?
}

/// The ⌘F presses of a window (View ▸ Search): each one is taken once, by the search field
/// in view (`playerFindFocus()`). A Search screen built again later (the Mac builds a section's
/// screen when it is chosen, story 13.16) finds nothing left to take, so it does not grab the
/// focus again on every visit.
@MainActor @Observable
final class FindRequests {
    /// Every ⌘F so far.
    private(set) var asked = 0
    @ObservationIgnored private var taken = 0

    /// ⌘F.
    func ask() { asked += 1 }

    /// True once for each ⌘F not yet taken.
    func take() -> Bool {
        guard taken < asked else { return false }
        taken = asked
        return true
    }
}

extension EnvironmentValues {
    /// The window's ⌘F presses; a screen with a search field takes each one (`playerFindFocus()`).
    @Entry var playerFind: FindRequests?
}

extension View {
    /// The shell's side of the View menu: publishes `sections` and `selection` to the menu bar
    /// for the window this view is in, and passes ⌘F down to the screens.
    func playerMenuTarget(sections: [PlayerSection], selection: Binding<PlayerSection>) -> some View {
        modifier(PlayerMenuTargetModifier(sections: sections, selection: selection))
    }

    /// A screen's search field takes the focus at ⌘F (View ▸ Search). Put it after `searchable`.
    func playerFindFocus() -> some View {
        modifier(PlayerFindFocus())
    }
}

private struct PlayerMenuTargetModifier: ViewModifier {
    let sections: [PlayerSection]
    @Binding var selection: PlayerSection
    @State private var find = FindRequests()

    func body(content: Content) -> some View {
        content
            .environment(\.playerFind, find)
            .focusedSceneValue(\.playerMenu, target)
    }

    private var target: PlayerMenuTarget {
        let selection = $selection
        let find = find
        let sections = sections
        return PlayerMenuTarget(sections: sections, chosen: selection.wrappedValue) { section in
            guard sections.contains(section) else { return }
            selection.wrappedValue = section
        } find: {
            if sections.contains(.search), selection.wrappedValue != .search { selection.wrappedValue = .search }
            find.ask()
        }
    }
}

private struct PlayerFindFocus: ViewModifier {
    @Environment(\.playerFind) private var find
    @FocusState private var focused: Bool

    func body(content: Content) -> some View {
        content
            .searchFocused($focused)
            // at each ⌘F, and when ⌘F is what brought this screen up (asked, not yet taken)
            .task(id: find?.asked ?? 0) { if find?.take() == true { focused = true } }
    }
}
