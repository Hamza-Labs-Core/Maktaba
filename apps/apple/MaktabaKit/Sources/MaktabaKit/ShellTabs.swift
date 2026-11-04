import Foundation

/// The tabs an app's shell shows while the home's features change (story 51.11). A tab goes
/// when its feature goes off, but a screen already open is never torn down: the tab in view
/// stays (in its place in `order`) until another one is chosen, and its screen says the
/// feature is off when its next call fails. The other tabs keep their stacks: the shell is
/// not rebuilt, only its list of tabs changes.
public enum ShellTabs {
    /// `offered`, plus `selected` when the change took it away, put back where `order` has it.
    public static func shown<Tab: Hashable>(offered: [Tab], selected: Tab, order: [Tab]) -> [Tab] {
        guard !offered.contains(selected) else { return offered }
        guard let at = order.firstIndex(of: selected) else { return offered + [selected] }
        // before the first offered tab that comes after it in `order`
        let after = offered.firstIndex { (order.firstIndex(of: $0) ?? .max) > at } ?? offered.count
        var tabs = offered
        tabs.insert(selected, at: after)
        return tabs
    }
}
