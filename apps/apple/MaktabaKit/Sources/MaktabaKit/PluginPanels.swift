import Foundation
import SwiftUI

// Plugin panels (Epic 54, story 54.20): a plugin's read-only view of a video, series or
// library, declared as data (ui@1) and drawn here with SwiftUI's own views. Every value is
// plain text: a `Text` made from a String (never `LocalizedStringKey` or `AttributedString`,
// which read markdown), no value becomes a link, and the only image is the home's panel
// image route. The API refused anything else when the plugin wrote it; this is the second
// wall. tvOS shows kv, badges and markers only.

/// A JSON value as a panel holds it.
public enum PanelValue: Equatable, Sendable, Decodable {
    case string(String)
    case number(Double)
    case bool(Bool)
    case array([PanelValue])
    case object([String: PanelValue])
    case null

    public init(from decoder: Decoder) throws {
        let c = try decoder.singleValueContainer()
        if c.decodeNil() { self = .null }
        else if let b = try? c.decode(Bool.self) { self = .bool(b) }
        else if let n = try? c.decode(Double.self) { self = .number(n) }
        else if let s = try? c.decode(String.self) { self = .string(s) }
        else if let a = try? c.decode([PanelValue].self) { self = .array(a) }
        else if let o = try? c.decode([String: PanelValue].self) { self = .object(o) }
        else { self = .null }
    }

    public var list: [PanelValue] { if case .array(let a) = self { a } else { [] } }
    public subscript(_ key: String) -> PanelValue { if case .object(let o) = self { o[key] ?? .null } else { .null } }
    public var number: Double? { if case .number(let n) = self { n } else { nil } }
}

public struct PanelBlock: Equatable, Sendable, Decodable {
    public let type: String
    public let label: String?
    public let columns: [String]?
    public let value: PanelValue?
    public let image: String?
}

public struct PanelView: Equatable, Sendable, Decodable, Identifiable {
    public let pluginID: String
    public let plugin: String
    public let title: String
    public let blocks: [PanelBlock]
    public var id: String { pluginID }

    enum CodingKeys: String, CodingKey {
        case pluginID = "plugin_id"
        case plugin, title, blocks
    }
}

public enum PanelText {
    /// The longest text one cell, chip or row shows.
    public static let maxShown = 200

    /// A value as the text a panel shows: strings and numbers only, control and direction
    /// characters removed, cut to `max` characters with an ellipsis. Anything else is "".
    public static func plain(_ v: PanelValue?, max: Int = maxShown) -> String {
        let s: String
        switch v {
        case .string(let x): s = x
        case .number(let n) where n.isFinite:
            s = n == n.rounded() && abs(n) < 1e15 ? String(Int64(n)) : String(n)
        default: return ""
        }
        let kept = String(String.UnicodeScalarView(s.unicodeScalars.filter { !isHidden($0) }))
        return kept.count > max ? String(kept.prefix(max - 1)) + "…" : kept
    }

    public static func plain(_ s: String?, max: Int = maxShown) -> String {
        plain(s.map(PanelValue.string), max: max)
    }

    static func isHidden(_ u: Unicode.Scalar) -> Bool {
        let v = u.value
        return v < 0x20 || (0x7F...0x9F).contains(v) || v == 0x200E || v == 0x200F
            || (0x202A...0x202E).contains(v) || (0x2066...0x2069).contains(v) || v == 0xFEFF
    }

    /// The home's own panel image route only; never a URL a plugin wrote.
    public static func safeImage(_ s: String?) -> String? {
        guard let s, s.hasPrefix("/api/panels/image?"),
              s.unicodeScalars.allSatisfy({ CharacterSet(charactersIn: "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_.:=&%-/?").contains($0) })
        else { return nil }
        return s
    }

    /// "1:01" or "1:02:03" for a marker's time.
    public static func clock(_ ms: Double) -> String {
        let s = Int(ms / 1000)
        let h = s / 3600, m = (s % 3600) / 60, sec = s % 60
        return h > 0 ? String(format: "%d:%02d:%02d", h, m, sec) : String(format: "%d:%02d", m, sec)
    }

    /// The blocks a platform draws: tvOS shows kv, badges and markers only.
    public static func shown(_ blocks: [PanelBlock], tv: Bool) -> [PanelBlock] {
        tv ? blocks.filter { ["kv", "badges", "markers"].contains($0.type) } : blocks
    }
}

extension APIClient {
    /// `GET /api/panels?subject=<kind>:<id>&lang=` (story 54.20). Only called while the home
    /// answers `plugin_panels: on`; a 404 (an older home, or a subject this person cannot see)
    /// is no panels.
    public func panels(kind: String, id: String, language: String) async throws -> [PanelView] {
        let r = request("GET", "/api/panels", query: ["subject": "\(kind):\(id)", "lang": language])
        let (data, response) = try await exchange(r, answered: [404])
        if response?.statusCode == 404 { return [] }
        struct Wire: Decodable { let panels: [PanelView]? }
        return Array(((try? JSONDecoder.home.decode(Wire.self, from: data))?.panels ?? []).prefix(3))
    }
}

/// One plugin's panel, drawn natively. `Route` is the app's navigation value: a marker
/// opens the player at its time, a chip a search for its text, both inside the app.
public struct PluginPanelView<Route: Hashable>: View {
    let panel: PanelView
    /// a marker's time in seconds, as the route that plays from it (nil: not tappable)
    let seek: ((Double) -> Route)?
    /// a chip's text, as the route that searches Maktaba for it (nil: not tappable)
    let search: ((String) -> Route)?
    /// the panel image, loaded by the app with its session (nil: no image)
    let image: ((String) -> AnyView)?
    /// a marker's time in seconds, played by the page itself (the Apple TV, whose pages play
    /// rather than push a route); used when `seek` is nil
    let play: ((Double) -> Void)?

    public init(panel: PanelView, seek: ((Double) -> Route)? = nil, search: ((String) -> Route)? = nil,
                image: ((String) -> AnyView)? = nil, play: ((Double) -> Void)? = nil) {
        self.panel = panel
        self.seek = seek
        self.search = search
        self.image = image
        self.play = play
    }

    static var isTV: Bool {
        #if os(tvOS)
        true
        #else
        false
        #endif
    }

    public var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(alignment: .firstTextBaseline) {
                Text(verbatim: PanelText.plain(panel.title.isEmpty ? panel.plugin : panel.title, max: 100)).font(.headline)
                if !panel.title.isEmpty {
                    Text(verbatim: PanelText.plain(panel.plugin, max: 100)).font(.caption).foregroundStyle(.secondary)
                }
            }
            ForEach(Array(PanelText.shown(panel.blocks, tv: Self.isTV).enumerated()), id: \.offset) { _, b in
                block(b)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding()
        .background(.quaternary.opacity(0.4), in: .rect(cornerRadius: 12))
        .accessibilityElement(children: .contain)
    }

    @ViewBuilder func label(_ b: PanelBlock) -> some View {
        if let l = b.label, !l.isEmpty {
            Text(verbatim: PanelText.plain(l, max: 100)).font(.subheadline.bold())
        }
    }

    @ViewBuilder func block(_ b: PanelBlock) -> some View {
        let v = b.value ?? .null
        VStack(alignment: .leading, spacing: 6) {
            label(b)
            switch b.type {
            case "kv":
                ForEach(Array(v.list.prefix(20).enumerated()), id: \.offset) { _, row in
                    HStack(alignment: .firstTextBaseline) {
                        Text(verbatim: PanelText.plain(row["label"])).foregroundStyle(.secondary)
                        Spacer(minLength: 12)
                        Text(verbatim: PanelText.plain(row["value"])).multilineTextAlignment(.trailing)
                    }
                }
            case "badges":
                FlowRow(items: v.list.prefix(10).map { PanelText.plain($0["text"]) }) { text in
                    Text(verbatim: text).font(.caption).padding(.horizontal, 8).padding(.vertical, 3)
                        .background(.tint.opacity(0.15), in: .capsule)
                }
            case "chips":
                FlowRow(items: v.list.prefix(20).map { PanelText.plain($0) }.filter { !$0.isEmpty }) { text in
                    if let search {
                        NavigationLink(value: search(text)) { Text(verbatim: text) }.buttonStyle(.bordered)
                    } else {
                        Text(verbatim: text)
                    }
                }
            case "table":
                let cols = Array((b.columns ?? []).prefix(6))
                Grid(alignment: .leading, horizontalSpacing: 12, verticalSpacing: 4) {
                    GridRow { ForEach(Array(cols.enumerated()), id: \.offset) { _, c in Text(verbatim: PanelText.plain(c, max: 100)).bold() } }
                    ForEach(Array(v.list.prefix(50).enumerated()), id: \.offset) { _, row in
                        GridRow {
                            ForEach(0..<cols.count, id: \.self) { j in
                                Text(verbatim: PanelText.plain(j < row.list.count ? row.list[j] : nil))
                            }
                        }
                    }
                }
            case "markers":
                ForEach(Array(v.list.prefix(200).enumerated()), id: \.offset) { _, m in
                    if let t = m["t_ms"].number, t >= 0 {
                        let row = HStack {
                            Text(verbatim: PanelText.clock(t)).monospacedDigit().foregroundStyle(.secondary)
                            Text(verbatim: PanelText.plain(m["label"]))
                        }
                        if let seek {
                            NavigationLink(value: seek(t / 1000)) { row }.buttonStyle(.plain)
                        } else if let play {
                            Button { play(t / 1000) } label: { row }
                        } else {
                            row
                        }
                    }
                }
            case "image":
                if let src = PanelText.safeImage(b.image), let image { image(src) }
            case "text":
                Text(verbatim: PanelText.plain(v, max: 1000))
            default:
                EmptyView()
            }
        }
    }
}

/// Chips and badges, wrapped onto lines.
struct FlowRow<Content: View>: View {
    let items: [String]
    @ViewBuilder let content: (String) -> Content

    var body: some View {
        ViewThatFits(in: .horizontal) {
            HStack(spacing: 8) { ForEach(Array(items.enumerated()), id: \.offset) { _, s in content(s) } }
            VStack(alignment: .leading, spacing: 6) { ForEach(Array(items.enumerated()), id: \.offset) { _, s in content(s) } }
        }
    }
}

/// "From plugins", collapsed, below Maktaba's own sections; nothing when there is no panel.
public struct PluginPanelsSection<Route: Hashable>: View {
    let panels: [PanelView]
    let seek: ((Double) -> Route)?
    let search: ((String) -> Route)?
    let image: ((String) -> AnyView)?
    let play: ((Double) -> Void)?
    @State private var open = false

    public init(panels: [PanelView], seek: ((Double) -> Route)? = nil, search: ((String) -> Route)? = nil,
                image: ((String) -> AnyView)? = nil, play: ((Double) -> Void)? = nil) {
        self.panels = panels
        self.seek = seek
        self.search = search
        self.image = image
        self.play = play
    }

    public var body: some View {
        if !panels.isEmpty {
            #if os(tvOS)
            VStack(alignment: .leading, spacing: 12) {
                Text("From plugins", bundle: .module).font(.headline)
                ForEach(panels) { PluginPanelView<Route>(panel: $0, seek: seek, play: play) }
            }
            #else
            DisclosureGroup(isExpanded: $open) {
                VStack(alignment: .leading, spacing: 12) {
                    ForEach(panels) { PluginPanelView<Route>(panel: $0, seek: seek, search: search, image: image, play: play) }
                }
                .padding(.top, 8)
            } label: {
                Text("From plugins", bundle: .module).font(.headline)
            }
            .accessibilityIdentifier("plugin-panels")
            #endif
        }
    }
}

/// A series' or library's panels (story 54.20 §1): asked for `<kind>:<id>` only while the
/// home answers `plugin_panels: on`, into the page's own state, so the page adds its
/// [PluginPanelsSection] only when there is a panel (an empty view would still take the
/// page's spacing, and a task on a view that draws nothing never starts). A failure is no
/// panels: they are extras, never the page.
public struct SubjectPanelsLoader: ViewModifier {
    let client: APIClient
    let caps: Capabilities
    let kind: String
    let id: String
    @Binding var panels: [PanelView]

    public func body(content: Content) -> some View {
        content.task(id: "\(kind):\(id):\(caps.panels)") {
            guard caps.panels else {
                panels = []
                return
            }
            panels = (try? await client.panels(kind: kind, id: id, language: SubjectPanels.language)) ?? []
        }
    }
}

public enum SubjectPanels {
    /// The language the home words a panel in: Arabic when the app is in Arabic.
    public static var language: String {
        AppLanguage.locale.language.languageCode?.identifier == "ar" ? "ar" : "en"
    }
}

extension View {
    /// Loads a series' or library's plugin panels into `panels` (see [SubjectPanelsLoader]).
    public func subjectPanels(client: APIClient, caps: Capabilities, kind: String, id: String,
                              into panels: Binding<[PanelView]>) -> some View {
        modifier(SubjectPanelsLoader(client: client, caps: caps, kind: kind, id: id, panels: panels))
    }
}
