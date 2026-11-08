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
///
/// Beside each image is a text reference, `<name>.txt`: the page's accessibility elements in
/// order, each one's role and words (label, value, title). It must match exactly, so a wording
/// change the pixel tolerance lets through (an English phrase on an Arabic page is a few hundred
/// pixels) fails here. It is attached and copied the same way as the image.
///
/// The Apple TV's video page draws MaktabaKit's TV form of the panels; `tvPanelMatchesItsReference`
/// draws that form here (story 54.20 T05), as `tv-panel-<lang>.png` and `.txt`.
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

        let (image, text) = try await Self.draw(Self.screen(c.page, context: context), c: c, home: home)
        let png = try #require(image.representation(using: .png, properties: [:]))
        Attachment.record(png, named: "\(c.name).png")
        Attachment.record(Data(text.utf8), named: "\(c.name).txt")

        if let textURL = Bundle(for: SnapshotBundle.self).url(forResource: c.name, withExtension: "txt", subdirectory: "Snapshots"),
           let refText = try? String(contentsOf: textURL, encoding: .utf8) {
            let diff = Self.firstDifference(text, refText)
            #expect(diff == nil, "\(c.name)'s words differ from its text reference: \(diff ?? ""); if the change is meant, commit the attached \(c.name).txt")
        } else {
            Issue.record("no text reference for \(c.name): commit the attached \(c.name).txt as PlayerMacTests/Snapshots/\(c.name).txt")
        }

        guard let refURL = Bundle(for: SnapshotBundle.self).url(forResource: c.name, withExtension: "png", subdirectory: "Snapshots"),
              let refData = try? Data(contentsOf: refURL),
              let ref = NSBitmapImageRep(data: refData) else {
            Issue.record("no reference for \(c.name): commit the attached \(c.name).png as PlayerMacTests/Snapshots/\(c.name).png")
            return
        }
        let diff = try Self.difference(image, ref)
        #expect(diff <= Self.tolerance,
                "\(c.name) differs from its reference in \(String(format: "%.2f", diff * 100)) % of its pixels; if the change is meant, commit the attached image")
    }

    /// The Apple TV video page's panels (story 54.20 T05): MaktabaKit's own TV form, drawn here
    /// on the Mac as the TV draws it (`pluginPanelsTVForm`: the TV's subset of blocks, open,
    /// no chips), on the TV page's background at a 16:9 size, in English and Arabic (right to
    /// left). One value is longer than the text rules allow: it ends in "…" and wraps. The
    /// Apple TV's own text styles are larger than the Mac's; this is the closest the hosted
    /// Mac tests draw it (no tvOS simulator snapshot runs in CI).
    @Test(arguments: ["en", "ar"]) func tvPanelMatchesItsReference(_ lang: String) async throws {
        let name = "tv-panel-\(lang)"
        let panels = try JSONDecoder().decode([PanelView].self, from: Data(Self.tvPanel(lang).utf8))
        #expect(PanelText.shown(panels[0].blocks, tv: true).map(\.type) == ["kv", "badges", "markers"])
        let size = Self.tvSize
        let root = ScrollView {
            VStack(alignment: .leading, spacing: 24) {
                Text(verbatim: "The Night Journey").font(.largeTitle.bold())
                PluginPanelsSection<String>(panels: panels, play: { _ in })
            }
            .padding(40)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .background(Color(red: 0x0B / 255, green: 0x11 / 255, blue: 0x1C / 255))
        .environment(\.pluginPanelsTVForm, true)
        .environment(\.locale, Locale(identifier: lang))
        .environment(\.layoutDirection, lang == "ar" ? .rightToLeft : .leftToRight)
        .preferredColorScheme(.dark)
        .frame(width: size.width, height: size.height)

        let (image, text) = try await Self.render(root, size: size)
        let png = try #require(image.representation(using: .png, properties: [:]))
        Attachment.record(png, named: "\(name).png")
        Attachment.record(Data(text.utf8), named: "\(name).txt")
        // the TV's subset only (no chip), the long value cut at the text rules' length
        #expect(!text.contains(lang == "ar" ? "تاريخ" : "History"), "no chips in the TV form: \(text)")
        #expect(text.contains("…"), "the long value ends in an ellipsis: \(text)")

        if let textURL = Bundle(for: SnapshotBundle.self).url(forResource: name, withExtension: "txt", subdirectory: "Snapshots"),
           let refText = try? String(contentsOf: textURL, encoding: .utf8) {
            let diff = Self.firstDifference(text, refText)
            #expect(diff == nil, "\(name)'s words differ from its text reference: \(diff ?? ""); if the change is meant, commit the attached \(name).txt")
        } else {
            Issue.record("no text reference for \(name): commit the attached \(name).txt as PlayerMacTests/Snapshots/\(name).txt")
        }
        guard let refURL = Bundle(for: SnapshotBundle.self).url(forResource: name, withExtension: "png", subdirectory: "Snapshots"),
              let refData = try? Data(contentsOf: refURL),
              let ref = NSBitmapImageRep(data: refData) else {
            Issue.record("no reference for \(name): commit the attached \(name).png as PlayerMacTests/Snapshots/\(name).png")
            return
        }
        let diff = try Self.difference(image, ref)
        #expect(diff <= Self.tolerance,
                "\(name) differs from its reference in \(String(format: "%.2f", diff * 100)) % of its pixels; if the change is meant, commit the attached image")
    }

    /// A 1080p TV's 16:9, at half its points: the Mac's text styles are about half the Apple TV's.
    static let tvSize = CGSize(width: 960, height: 540)

    /// One plugin's panel as a home answers it in `lang`: every block type the TV form keeps
    /// or drops, and a value longer than `PanelText.maxShown`.
    nonisolated static func tvPanel(_ lang: String) -> String {
        let ar = lang == "ar"
        let long = String(repeating: ar ? "رحلة ليلية نحو الشمال عبر الصحراء الواسعة، " : "A journey north through the desert by night, ", count: 6)
        return """
        [{"plugin_id":"scenes","plugin":"\(ar ? "المشاهد" : "Scenes")","title":"\(ar ? "المشاهد" : "Scenes")","blocks":[
         {"type":"kv","value":[{"label":"\(ar ? "الحقبة" : "Era")","value":"\(ar ? "العشرينيات" : "1920s")"},
                               {"label":"\(ar ? "ملاحظات" : "Notes")","value":"\(long)"}]},
         {"type":"badges","value":[{"text":"\(ar ? "مرمّم" : "Restored")"},{"text":"\(ar ? "عالي الدقة" : "HD")"}]},
         {"type":"chips","value":["\(ar ? "تاريخ" : "History")"]},
         {"type":"markers","value":[{"t_ms":754000,"label":"\(ar ? "الطريق شمالاً" : "The road north")"}]}]}]
        """
    }

    /// A view with no requests, drawn in a borderless dark window of `size` once laid out, and
    /// its words (as `draw`).
    static func render(_ view: some View, size: CGSize) async throws -> (NSBitmapImageRep, String) {
        let window = NSWindow(contentRect: NSRect(origin: .zero, size: size), styleMask: [.borderless],
                              backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.appearance = NSAppearance(named: .darkAqua)
        defer { window.close() }
        let hosting = NSHostingView(rootView: view)
        window.contentView = hosting
        window.orderFrontRegardless()
        try await Task.sleep(for: .milliseconds(500))
        hosting.layoutSubtreeIfNeeded()
        window.displayIfNeeded()
        let rep = try #require(hosting.bitmapImageRepForCachingDisplay(in: hosting.bounds))
        hosting.cacheDisplay(in: hosting.bounds, to: rep)
        return (rep, try await words(of: hosting))
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
    static func draw(_ screen: some View, c: Case, home: MockHome) async throws -> (NSBitmapImageRep, String) {
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
        return (rep, try await words(of: hosting))
    }

    /// The page's accessibility elements in order, one line each: its role and words. SwiftUI
    /// builds its accessibility tree only for an assistive client, which asks for it with
    /// AXEnhancedUserInterface; the test asks the same way, after the image is drawn, and
    /// stops asking once it has read the tree.
    static func words(of hosting: NSView) async throws -> String {
        let enhanced = NSAccessibility.Attribute(rawValue: "AXEnhancedUserInterface")
        NSApp.accessibilitySetValue(true, forAttribute: enhanced)
        defer { NSApp.accessibilitySetValue(false, forAttribute: enhanced) }
        try await Task.sleep(for: .milliseconds(300))
        var lines: [String] = []
        collect(hosting, into: &lines)
        return lines.joined(separator: "\n") + "\n"
    }

    /// `element`'s line (when it has words) and its children's, depth first.
    static func collect(_ element: Any, into lines: inout [String]) {
        guard let o = element as? NSObject else { return }
        let role = (ax(o, "accessibilityRole") as? String).map { $0.hasPrefix("AX") ? String($0.dropFirst(2)) : $0 } ?? "?"
        var words: [String] = []
        for key in ["accessibilityLabel", "accessibilityValue", "accessibilityTitle"] {
            let w: String? = switch ax(o, key) {
            case let s as String: s
            case let n as NSNumber: n.stringValue
            default: nil
            }
            if let w, !w.isEmpty, !words.contains(w) { words.append(w) }
        }
        if !words.isEmpty { lines.append("\(role): " + words.joined(separator: " | ")) }
        for child in (ax(o, "accessibilityChildren") as? [Any]) ?? [] { collect(child, into: &lines) }
    }

    /// An accessibility getter by name: SwiftUI's elements are not NSAccessibilityElements,
    /// and Swift sees the getters as both properties and methods, so they are asked by selector.
    static func ax(_ o: NSObject, _ name: String) -> Any? {
        let selector = NSSelectorFromString(name)
        guard o.responds(to: selector) else { return nil }
        return o.perform(selector)?.takeUnretainedValue()
    }

    /// The first line where `text` and `reference` differ, both shown; nil when they match.
    static func firstDifference(_ text: String, _ reference: String) -> String? {
        let a = text.components(separatedBy: "\n"), b = reference.components(separatedBy: "\n")
        for i in 0..<max(a.count, b.count) {
            let x = i < a.count ? a[i] : "(none)", y = i < b.count ? b[i] : "(none)"
            if x != y { return "line \(i + 1): now \"\(x)\", reference \"\(y)\"" }
        }
        return nil
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
