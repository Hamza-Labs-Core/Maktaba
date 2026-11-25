#if os(macOS)
import MaktabaKit
import SwiftUI

/// The Mac Player's sidebar (Epic 13 story 13.16, `docs/design/desktop/mockups/mac-player.html`):
/// the sections as the apps' side list instead of the system's grey selection and white
/// symbols. Each row is a brass symbol and the section's name; the chosen one sits on the
/// brass `chosen` fill, in 600 weight. Profile stays at the bottom, as on the rail of the other
/// desktop Players. The column is the window's split view sidebar, so the system keeps its
/// vibrancy and the traffic lights over it; in Arabic AppKit puts it at the right
/// (`LanguageChoice`, D5) and the rows mirror.
///
/// The rows above Profile are the person's to arrange, as the iPad's tabs are: drag one to move
/// it, a row's menu hides it, and Show All Sections brings them back (`SidebarArrangement`,
/// kept by the shell). The column takes the keyboard's focus: ↑ and ↓ choose the section above
/// or below. VoiceOver reads it as Sections, the chosen row as selected.
struct MacSidebar: View {
    let rows: MacSidebarRows
    @Binding var selection: PlayerSection
    @Binding var arrangement: SidebarArrangement

    var body: some View {
        List {
            ForEach(rows.top) { row($0, arranged: true) }
                .onMove { offsets, destination in
                    arrangement.move(rows.top.map(\.rawValue), fromOffsets: offsets, toOffset: destination)
                }
        }
        .listStyle(.sidebar)
        // the list's own name and identifier (on the outline, not the Profile row below it)
        .accessibilityLabel(Text("Sections", tableName: "Shell", comment: "The Mac sidebar's name for VoiceOver: the Player's sections"))
        .accessibilityIdentifier("player.sidebar")
        .safeAreaInset(edge: .bottom, spacing: 0) {
            if let bottom = rows.bottom {
                row(bottom, arranged: false)
                    .padding(.horizontal, MaktabaTokens.Spacing.s4) // in line with the list's rows
                    .padding(.vertical, MaktabaTokens.Spacing.s3)
            }
        }
        .focusable()
        .onMoveCommand { direction in
            if let next = rows.next(after: selection, direction) { selection = next }
        }
    }

    private func row(_ section: PlayerSection, arranged: Bool) -> some View {
        Button {
            selection = section
        } label: {
            MacSidebarRow(section: section, chosen: section == selection)
        }
        .buttonStyle(.plain)
        .accessibilityAddTraits(section == selection ? .isSelected : [])
        .accessibilityIdentifier("player.sidebar.\(section.rawValue)")
        .contextMenu {
            if arranged, rows.top.count > 1 {
                Button {
                    arrangement.hide(section.rawValue)
                } label: {
                    Text("Hide from the Sidebar", tableName: "Shell", comment: "A Mac sidebar row's menu: hides that section")
                }
            }
            if !arrangement.hidden.isEmpty {
                Button {
                    arrangement.showAll()
                } label: {
                    Text("Show All Sections", tableName: "Shell", comment: "A Mac sidebar row's menu: shows the hidden sections again")
                }
            }
        }
    }
}

/// Which sections the sidebar lists at the top, in the person's order (`SidebarArrangement`,
/// the hidden ones left out but the one in view), and which one sits at its bottom: Profile,
/// when it shows. `all` is the sidebar from top to bottom, which the View menu's ⌘1, ⌘2 …
/// follow.
struct MacSidebarRows: Equatable {
    let top: [PlayerSection]
    let bottom: PlayerSection?

    /// The sections the person can move and hide: all but Profile, which stays at the bottom.
    static let arrangeable = PlayerSection.allCases.filter { $0 != .profile }

    init(_ sections: [PlayerSection], arrangement: SidebarArrangement = SidebarArrangement(), keep: PlayerSection? = nil) {
        let rest = sections.filter(Self.arrangeable.contains)
        if rest.isEmpty {
            top = sections
            bottom = nil
        } else {
            top = arrangement.arrange(rest.map(\.rawValue), keep: keep?.rawValue).compactMap(PlayerSection.init(rawValue:))
            bottom = sections.contains(.profile) ? .profile : nil
        }
    }

    /// The sidebar from top to bottom.
    var all: [PlayerSection] { top + (bottom.map { [$0] } ?? []) }

    /// The section ↑ or ↓ chooses from `section`: the one above or below it, none past either
    /// end (← and →: none).
    func next(after section: PlayerSection, _ direction: MoveCommandDirection) -> PlayerSection? {
        let list = all
        guard let at = list.firstIndex(of: section) else { return list.first }
        switch direction {
        case .up: return at > 0 ? list[at - 1] : nil
        case .down: return at + 1 < list.count ? list[at + 1] : nil
        default: return nil
        }
    }
}

/// One row of the sidebar: 32 points high, a brass symbol in an 18-point column, the name; the
/// chosen row on the `chosen` fill with rounded corners.
struct MacSidebarRow: View {
    let section: PlayerSection
    let chosen: Bool

    var body: some View {
        HStack(spacing: 10) {
            Image(systemName: section.systemImage)
                .font(.system(size: 14, weight: .medium))
                .foregroundStyle(Color.mktBrassText)
                .frame(width: 18, height: 18)
                .accessibilityHidden(true)
            Text(section.title)
                .font(.maktabaBody(13, weight: chosen ? .semibold : .regular, relativeTo: .body))
                .foregroundStyle(Color.mktText)
                .lineLimit(2)
            Spacer(minLength: 0)
        }
        .padding(.horizontal, 10)
        .frame(minHeight: 32)
        .background(chosen ? Color.mktChosen : Color.clear, in: .rect(cornerRadius: MaktabaTokens.Radius.md))
        .contentShape(.rect)
    }
}

/// The apps' row on the Mac (story 13.16): a full-width row of the list, its label at the start
/// with nothing of a push button's bezel; under the pointer a faint fill, pressed the `chosen`
/// one. The text keeps the colour around it (a red Sign out stays red).
struct MacRowButtonStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        MacRow(configuration: configuration)
    }

    private struct MacRow: View {
        let configuration: ButtonStyleConfiguration
        @State private var hovering = false
        @Environment(\.isEnabled) private var enabled

        var body: some View {
            configuration.label
                .frame(maxWidth: .infinity, minHeight: 32, alignment: .leading)
                .padding(.horizontal, MaktabaTokens.Spacing.s2)
                .contentShape(.rect)
                .background(fill, in: .rect(cornerRadius: MaktabaTokens.Radius.md))
                // the fill reaches past the label; the label stays in line with the list's
                // other rows (the theme, the language) and its group heads
                .padding(.horizontal, -MaktabaTokens.Spacing.s2)
                .opacity(enabled ? 1 : 0.5)
                .onHover { hovering = $0 }
        }

        private var fill: Color {
            if configuration.isPressed { return .mktChosen }
            return hovering ? Color.mktText.opacity(0.06) : .clear
        }
    }
}

/// The apps' row label on the Mac: the symbol in brass (the tint: a red row's is red) in an
/// 18-point column, then the title.
struct MacRowLabelStyle: LabelStyle {
    func makeBody(configuration: Configuration) -> some View {
        HStack(spacing: 10) {
            configuration.icon
                .font(.system(size: 14, weight: .medium))
                .foregroundStyle(.tint)
                .frame(width: 18)
            configuration.title
        }
    }
}

extension View {
    /// A list in the apps' row style on the Mac (story 13.16): Profile and its settings. Its
    /// buttons are full-width rows (`MacRowButtonStyle`), its labels a brass symbol and the
    /// title (`MacRowLabelStyle`), its text the theme's, not the tint.
    func macRows() -> some View {
        buttonStyle(MacRowButtonStyle())
            .labelStyle(MacRowLabelStyle())
            .foregroundStyle(Color.mktText)
    }
}
#endif
