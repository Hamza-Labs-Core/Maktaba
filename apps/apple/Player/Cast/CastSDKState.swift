#if DEBUG
import Foundation

/// What the real Google Cast SDK says of itself, in a Debug build launched with
/// `-maktaba-cast real`: the app writes it as `cast.sdk`'s accessibility value and the UI tests
/// read it back (this file is in both). Every field but `receivers` is the SDK's own answer or
/// its own log, not worked out from what the app asked of it.
struct CastSDKState: Equatable, Sendable {
    /// The receiver ids of the criteria the app gave the SDK, once the SDK says it is set up
    /// (none: it is not). The app's word, not the SDK's: its headers give no criteria back.
    /// That the SDK searches for this receiver is shown by the TV it finds.
    var receivers: [String] = []
    /// Its discovery manager says it looks for Cast devices. Not enough on its own: the SDK
    /// says so too while iOS refuses every Bonjour search it makes (`errors`).
    var running = false
    /// Its Bonjour browser sent a query: the search really began. `running` comes first, and how
    /// long before is the SDK's own doing: it searches once it has read the network's state,
    /// which it asks for on the background-priority queue (4.8.6,
    /// `-[GCKNNetworkReachability startMonitoring]`). That queue gets no time on a machine with no
    /// CPU to spare: on CI's runners the answer took from 2 s to over a minute.
    var searching = false
    /// The codes its Bonjour browser failed to search with, in order of number.
    var errors: [Int] = []
    /// The ids of the devices it found.
    var devices: [String] = []

    /// `NSNetServicesMissingRequiredConfigurationError`: the service is not in Info.plist's
    /// `NSBonjourServices`.
    static let undeclared = -72008
    /// `kDNSServiceErr_PolicyDenied`: local network access is off for the app.
    static let denied = -65570

    /// Why this is no discovery of TVs for `receiver`, empty when it is one.
    func problems(receiver: String) -> [String] {
        var out: [String] = []
        if receivers.isEmpty { out.append("the SDK is not set up") }
        else if receivers != [receiver] { out.append("the SDK is set up for \(receivers.joined(separator: ", ")), not \(receiver)") }
        if !running { out.append("discovery is not running") }
        else if !searching { out.append("the Bonjour search has not begun: the SDK has not read the network's state yet") }
        for e in errors {
            switch e {
            case Self.undeclared: out.append("a Bonjour service is missing from NSBonjourServices (\(e))")
            case Self.denied: out.append("local network access is denied (\(e))")
            default: out.append("a Bonjour search failed (\(e))")
            }
        }
        return out
    }

    /// Whether a line the SDK logs says that its Bonjour browser sent a query.
    static func beganSearch(function: String) -> Bool { function.contains("didSendDiscoveryQuery") }

    /// The code in a line the SDK logs when its Bonjour browser could not search, nil for any
    /// other line.
    static func searchError(function: String, message: String) -> Int? {
        guard function.contains("didNotSearch") else { return nil }
        guard let r = message.range(of: #"NSNetServicesErrorCode = "?-?\d+"#, options: .regularExpression),
              let n = message[r].range(of: #"-?\d+$"#, options: .regularExpression) else { return 0 }
        return Int(message[r][n]) ?? 0
    }

    var text: String {
        func list(_ items: [String]) -> String { items.joined(separator: ",") }
        return "receivers=\(list(receivers));running=\(running);searching=\(searching);errors=\(list(errors.map(String.init)));devices=\(list(devices))"
    }

    init(receivers: [String] = [], running: Bool = false, searching: Bool = false, errors: [Int] = [], devices: [String] = []) {
        self.receivers = receivers
        self.running = running
        self.searching = searching
        self.errors = errors
        self.devices = devices
    }

    /// `text` read back, nil when it is not one.
    init?(text: String) {
        var fields: [String: String] = [:]
        for pair in text.split(separator: ";") {
            let kv = pair.split(separator: "=", maxSplits: 1, omittingEmptySubsequences: false).map(String.init)
            if kv.count == 2 { fields[kv[0]] = kv[1] }
        }
        guard let receivers = fields["receivers"], let running = fields["running"].flatMap(Bool.init),
              let searching = fields["searching"].flatMap(Bool.init), let errors = fields["errors"], let devices = fields["devices"] else { return nil }
        func list(_ s: String) -> [String] { s.split(separator: ",").map(String.init) }
        self.init(receivers: list(receivers), running: running, searching: searching, errors: list(errors).compactMap(Int.init), devices: list(devices))
    }
}
#endif
