import MaktabaKit
import SwiftUI

/// What the View menu acts on in the focused window (Epic 48, FR-42): the sections that
/// window shows, the one chosen, and a way to choose one or to go to search. The shell
/// publishes it (`playerMenuTarget(sections:selection:)`), so the menu never names a screen:
/// whichever sections are enabled get ⌘1, ⌘2 … in their order.
struct PlayerMenuTarget {
    let sections: [PlayerSection]
    let chosen: PlayerSection
    let choose: (PlayerSection) -> Void
    /// ⌘F: the Search section if the window has it (hidden from the Mac's sidebar too), and its
    /// search field focused (`playerFindFocus()`).
    let find: () -> Void

    /// `offered`: every section of the window, those hidden from its sidebar too.
    @MainActor init(sections: [PlayerSection], offered: [PlayerSection], selection: Binding<PlayerSection>, finds: FindRequests) {
        self.sections = sections
        chosen = selection.wrappedValue
        choose = { section in
            guard sections.contains(section) else { return }
            selection.wrappedValue = section
        }
        find = {
            let opening = offered.contains(.search) && selection.wrappedValue != .search
            if opening { selection.wrappedValue = .search }
            finds.ask(opening: opening)
        }
    }
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

extension EnvironmentValues {
    /// The window's ⌘F presses; a screen with a search field takes each one (`playerFindFocus()`).
    @Entry var playerFind: FindRequests?
}

extension View {
    /// The shell's side of the View menu: publishes `sections` and `selection` to the menu bar
    /// for the window this view is in, and passes ⌘F down to the screens. `offered`: every
    /// section of the window, those hidden from its sidebar too (⌘F opens Search among them).
    func playerMenuTarget(sections: [PlayerSection], offered: [PlayerSection], selection: Binding<PlayerSection>) -> some View {
        modifier(PlayerMenuTargetModifier(sections: sections, offered: offered, selection: selection))
    }

    /// A screen's search field takes the focus at ⌘F (View ▸ Search). Put it after `searchable`.
    func playerFindFocus() -> some View {
        modifier(PlayerFindFocus())
    }
}

private struct PlayerMenuTargetModifier: ViewModifier {
    let sections: [PlayerSection]
    let offered: [PlayerSection]
    @Binding var selection: PlayerSection
    @State private var find = FindRequests()

    func body(content: Content) -> some View {
        content
            .environment(\.playerFind, find)
            .focusedSceneValue(\.playerMenu, PlayerMenuTarget(sections: sections, offered: offered, selection: $selection, finds: find))
            .onChange(of: selection) { _, now in if now != .search { find.drop() } }
    }
}

private struct PlayerFindFocus: ViewModifier {
    @Environment(\.playerFind) private var find
    @FocusState private var focused: Bool

    func body(content: Content) -> some View {
        content
            .searchFocused($focused)
            .onAppear { find?.fieldAppeared() }
            .onDisappear { find?.fieldDisappeared() }
            // at each ⌘F, and when ⌘F is what brought this screen up (asked, not yet taken)
            .task(id: find?.asked ?? 0) { if find?.take() == true { focused = true } }
    }
}
