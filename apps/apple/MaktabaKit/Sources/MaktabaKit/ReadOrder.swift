import Foundation

/// The order of a page's reads and changes. A read that began before a change was answered
/// before it (its answer still has the removed row, the old pin), and an answer that arrives
/// after a newer one is older than what is on screen: neither may be applied, or the old state
/// comes back on screen while the home has the new one. As the web's `lib/readOrder` and
/// Android's `ReadOrder`; one per list a page reads.
@MainActor
public final class ReadOrder {
    private var started = 0
    /// The oldest ticket that may still be applied.
    private var floor = 0
    private var pending = 0

    public init() {}

    /// A read begins: its ticket, for `fresh` once it is answered.
    public func begin() -> Int {
        started += 1
        return started
    }

    /// The read with `ticket` was answered: true when it may be applied, that is it began
    /// after every change and is no older than an answer applied. Reads that began before it
    /// are then older, and are dropped.
    public func fresh(_ ticket: Int) -> Bool {
        guard pending == 0, ticket >= floor else { return false }
        floor = ticket
        return true
    }

    /// A change is on screen or saved: every read under way is older than it.
    public func changed() {
        floor = started + 1
    }

    /// Runs `save` (a removal, a toggle): no read is applied while it is under way, and every
    /// read that began before it ends is older than it.
    public func saving<T>(_ save: () async throws -> T) async rethrows -> T {
        pending += 1
        defer {
            pending -= 1
            floor = started + 1
        }
        return try await save()
    }
}
