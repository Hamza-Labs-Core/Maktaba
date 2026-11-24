#if os(macOS)
import AppKit
import SwiftUI
import Testing
import MaktabaKit
@testable import Maktaba_Player_Mac

/// Story 13.16: the Mac Player's sidebar lists its sections as the apps' side list, in the
/// person's order, Profile at its bottom; the View menu's ⌘1 … follow it; ↑ and ↓ move through
/// it; a section keeps what it holds while another is in view, and ⌘F is taken once.
@MainActor @Suite struct MacSidebarLogic {
    @Test func profileSitsAtTheBottomAndTheRestKeepTheirOrder() {
        let rows = MacSidebarRows(PlayerSection.allCases)
        #expect(rows.top == [.home, .libraries, .liveTV, .search])
        #expect(rows.bottom == .profile)
        #expect(rows.all == PlayerSection.allCases)
    }

    @Test func withoutProfileNothingSitsAtTheBottom() {
        let rows = MacSidebarRows([.home, .libraries, .search])
        #expect(rows.top == [.home, .libraries, .search])
        #expect(rows.bottom == nil)
    }

    /// Profile alone is never a bottom row under an empty list (the shell shows one section
    /// with no sidebar at all; a sidebar of one keeps it at the top).
    @Test func profileAloneStaysAtTheTop() {
        let rows = MacSidebarRows([.profile])
        #expect(rows.top == [.profile])
        #expect(rows.bottom == nil)
    }

    /// The person's order and hidden sections, and the View menu numbering the sidebar as it
    /// stands (the shell passes `rows.all` to `playerMenuTarget`).
    @Test func theArrangedOrderIsTheSidebarsAndTheMenusNumbers() {
        var arrangement = SidebarArrangement()
        // Search dragged to the top
        arrangement.move(MacSidebarRows(PlayerSection.allCases).top.map(\.rawValue), fromOffsets: IndexSet(integer: 3), toOffset: 0)
        let rows = MacSidebarRows(PlayerSection.allCases, arrangement: SidebarArrangement(stored: arrangement.stored))
        #expect(rows.top == [.search, .home, .libraries, .liveTV])
        #expect(rows.bottom == .profile, "Profile stays at the bottom")
        let menu = PlayerViewMenu.items(rows.all)
        #expect(menu.map(\.section) == [.search, .home, .libraries, .liveTV, .profile])
        #expect(menu.map(\.key.character) == ["1", "2", "3", "4", "5"])

        arrangement.hide(PlayerSection.liveTV.rawValue)
        let hidden = MacSidebarRows(PlayerSection.allCases, arrangement: arrangement, keep: .home)
        #expect(hidden.all == [.search, .home, .libraries, .profile])
        #expect(PlayerViewMenu.items(hidden.all).map(\.key.character) == ["1", "2", "3", "4"], "⌘4 is Profile now")
        // hidden while in view: it stays until another section is chosen
        #expect(MacSidebarRows(PlayerSection.allCases, arrangement: arrangement, keep: .liveTV).top.contains(.liveTV))
    }

    @Test func upAndDownChooseTheNeighbouringSection() {
        let rows = MacSidebarRows(PlayerSection.allCases)
        #expect(rows.next(after: .home, .down) == .libraries)
        #expect(rows.next(after: .libraries, .up) == .home)
        #expect(rows.next(after: .search, .down) == .profile, "down from the last row to Profile at the bottom")
        #expect(rows.next(after: .home, .up) == nil)
        #expect(rows.next(after: .profile, .down) == nil)
        #expect(rows.next(after: .home, .left) == nil && rows.next(after: .home, .right) == nil)
    }

    /// Switching away and back: the shell's keeper hands Libraries the same state (its stack and
    /// its chosen library) and Search the same models (its query).
    @Test func aSectionFindsWhatItHeldOnComingBack() throws {
        let url = try #require(URL(string: "http://127.0.0.1:9"))
        let client = APIClient(home: url, tokens: TokenStore(app: "test.mac.keeper", home: url))
        let defaults = try #require(UserDefaults(suiteName: "test.mac.keeper"))
        let session = AppSession(app: "test.mac.keeper", defaults: defaults, asksHomes: false)
        let user = try JSONDecoder().decode(User.self, from: Data(#"{"id":"u1","username":"viewer","is_admin":false}"#.utf8))
        let context = PlayerContext(session: session, user: user, client: client)
        let keeper = SectionKeeper<PlayerSection>(lasting: [.libraries, .profile])

        let libraries = keeper.state(.libraries, epoch: 0) { LibrariesState(session: session, client: client) }
        libraries.choice = .library("lib-docs")
        libraries.path = [.series(id: "s-seerah"), .video(id: "v1")]
        let search = keeper.state(.search, epoch: 0) { SearchKept(context) }
        search.model.query = "mecca"
        // Home chosen in between
        _ = keeper.state(.home, epoch: 0) { HomeScreen.model(context) }

        let back = keeper.state(.libraries, epoch: 0) { LibrariesState(session: session, client: client) }
        #expect(back === libraries)
        #expect(back.choice == .library("lib-docs"))
        #expect(back.path == [.series(id: "s-seerah"), .video(id: "v1")])
        #expect(keeper.state(.search, epoch: 0) { SearchKept(context) }.model.query == "mecca")
        // a new parental limit: Search reads again, Libraries keeps its stack and reads its lists again itself
        #expect(keeper.state(.search, epoch: 1) { SearchKept(context) }.model.query == "")
        #expect(keeper.state(.libraries, epoch: 1) { LibrariesState(session: session, client: client) }.path.count == 2)
    }

    /// ⌘F: taken once by the search field in view; a Search screen built again later finds
    /// nothing to take (it does not grab the focus on every visit).
    @Test func eachFindIsTakenOnce() {
        let find = FindRequests()
        #expect(!find.take(), "no ⌘F yet")
        find.ask()
        #expect(find.take())
        #expect(!find.take(), "Search shown again: nothing left")
        find.ask()
        find.ask()
        #expect(find.take())
        #expect(!find.take())
    }
}

/// Story 13.16's references, beside the pages' (`PageSnapshots`, the same tolerance and the
/// same way to make or change a reference: `tools/apple/snapshots.py`): the sidebar, in English
/// and Arabic (right to left); Profile's are in `ProfileSnapshots`.
extension PageSnapshots {
    /// The sidebar column, Libraries chosen: brass symbols, the chosen row on the brass fill,
    /// Profile at the bottom. In Arabic the rows mirror; the window puts the column at the
    /// right (AppKit, in an app started in Arabic: not drawn here).
    @Test(arguments: ["en", "ar"]) func sidebarMatchesItsReference(_ lang: String) async throws {
        let size = CGSize(width: 232, height: 420)
        let root = MacSidebar(rows: MacSidebarRows(PlayerSection.allCases), selection: .constant(.libraries),
                              arrangement: .constant(SidebarArrangement()))
            .environment(\.locale, Locale(identifier: lang))
            .environment(\.layoutDirection, lang == "ar" ? .rightToLeft : .leftToRight)
            .preferredColorScheme(.dark)
            .frame(width: size.width, height: size.height)
            .background(Color.mktBackground)
        let (image, text) = try await Self.render(root, size: size, quiet: nil)
        #expect(text.contains(lang == "ar" ? "Outline: الأقسام" : "Outline: Sections"), "VoiceOver's name for the sidebar: \(text)")
        #expect(text.contains(lang == "ar" ? "المكتبات" : "Libraries"), "the sections by name: \(text)")
        try Self.compare("sidebar-\(lang)", image: image, text: text)
    }

    /// A view drawn once `home` (if any) has had no request for a second (15 s at most), and its
    /// words, a list's rows among them (`listWords`).
    static func render(_ view: some View, size: CGSize, quiet home: MockHome?) async throws -> (NSBitmapImageRep, String) {
        let window = NSWindow(contentRect: NSRect(origin: .zero, size: size), styleMask: [.borderless],
                              backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.appearance = NSAppearance(named: .darkAqua)
        defer { window.close() }
        let hosting = NSHostingView(rootView: view)
        window.contentView = hosting
        window.orderFrontRegardless()
        let deadline = Date.now.addingTimeInterval(15)
        var count = -1
        var quietSince = Date.now
        while let home, Date.now < deadline {
            try await Task.sleep(for: .milliseconds(100))
            let seen = home.seen
            if seen.count != count {
                count = seen.count
                quietSince = .now
            } else if count > 0, Date.now.timeIntervalSince(quietSince) >= 1 {
                break
            }
        }
        try await Task.sleep(for: .milliseconds(500))
        hosting.layoutSubtreeIfNeeded()
        window.displayIfNeeded()
        let rep = try #require(hosting.bitmapImageRepForCachingDisplay(in: hosting.bounds))
        hosting.cacheDisplay(in: hosting.bounds, to: rep)
        return (rep, try await listWords(of: hosting))
    }

    /// As `words(of:)`, and into a list's rows: a SwiftUI `List` on the Mac is an outline whose
    /// rows and cells answer only the older attribute calls (`accessibilityAttributeValue`), so
    /// `collect` stops at the outline. Kept apart from `collect`, so the pages' references stay.
    static func listWords(of hosting: NSView) async throws -> String {
        let enhanced = NSAccessibility.Attribute(rawValue: "AXEnhancedUserInterface")
        NSApp.accessibilitySetValue(true, forAttribute: enhanced)
        defer { NSApp.accessibilitySetValue(false, forAttribute: enhanced) }
        try await Task.sleep(for: .milliseconds(300))
        var lines: [String] = []
        collectRows(hosting, into: &lines)
        return lines.joined(separator: "\n") + "\n"
    }

    static func collectRows(_ element: Any, into lines: inout [String]) {
        guard let o = element as? NSObject else { return }
        let role = ((ax(o, "accessibilityRole") as? String) ?? (o.accessibilityAttributeValue(.role) as? String))
            .map { $0.hasPrefix("AX") ? String($0.dropFirst(2)) : $0 } ?? "?"
        if role == "ScrollBar" { return }
        var words: [String] = []
        for key in ["accessibilityLabel", "accessibilityValue", "accessibilityTitle"] {
            let w: String? = switch ax(o, key) {
            case let s as String: s
            case let a as NSAttributedString: a.string
            case let n as NSNumber: n.stringValue
            default: nil
            }
            if let w, !w.isEmpty, !words.contains(w) { words.append(w) }
        }
        if !words.isEmpty { lines.append("\(role): " + words.joined(separator: " | ")) }
        let children = (ax(o, "accessibilityChildren") as? [Any]) ?? (o.accessibilityAttributeValue(.children) as? [Any]) ?? []
        for child in children { collectRows(child, into: &lines) }
    }

    /// Attaches `name`'s image and words, then compares them with its references, as the
    /// pages' test does.
    static func compare(_ name: String, image: NSBitmapImageRep, text: String) throws {
        let png = try #require(image.representation(using: .png, properties: [:]))
        Attachment.record(png, named: "\(name).png")
        Attachment.record(Data(text.utf8), named: "\(name).txt")
        let bundle = Bundle(for: MacSnapshotBundle.self)
        if let textURL = bundle.url(forResource: name, withExtension: "txt", subdirectory: "Snapshots"),
           let refText = try? String(contentsOf: textURL, encoding: .utf8) {
            let diff = firstDifference(text, refText)
            #expect(diff == nil, "\(name)'s words differ from its text reference: \(diff ?? ""); if the change is meant, take the attached \(name).txt with `tools/apple/snapshots.py <run id>` and commit it")
        } else {
            Issue.record("no text reference for \(name): take it from CI's run with `tools/apple/snapshots.py <run id>` (it copies the attached \(name).txt to PlayerMacTests/Snapshots/), read it, and commit it")
        }
        guard let refURL = bundle.url(forResource: name, withExtension: "png", subdirectory: "Snapshots"),
              let refData = try? Data(contentsOf: refURL),
              let ref = NSBitmapImageRep(data: refData) else {
            Issue.record("no reference for \(name): take it from CI's run with `tools/apple/snapshots.py <run id>` (it copies the attached \(name).png to PlayerMacTests/Snapshots/), look at it, and commit it")
            return
        }
        let diff = try difference(image, ref)
        #expect(diff <= tolerance,
                "\(name) differs from its reference in \(String(format: "%.2f", diff * 100)) % of its pixels; if the change is meant, take the attached image with `tools/apple/snapshots.py <run id>` and commit it")
    }
}

/// This test bundle, where the references are.
private final class MacSnapshotBundle {}

/// Story 13.16's Profile reference, in its own suite: it is drawn in the language the test host
/// runs in, so its words are the app's own (MaktabaKit's, the theme's and the language's choices
/// among them) and AppKit's parts are laid out as in the app. CI's Mac job runs it twice, with
/// every other test (English) and again with the host in Arabic (`-testLanguage ar`).
@MainActor @Suite(.serialized) struct ProfileSnapshots {
    /// The language the host was started in, as the app picks it.
    static let lang = LanguageChoice.isRightToLeft() ? "ar" : "en"

    /// Profile in the apps' row style: full-width rows, brass symbols, the group heads muted,
    /// the Language row's English and العربية.
    @Test func profileMatchesItsReference() async throws {
        let lang = Self.lang
        let home = try MockHome()
        home.clock = PageSnapshots.clock
        try home.start()
        let url = try #require(URL(string: home.url))
        let client = APIClient(home: url, tokens: TokenStore(app: "test.mac.snapshots", home: url))
        let defaults = try #require(UserDefaults(suiteName: "test.mac.snapshots.profile-\(lang)"))
        let session = AppSession(app: "test.mac.snapshots", defaults: defaults, asksHomes: false)
        let user = try JSONDecoder().decode(User.self, from: Data(#"{"id":"u1","username":"viewer","is_admin":false}"#.utf8))
        let context = PlayerContext(session: session, user: user, client: client)
        let size = CGSize(width: 760, height: 1400)
        let root = NavigationStack { ProfileScreen(context: context) }
            .environment(\.locale, AppLanguage.locale)
            .environment(\.layoutDirection, lang == "ar" ? .rightToLeft : .leftToRight)
            .preferredColorScheme(.dark)
            .tint(.brass)
            .frame(width: size.width, height: size.height)
        let (image, text) = try await PageSnapshots.render(root, size: size, quiet: home)
        #expect(text.contains(lang == "ar" ? "سجل المشاهدة" : "Watch history"), "Profile's rows: \(text)")
        if lang == "ar" {
            for english in ["Approve a TV", "Report a problem", "My reports", "Dark", "Light", "System"] {
                #expect(!text.contains(english), "\(english) in Arabic: \(text)")
            }
        }
        try PageSnapshots.compare("profile-\(lang)", image: image, text: text)
    }
}
#endif
