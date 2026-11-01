import SwiftUI

/// The View menu's sections (Epic 48, FR-42): the sections the window shows, in their order,
/// on ⌘1 to ⌘5. Written against `PlayerSection`, not the screens, so a section that merges
/// later takes its number with no change here.
enum PlayerViewMenu {
    struct Item: Identifiable {
        let section: PlayerSection
        let key: KeyEquivalent
        var id: PlayerSection { section }
    }

    static func items(_ sections: [PlayerSection]) -> [Item] {
        zip(sections.prefix(5), ["1", "2", "3", "4", "5"] as [KeyEquivalent]).map { Item(section: $0, key: $1) }
    }
}

/// The View menu (the Mac's menu bar, an iPad's with a keyboard): each section on ⌘1–⌘5 and
/// Search on ⌘F (off while the window shows no sections: signing in, choosing a home), and on
/// the Mac Enter Full Screen on ⌃⌘F (`MacFullScreen`), above the system's sidebar and toolbar
/// items.
struct PlayerViewCommands: Commands {
    @FocusedValue(\.playerMenu) private var menu

    var body: some Commands {
        CommandGroup(before: .toolbar) {
            ForEach(PlayerViewMenu.items(menu?.sections ?? PlayerSection.visible())) { item in
                Button {
                    menu?.choose(item.section)
                } label: {
                    Text(item.section.title)
                }
                .keyboardShortcut(item.key, modifiers: .command)
                .disabled(menu == nil)
            }
            Button {
                menu?.find()
            } label: {
                Text("Search…", tableName: "Mac", comment: "View menu: go to search and type (Command-F)")
            }
            .keyboardShortcut("f", modifiers: .command)
            .disabled(menu == nil)
            #if os(macOS)
            Divider()
            Button {
                MacFullScreen.shared.toggle()
            } label: {
                if MacFullScreen.shared.isFullScreen {
                    Text("Exit Full Screen", tableName: "Mac", comment: "View menu (Control-Command-F) while full screen")
                } else {
                    Text("Enter Full Screen", tableName: "Mac", comment: "View menu (Control-Command-F): the window on a space of its own")
                }
            }
            .keyboardShortcut("f", modifiers: [.control, .command])
            #endif
            Divider()
        }
    }
}

/// The Playback menu (FR-42): space plays and pauses, ←/→ skip 10 s and ⇧←/⇧→ 30 s, N plays
/// the next episode, S the subtitles, ⌘. stops. Each is off while nothing plays (N and S
/// while the player publishes no next episode or subtitles, which it does only while it
/// plays), so the keys stay the search field's and the lists'.
struct PlayerPlaybackCommands: Commands {
    @FocusedValue(\.playback) private var playback
    @FocusedValue(\.playNext) private var next
    @FocusedValue(\.playerSubtitles) private var subtitles

    var body: some Commands {
        CommandMenu(Text("Playback", tableName: "Mac", comment: "The Playback menu")) {
            let playing = playback?.player != nil
            Button { playback?.togglePause() } label: {
                Text("Play/Pause", tableName: "Mac", comment: "Playback menu (space)")
            }
            .keyboardShortcut(.space, modifiers: [])
            .disabled(!playing)
            Divider()
            Button { playback?.skip(by: 10) } label: {
                Text("Skip Forward 10 Seconds", tableName: "Mac", comment: "Playback menu (right arrow)")
            }
            .keyboardShortcut(.rightArrow, modifiers: [])
            .disabled(!playing)
            Button { playback?.skip(by: -10) } label: {
                Text("Skip Back 10 Seconds", tableName: "Mac", comment: "Playback menu (left arrow)")
            }
            .keyboardShortcut(.leftArrow, modifiers: [])
            .disabled(!playing)
            Button { playback?.skip(by: 30) } label: {
                Text("Skip Forward 30 Seconds", tableName: "Mac", comment: "Playback menu (Shift-right arrow)")
            }
            .keyboardShortcut(.rightArrow, modifiers: .shift)
            .disabled(!playing)
            Button { playback?.skip(by: -30) } label: {
                Text("Skip Back 30 Seconds", tableName: "Mac", comment: "Playback menu (Shift-left arrow)")
            }
            .keyboardShortcut(.leftArrow, modifiers: .shift)
            .disabled(!playing)
            Divider()
            Button { next?.perform() } label: {
                Text("Next Episode", tableName: "Mac", comment: "Playback menu (N)")
            }
            .keyboardShortcut("n", modifiers: [])
            .disabled(next == nil)
            Button { subtitles?.perform() } label: {
                Text("Subtitles", tableName: "Mac", comment: "Playback menu (S): the subtitle choices")
            }
            .keyboardShortcut("s", modifiers: [])
            .disabled(subtitles == nil)
            Divider()
            Button { playback?.stop() } label: {
                Text("Stop", tableName: "Mac", comment: "Playback menu (Command-period)")
            }
            .keyboardShortcut(".", modifiers: .command)
            .disabled(playback?.video == nil)
        }
    }
}
