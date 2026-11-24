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
struct MacSidebar: View {
    let sections: [PlayerSection]
    @Binding var selection: PlayerSection

    var body: some View {
        let rows = MacSidebarRows(sections)
        List {
            ForEach(rows.top) { row($0) }
        }
        .listStyle(.sidebar)
        .safeAreaInset(edge: .bottom, spacing: 0) {
            if let bottom = rows.bottom {
                row(bottom)
                    .padding(.horizontal, MaktabaTokens.Spacing.s2 + 2)
                    .padding(.vertical, MaktabaTokens.Spacing.s3)
            }
        }
        .accessibilityIdentifier("player.sidebar")
    }

    private func row(_ section: PlayerSection) -> some View {
        Button {
            selection = section
        } label: {
            MacSidebarRow(section: section, chosen: section == selection)
        }
        .buttonStyle(.plain)
        .accessibilityAddTraits(section == selection ? .isSelected : [])
        .accessibilityIdentifier("player.sidebar.\(section.rawValue)")
    }
}

/// Which sections the sidebar lists at the top and which one sits at its bottom: Profile, when
/// it shows, at the bottom; the rest at the top in their order.
struct MacSidebarRows: Equatable {
    let top: [PlayerSection]
    let bottom: PlayerSection?

    init(_ sections: [PlayerSection]) {
        top = sections.filter { $0 != .profile }
        bottom = sections.contains(.profile) && !top.isEmpty ? .profile : nil
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
