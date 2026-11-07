#if os(macOS)
import AppKit
import SwiftUI
import Testing
import MaktabaKit
@testable import Maktaba_Player_Mac

/// Issue #876: the library, series and video pages compared with committed references, so a
/// layout that moves fails here, as Roborazzi's goldens fail Android's. Each page is the real
/// screen (the same sources as the iPhone and iPad apps) drawn in the dark theme, fed by
/// MockHome's fixtures: with no panel, with "From plugins" collapsed and open, in English and
/// Arabic (right to left), at the default text size and at AX3.
///
/// The references are `PlayerMacTests/Snapshots/<name>.png`, resources of this bundle (the
/// host app is sandboxed: they are read from the bundle, never the source tree). A page that
/// differs from its reference in more than `PageSnapshots.tolerance` of its pixels fails; a
/// page without a reference fails too. Either way the page drawn now is attached to the test
/// (`mac.xcresult` in CI's `player-ui-tests-mac` artifact): a reference is made or changed by
/// committing that attachment: `tools/apple/snapshots.py <run id>` copies them here.
@MainActor @Suite(.serialized) struct PageSnapshots {
    enum Page: String, CaseIterable, Sendable {
        case library, series, video

        var subject: String {
            switch self {
            case .library: "library:lib-docs"
            case .series: "series:s-seerah"
            case .video: "video:v1"
            }
        }
    }

    enum Panels: String, Sendable { case none, collapsed, open }

    struct Case: Sendable, CustomTestStringConvertible {
        let page: Page
        let panels: Panels
        let lang: String
        let ax3: Bool

        var name: String { "\(page.rawValue)-\(panels.rawValue)-\(lang)\(ax3 ? "-ax3" : "")" }
        var testDescription: String { name }
    }

    /// Every page with no panel, collapsed and open, in both languages; and open at AX3, where
    /// a panel's rows wrap.
    nonisolated static let cases: [Case] = Page.allCases.flatMap { page in
        ["en", "ar"].flatMap { lang in
            [Panels.none, .collapsed, .open].map { Case(page: page, panels: $0, lang: lang, ax3: false) }
                + [Case(page: page, panels: .open, lang: lang, ax3: true)]
        }
    }

    /// One plugin's panel: a key-value row and chips, the blocks every app draws.
    nonisolated static let panel = #"""
    {"panels":[{"plugin_id":"topics","plugin":"Topics","title":"Topics","blocks":[
     {"type":"kv","value":[{"label":"Era","value":"7th century"},{"label":"Speaker","value":"Dr. Example"}]},
     {"type":"chips","value":["Hijra","Medina"]}]}]}
    """#

    /// The share of pixels that may differ (anti-aliasing), each by more than `channel`.
    static let tolerance = 0.002
    static let channel = 24
    static let size = CGSize(width: 760, height: 1500)

    /// The window for a page: the video page is taller, as its "From plugins" comes after the
    /// chapters and the transcript.
    static func size(of page: Page) -> CGSize {
        page == .video ? CGSize(width: size.width, height: 2600) : size
    }

    @Test(arguments: cases) func pageMatchesItsReference(_ c: Case) async throws {
        let home = try MockHome()
        try home.start()
        let features = MockHome.features.map { #""\#($0)":"on""# }.joined(separator: ",")
        home.stub("GET", "/api/capabilities", body: #"{"etag":"c-snap","features":{\#(features),"plugin_panels":"on"}}"#,
                  headers: ["ETag": #""c-snap""#])
        home.stub("GET", "/api/panels", body: c.panels == .none ? #"{"panels":[]}"# : Self.panel)

        let url = try #require(URL(string: home.url))
        let client = APIClient(home: url, tokens: TokenStore(app: "test.mac.snapshots", home: url))
        let caps = CapabilitiesStore()
        caps.use(client)
        await caps.refresh()
        #expect(caps.current.panels, "the home answers plugin_panels: on")
        let defaults = try #require(UserDefaults(suiteName: "test.mac.snapshots.\(c.name)"))
        let session = AppSession(app: "test.mac.snapshots", defaults: defaults, asksHomes: false, capabilities: caps)
        let user = try JSONDecoder().decode(User.self, from: Data(#"{"id":"u1","username":"viewer","is_admin":false}"#.utf8))
        let context = PlayerContext(session: session, user: user, client: client)

        let image = try await Self.draw(Self.screen(c.page, context: context), c: c, home: home)
        let png = try #require(image.representation(using: .png, properties: [:]))
        Attachment.record(png, named: "\(c.name).png")

        guard let refURL = Bundle(for: SnapshotBundle.self).url(forResource: c.name, withExtension: "png"),
              let refData = try? Data(contentsOf: refURL),
              let ref = NSBitmapImageRep(data: refData) else {
            Issue.record("no reference for \(c.name): commit the attached \(c.name).png as PlayerMacTests/Snapshots/\(c.name).png")
            return
        }
        let diff = try Self.difference(image, ref)
        #expect(diff <= Self.tolerance,
                "\(c.name) differs from its reference in \(String(format: "%.2f", diff * 100)) % of its pixels; if the change is meant, commit the attached image")
    }

    @ViewBuilder static func screen(_ page: Page, context: PlayerContext) -> some View {
        switch page {
        case .library: LibraryScreen(context: context, id: "lib-docs")
        case .series: SeriesScreen(context: context, id: "s-seerah")
        case .video: VideoScreen(context: context, id: "v1")
        }
    }

    /// The screen in a window of `size`, dark, in the case's language and text size, drawn
    /// once its requests have gone quiet (the panels asked, the fixtures and posters read).
    static func draw(_ screen: some View, c: Case, home: MockHome) async throws -> NSBitmapImageRep {
        let size = size(of: c.page)
        let root = NavigationStack { screen }
            .environment(\.locale, Locale(identifier: c.lang))
            .environment(\.layoutDirection, c.lang == "ar" ? .rightToLeft : .leftToRight)
            .environment(\.pluginPanelsOpenAtFirst, c.panels == .open)
            .dynamicTypeSize(c.ax3 ? .accessibility3 : .large)
            .preferredColorScheme(.dark)
            .frame(width: size.width, height: size.height)
        let window = NSWindow(contentRect: NSRect(origin: .zero, size: size), styleMask: [.borderless],
                              backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.appearance = NSAppearance(named: .darkAqua)
        defer { window.close() }
        let hosting = NSHostingView(rootView: root)
        window.contentView = hosting
        window.orderFrontRegardless()

        // quiet: the panels were asked for and no request came for a second (15 s at most)
        let deadline = Date.now.addingTimeInterval(15)
        var count = -1
        var quietSince = Date.now
        while Date.now < deadline {
            try await Task.sleep(for: .milliseconds(100))
            let seen = home.seen
            if seen.count != count {
                count = seen.count
                quietSince = .now
            } else if seen.contains("GET /api/panels"), Date.now.timeIntervalSince(quietSince) >= 1 {
                break
            }
        }
        #expect(home.seen.contains("GET /api/panels"), "the page asked for \(c.page.subject)'s panels: \(home.seen)")
        try await Task.sleep(for: .milliseconds(500))
        hosting.layoutSubtreeIfNeeded()
        window.displayIfNeeded()
        let rep = try #require(hosting.bitmapImageRepForCachingDisplay(in: hosting.bounds))
        hosting.cacheDisplay(in: hosting.bounds, to: rep)
        return rep
    }

    /// The share of pixels where a channel differs by more than `channel` (1 when the sizes differ).
    static func difference(_ a: NSBitmapImageRep, _ b: NSBitmapImageRep) throws -> Double {
        guard a.pixelsWide == b.pixelsWide, a.pixelsHigh == b.pixelsHigh else { return 1 }
        let pa = try rgba(a), pb = try rgba(b)
        var differ = 0
        for i in stride(from: 0, to: pa.count, by: 4) where (0..<4).contains(where: { abs(Int(pa[i + $0]) - Int(pb[i + $0])) > channel }) {
            differ += 1
        }
        return Double(differ) / Double(a.pixelsWide * a.pixelsHigh)
    }

    /// The pixels as 8-bit RGBA in sRGB, whatever the image's own format.
    static func rgba(_ rep: NSBitmapImageRep) throws -> [UInt8] {
        let w = rep.pixelsWide, h = rep.pixelsHigh
        var out = [UInt8](repeating: 0, count: w * h * 4)
        let cg = try #require(rep.cgImage)
        let space = try #require(CGColorSpace(name: CGColorSpace.sRGB))
        try out.withUnsafeMutableBytes { buf in
            let ctx = try #require(CGContext(data: buf.baseAddress, width: w, height: h, bitsPerComponent: 8, bytesPerRow: w * 4,
                                             space: space, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue))
            ctx.draw(cg, in: CGRect(x: 0, y: 0, width: w, height: h))
        }
        return out
    }
}

/// This test bundle, where the references are.
private final class SnapshotBundle {}
#endif
