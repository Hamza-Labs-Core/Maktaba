import Foundation

/// The Player's five sections (Epic 48, FR-5): a tab bar on an iPhone (and a folded iPhone
/// Duo), tabs that become a sidebar on an iPad, an open Duo and the Mac.
enum PlayerSection: String, CaseIterable, Identifiable, Hashable, Sendable {
    case home
    case libraries
    case liveTV = "live"
    case search
    case profile

    /// The sections whose screens have merged, and only they show. Every release tag uploads
    /// to TestFlight, so a section stays out of this list until its screen story lands; that
    /// story adds its one line here. With one section the app shows that screen alone, as
    /// before the shell (no tab bar of one tab).
    static let enabled: [PlayerSection] = [
        .home,
        .libraries, // 48.18: Libraries, a library, Series, a series
        .liveTV, // 48.21: Now, Guide, the live player (shown only with a channel)
    ]

    var id: String { rawValue }

    var title: LocalizedStringResource {
        switch self {
        case .home: LocalizedStringResource("Home", table: "Shell", comment: "The Home section's tab")
        case .libraries: LocalizedStringResource("Libraries", table: "Shell", comment: "The Libraries section's tab")
        case .liveTV: LocalizedStringResource("Live TV", table: "Shell", comment: "The Live TV section's tab")
        case .search: LocalizedStringResource("Search", table: "Shell", comment: "The Search section's tab")
        case .profile: LocalizedStringResource("Profile", table: "Shell", comment: "The Profile section's tab")
        }
    }

    var systemImage: String {
        switch self {
        case .home: "house"
        case .libraries: "books.vertical"
        case .liveTV: "dot.radiowaves.left.and.right"
        case .search: "magnifyingglass"
        case .profile: "person.crop.circle"
        }
    }

    /// The sections this launch shows: `enabled`, in the order of `allCases`. A Debug build
    /// (the UI tests and a developer's run; never a TestFlight or App Store build) also takes
    /// `-maktaba-sections all` or `-maktaba-sections home,search`, so a screen can be tested
    /// before it is turned on.
    static func visible(arguments: [String] = CommandLine.arguments) -> [PlayerSection] {
        var shown = Set(enabled)
        #if DEBUG
        if let list = ShellLaunch.value(of: "-maktaba-sections", in: arguments) {
            shown = list == "all" ? Set(allCases)
                : Set(list.split(separator: ",").compactMap { PlayerSection(rawValue: String($0)) })
        }
        #endif
        let ordered = allCases.filter(shown.contains)
        return ordered.isEmpty ? [.libraries] : ordered
    }

    /// `sections` with Live TV only when the person may watch a channel (`hasChannels`;
    /// nil: the home has not said yet), never leaving no section (`LiveSectionGate`).
    static func shown(_ sections: [PlayerSection], hasChannels: Bool?) -> [PlayerSection] {
        guard hasChannels != true else { return sections }
        let without = sections.filter { $0 != .liveTV }
        return without.isEmpty ? sections : without
    }
}
