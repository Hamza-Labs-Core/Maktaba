import Foundation

/// The Mac Player's sidebar as the person arranged it (story 13.16): the sections' order and
/// the ones hidden, kept as text in the defaults (`stored`, `init(stored:)`), as the iPad keeps
/// its tabs' customization. The View menu's ⌘1, ⌘2 … follow the same order.
public struct SidebarArrangement: Equatable, Sendable {
    /// The sections' ids in the person's order (sections never moved are not in it).
    public private(set) var order: [String]
    /// The ids of the sections hidden from the sidebar.
    public private(set) var hidden: Set<String>

    public init(order: [String] = [], hidden: Set<String> = []) {
        self.order = order
        self.hidden = hidden
    }

    /// From its stored text: `order|hidden`, each a comma-separated list of ids.
    public init(stored: String) {
        let parts = stored.split(separator: "|", omittingEmptySubsequences: false).map(String.init)
        func ids(_ s: String) -> [String] { s.split(separator: ",").map(String.init) }
        order = parts.first.map(ids) ?? []
        hidden = Set(parts.count > 1 ? ids(parts[1]) : [])
    }

    /// The text kept in the defaults.
    public var stored: String { order.joined(separator: ",") + "|" + hidden.sorted().joined(separator: ",") }

    /// `sections` (in their natural order) as the sidebar lists them: the person's order first,
    /// a section they never placed where it falls among its natural neighbours, the hidden ones
    /// left out, except `keep` (the one in view: hiding it takes effect on the next choice).
    /// Never empty while `sections` is not.
    public func arrange(_ sections: [String], keep: String? = nil) -> [String] {
        var out = order.filter(sections.contains)
        for (i, s) in sections.enumerated() where !out.contains(s) {
            // after the last section that comes before it naturally and is already placed
            let before = sections[..<i].last(where: out.contains)
            let at = before.flatMap { out.firstIndex(of: $0) }.map { $0 + 1 } ?? 0
            out.insert(s, at: at)
        }
        let shown = out.filter { !hidden.contains($0) || $0 == keep }
        return shown.isEmpty ? out : shown
    }

    /// Moves the rows at `offsets` of `shown` (the list as arranged) to `destination`, as a
    /// list's `onMove` gives them; the whole order is kept from then on (a hidden section
    /// comes back after the others).
    public mutating func move(_ shown: [String], fromOffsets offsets: IndexSet, toOffset destination: Int) {
        var moved = shown
        let picked = offsets.filter { $0 < shown.count }.map { shown[$0] }
        let before = offsets.filter { $0 < destination }.count
        for i in offsets.sorted(by: >) where i < moved.count { moved.remove(at: i) }
        moved.insert(contentsOf: picked, at: max(0, min(moved.count, destination - before)))
        order = moved + order.filter { !moved.contains($0) }
    }

    /// Hides a section from the sidebar.
    public mutating func hide(_ id: String) { hidden.insert(id) }

    /// Shows every section again.
    public mutating func showAll() { hidden.removeAll() }
}

/// What a section kept while the person was in another one (story 13.16): the Mac's split view
/// builds a section's screen when it is chosen, and the screen takes its state (its model, its
/// stack) from here, so switching away and back finds it as it was. A change of `epoch` (the
/// account's parental limit, `ParentalStore.epoch`) drops what was kept, except the sections in
/// `lasting`, which read the home again themselves: nothing hidden stays.
@MainActor public final class SectionKeeper<Section: Hashable> {
    private var kept: [Section: AnyObject] = [:]
    private var epoch: Int?
    private let lasting: Set<Section>

    public init(lasting: Set<Section> = []) {
        self.lasting = lasting
    }

    /// `section`'s kept state, made by `make` the first time (or after an epoch change).
    public func state<State: AnyObject>(_ section: Section, epoch: Int, make: () -> State) -> State {
        if self.epoch != epoch {
            if self.epoch != nil { kept = kept.filter { lasting.contains($0.key) } }
            self.epoch = epoch
        }
        if let state = kept[section] as? State { return state }
        let state = make()
        kept[section] = state
        return state
    }
}
