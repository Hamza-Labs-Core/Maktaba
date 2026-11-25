import Foundation
import Observation

/// The ⌘F presses of a window (View ▸ Search). Each is taken once, by the search field in view
/// (`playerFindFocus()`) or by the one of the Search section the press opens. A press with
/// neither is not kept, so a field that comes later, or a Search screen built again (the Mac
/// builds a section's screen when it is chosen, story 13.16), never takes the focus for it.
@MainActor @Observable
public final class FindRequests {
    /// Every ⌘F so far.
    public private(set) var asked = 0
    /// A press not yet taken.
    @ObservationIgnored private var waiting = false
    /// The search fields in view.
    @ObservationIgnored private var fields = 0

    public init() {}

    /// ⌘F. `opening`: it opens the Search section, whose field is on its way.
    public func ask(opening: Bool) {
        waiting = opening || fields > 0
        asked += 1
    }

    /// True once for a ⌘F not yet taken.
    public func take() -> Bool {
        defer { waiting = false }
        return waiting
    }

    /// Search was left: a press its field never took goes with it.
    public func drop() { waiting = false }

    /// A search field came into view.
    public func fieldAppeared() { fields += 1 }

    /// A search field went out of view.
    public func fieldDisappeared() { fields = max(0, fields - 1) }
}
