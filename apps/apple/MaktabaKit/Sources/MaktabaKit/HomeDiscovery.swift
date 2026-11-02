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

/// No browse at all: the homes on the network are none. The UI tests run with a scripted
/// local network (`-maktaba-permissions-fake`), and type the mock home's address; a real
/// browse would put the system's "find devices on local networks?" question over the app
/// (on the Mac it stays on top for the whole run, where a test's clicks land on it).
@MainActor public final class NoDiscovery: HomeDiscovering {
    public init() {}
    public func start(_ found: @escaping @MainActor ([HomeBrowser.Found]) -> Void) { found([]) }
    public func stop() {}
}

/// A debug build run by tests: launched by the UI tests with a scripted local network
/// (`-maktaba-permissions-fake`), or the host of in-process tests (XCTest sets
/// `XCTestConfigurationFilePath`; the Mac Player's menu tests run inside the app). Neither
/// may browse the real network: the system's "find devices on local networks?" question
/// would come up and stay over the screen for the rest of the run (CI: the Mac's in-process
/// tests ran first, and the UI tests' clicks on Playback > Stop landed on that question).
enum TestLaunch {
    static func scriptsTheNetwork(arguments: [String] = CommandLine.arguments,
                                  environment: [String: String] = ProcessInfo.processInfo.environment) -> Bool {
        #if DEBUG
        return arguments.contains("-maktaba-permissions-fake") || environment["XCTestConfigurationFilePath"] != nil
        #else
        return false
        #endif
    }
}

extension BonjourDiscovery {
    /// The discovery an app uses: Bonjour, except in a debug build run by tests (`TestLaunch`).
    static func forThisApp(arguments: [String] = CommandLine.arguments,
                           environment: [String: String] = ProcessInfo.processInfo.environment) -> any HomeDiscovering {
        TestLaunch.scriptsTheNetwork(arguments: arguments, environment: environment) ? NoDiscovery() : BonjourDiscovery()
    }
}
