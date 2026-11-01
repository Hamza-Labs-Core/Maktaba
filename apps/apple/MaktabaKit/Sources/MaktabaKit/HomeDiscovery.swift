import Foundation
import Network

/// Where the homes on this network come from: Bonjour in the apps, a fake in tests.
@MainActor public protocol HomeDiscovering: AnyObject {
    /// Browses afresh (what was found before is forgotten), calling `found` with each new list.
    func start(_ found: @escaping @MainActor ([HomeBrowser.Found]) -> Void)
    func stop()
}

/// `HomeBrowser`, browsed afresh whenever the network changes (Wi-Fi joined or left,
/// another network): what was announced on the last one may be gone, and this one's homes
/// announce again.
@MainActor public final class BonjourDiscovery: HomeDiscovering {
    private var browser: HomeBrowser?
    private var monitor: NWPathMonitor?
    private var found: (@MainActor ([HomeBrowser.Found]) -> Void)?
    /// The network the browse is on (nil: not heard yet).
    private var network: String?

    public init() {}

    public func start(_ found: @escaping @MainActor ([HomeBrowser.Found]) -> Void) {
        stop()
        self.found = found
        browse()
        let m = NWPathMonitor()
        m.pathUpdateHandler = { [weak self] path in
            let network = "\(path.status) " + path.availableInterfaces.map(\.name).joined(separator: ",")
            MainActor.assumeIsolated { self?.changed(to: network) }
        }
        m.start(queue: .main)
        monitor = m
    }

    public func stop() {
        monitor?.cancel()
        monitor = nil
        network = nil
        browser?.stop()
        browser = nil
        found = nil
    }

    private func changed(to network: String) {
        defer { self.network = network }
        // the first answer is the network the browse started on
        guard let before = self.network, before != network else { return }
        browse()
    }

    private func browse() {
        browser?.stop()
        let b = HomeBrowser()
        b.onChange = { [weak self] in self?.found?($0) }
        browser = b
        found?([])
        b.start()
    }
}
