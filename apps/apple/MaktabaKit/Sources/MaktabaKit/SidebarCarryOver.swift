import Foundation

/// The Mac Player's sidebar before story 13.16 was SwiftUI's `TabView`, its arrangement a
/// `TabViewCustomization` the system encoded (JSON, as data) under `tabsKey`; a row's
/// customization id was `sectionPrefix` and the section's id. It is carried over once to
/// `key`. The value stays where it was: an older Player opened again finds it.
extension SidebarArrangement {
    /// The defaults key the arrangement is kept under (`stored`).
    public static let key = "player.mac.sidebar"
    /// The defaults key of the arrangement before story 13.16 (the iPad's tabs keep theirs there).
    public static let tabsKey = "player.tabs"
    /// What a section's id follows in its customization id.
    public static let sectionPrefix = "player.section."

    /// Keeps what an older Player stored, when nothing is kept here yet; `known`: the sections
    /// the person can arrange. True when it did.
    @discardableResult
    public static func carryOver(in defaults: UserDefaults, known: [String]) -> Bool {
        guard defaults.object(forKey: key) == nil, let tabs = defaults.data(forKey: tabsKey),
              let carried = SidebarArrangement(tabs: tabs, known: known) else { return false }
        defaults.set(carried.stored, forKey: key)
        return true
    }

    /// From the encoded `TabViewCustomization`. Its type gives an order only for a section id
    /// asked by name, and its visibilities only from macOS 15.4, so the encoding is read: two
    /// lists of a key, then its value (`{"base":{"explicit":{"_0":"player.section.live"}}}`,
    /// `{"visibility":{"hidden":{}}}` or `{"childrenIDs":[keys in order]}`). Sections not in
    /// `known` are left out, and hiding every one hides none. Nil when it arranges nothing, or
    /// is of a shape this does not know.
    init?(tabs: Data, known: [String]) {
        guard let top = try? JSONSerialization.jsonObject(with: tabs) as? [String: Any] else { return nil }
        func id(_ key: Any) -> String? {
            let base = (key as? [String: Any])?["base"] as? [String: Any]
            guard let name = (base?["explicit"] as? [String: Any])?["_0"] as? String,
                  name.hasPrefix(Self.sectionPrefix) else { return nil }
            let id = String(name.dropFirst(Self.sectionPrefix.count))
            return known.contains(id) ? id : nil
        }
        func pairs(_ field: String) -> [(key: Any, value: [String: Any])] {
            guard let list = top[field] as? [Any] else { return [] }
            return stride(from: 0, to: list.count - 1, by: 2).compactMap { at in
                (list[at + 1] as? [String: Any]).map { (list[at], $0) }
            }
        }
        var hidden: Set<String> = []
        for (key, value) in pairs("perTabState") {
            if let id = id(key), (value["visibility"] as? [String: Any])?["hidden"] != nil { hidden.insert(id) }
        }
        if hidden.isSuperset(of: known) { hidden = [] }
        var order: [String] = []
        for (_, value) in pairs("perSectionState") {
            for case let id? in (value["childrenIDs"] as? [Any] ?? []).map(id) where !order.contains(id) { order.append(id) }
        }
        guard !order.isEmpty || !hidden.isEmpty else { return nil }
        self.init(order: order, hidden: hidden)
    }
}
