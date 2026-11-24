#if os(macOS)
import AppKit
import SwiftUI
import Testing
import MaktabaKit
@testable import Maktaba_Player_Mac

/// Story 13.16: the Mac Player's sidebar lists its sections as the apps' side list, Profile at
/// its bottom.
@MainActor @Suite struct MacSidebarLogic {
    @Test func profileSitsAtTheBottomAndTheRestKeepTheirOrder() {
        let rows = MacSidebarRows(PlayerSection.allCases)
        #expect(rows.top == [.home, .libraries, .liveTV, .search])
        #expect(rows.bottom == .profile)
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
}

/// Story 13.16's references, beside the pages' (`PageSnapshots`, the same tolerance and the
/// same way to make or change a reference: `tools/apple/snapshots.py`): the sidebar and
/// Profile, in English and Arabic (right to left).
extension PageSnapshots {
    /// The sidebar column, Libraries chosen: brass symbols, the chosen row on the brass fill,
    /// Profile at the bottom. In Arabic the rows mirror; the window puts the column at the
    /// right (AppKit, in an app started in Arabic: not drawn here).
    @Test(arguments: ["en", "ar"]) func sidebarMatchesItsReference(_ lang: String) async throws {
        let size = CGSize(width: 232, height: 420)
        let root = MacSidebar(sections: PlayerSection.allCases, selection: .constant(.libraries))
            .environment(\.locale, Locale(identifier: lang))
            .environment(\.layoutDirection, lang == "ar" ? .rightToLeft : .leftToRight)
            .preferredColorScheme(.dark)
            .frame(width: size.width, height: size.height)
            .background(Color.mktBackground)
        let (image, text) = try await Self.render(root, size: size)
        #expect(text.contains(lang == "ar" ? "المكتبات" : "Libraries"), "the sections by name: \(text)")
        try Self.compare("sidebar-\(lang)", image: image, text: text)
    }

    /// Profile in the apps' row style: full-width rows, brass symbols, the group heads muted,
    /// the Language row's English and العربية. MaktabaKit's own words (Approve a TV or device,
    /// Report a problem) and the Language row's choice follow the test host's language, as the
    /// app's follow its own: English on both pages.
    @Test(arguments: ["en", "ar"]) func profileMatchesItsReference(_ lang: String) async throws {
        let home = try MockHome()
        home.clock = Self.clock
        try home.start()
        let url = try #require(URL(string: home.url))
        let client = APIClient(home: url, tokens: TokenStore(app: "test.mac.snapshots", home: url))
        let defaults = try #require(UserDefaults(suiteName: "test.mac.snapshots.profile-\(lang)"))
        let session = AppSession(app: "test.mac.snapshots", defaults: defaults, asksHomes: false)
        let user = try JSONDecoder().decode(User.self, from: Data(#"{"id":"u1","username":"viewer","is_admin":false}"#.utf8))
        let context = PlayerContext(session: session, user: user, client: client)
        let size = CGSize(width: 760, height: 1400)
        let root = NavigationStack { ProfileScreen(context: context) }
            .environment(\.locale, Locale(identifier: lang))
            .environment(\.layoutDirection, lang == "ar" ? .rightToLeft : .leftToRight)
            .preferredColorScheme(.dark)
            .tint(.brass)
            .frame(width: size.width, height: size.height)
        let (image, text) = try await Self.render(root, size: size, quiet: home)
        #expect(text.contains(lang == "ar" ? "سجل المشاهدة" : "Watch history"), "Profile's rows: \(text)")
        try Self.compare("profile-\(lang)", image: image, text: text)
    }

    /// A view drawn once `home` has had no request for a second (15 s at most), and its words.
    static func render(_ view: some View, size: CGSize, quiet home: MockHome) async throws -> (NSBitmapImageRep, String) {
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
        while Date.now < deadline {
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
        return (rep, try await words(of: hosting))
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
            #expect(diff == nil, "\(name)'s words differ from its text reference: \(diff ?? ""); if the change is meant, commit the attached \(name).txt")
        } else {
            Issue.record("no text reference for \(name): commit the attached \(name).txt as PlayerMacTests/Snapshots/\(name).txt")
        }
        guard let refURL = bundle.url(forResource: name, withExtension: "png", subdirectory: "Snapshots"),
              let refData = try? Data(contentsOf: refURL),
              let ref = NSBitmapImageRep(data: refData) else {
            Issue.record("no reference for \(name): commit the attached \(name).png as PlayerMacTests/Snapshots/\(name).png")
            return
        }
        let diff = try difference(image, ref)
        #expect(diff <= tolerance,
                "\(name) differs from its reference in \(String(format: "%.2f", diff * 100)) % of its pixels; if the change is meant, commit the attached image")
    }
}

/// This test bundle, where the references are.
private final class MacSnapshotBundle {}
#endif
