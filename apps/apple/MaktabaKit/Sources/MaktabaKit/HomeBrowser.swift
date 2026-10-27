import Foundation
import dnssd
import Network
import Observation

/// The homes that announce themselves on this network: a home publishes `_maktaba._tcp`
/// (its API port; in the TXT record `protocol`, `version`, `schema_rev`, `tls`, its `role`,
/// its web port `web`, and from Epic 42 the home's instance id `home`, the machine's own
/// `id`, `name` and web base `path`). Every machine of a cluster announces; only `role=home`
/// (or no role: an older home) is listed. The address an app uses is the web port on the
/// host the announcement resolves to (its `.local` name), the address a person would type.
/// One home announced twice (two interfaces, IPv4 and IPv6, a restart) is listed once: by
/// `home`, else `id`, else its address.
/// An app that browses lists `_maktaba._tcp` under `NSBonjourServices`.
@MainActor @Observable
public final class HomeBrowser {
    public struct Found: Identifiable, Hashable, Sendable {
        /// What makes it one home: its `id`, else its address (host:port and path).
        public let id: String
        public let name: String
        public let url: URL
        /// The home's instance id (TXT `id`), when it announces one.
        public let homeID: String?

        public init(name: String, url: URL, homeID: String?) {
            self.name = name
            self.url = url
            self.homeID = homeID
            self.id = homeID ?? Self.addressKey(url)
        }

        static func addressKey(_ url: URL) -> String {
            "\(url.host()?.lowercased() ?? ""):\(url.port ?? 0)\(url.path())"
        }
    }

    public private(set) var found: [Found] = []
    private var store = FoundHomes()
    private var browser: NWBrowser?
    /// The announcements being resolved to an address, by their instance name.
    private var resolving: [String: ResolveBox] = [:]

    public init() {}

    public func start() {
        guard browser == nil else { return }
        let b = NWBrowser(for: .bonjourWithTXTRecord(type: "_maktaba._tcp", domain: nil), using: .tcp)
        b.browseResultsChangedHandler = { [weak self] results, _ in
            MainActor.assumeIsolated { self?.update(results) }
        }
        b.start(queue: .main)
        browser = b
    }

    public func stop() {
        browser?.cancel()
        browser = nil
        resolving.values.forEach { $0.finish(nil, 0) }
        resolving = [:]
    }

    private func update(_ results: Set<NWBrowser.Result>) {
        var seen = Set<String>()
        for r in results {
            guard case let .service(instance, _, _, _) = r.endpoint else { continue }
            var txt: [String: String] = [:]
            if case let .bonjour(record) = r.metadata { txt = record.dictionary }
            guard Self.isHome(txt) else { continue }
            seen.insert(instance)
            guard !store.has(instance), resolving[instance] == nil else { continue }
            resolve(r.endpoint, instance: instance, txt: txt)
        }
        store.keep(seen)
        found = store.list
    }

    /// The announcement's host (DNS-SD resolve: no connection to the home is needed).
    private func resolve(_ endpoint: NWEndpoint, instance: String, txt: [String: String]) {
        guard case let .service(_, type, domain, _) = endpoint else { return }
        let done: @MainActor (String?, Int) -> Void = { [weak self] host, port in
            guard let self else { return }
            self.resolving[instance] = nil
            guard let host, let found = Self.found(instance: instance, host: host, apiPort: port, txt: txt) else { return }
            self.store.add(instance, found)
            self.found = self.store.list
        }
        let box = ResolveBox(done)
        var ref: DNSServiceRef?
        let err = DNSServiceResolve(&ref, 0, 0, instance, type, domain.isEmpty ? "local." : domain, { _, _, _, code, _, target, port, _, _, ctx in
            guard let ctx else { return }
            let box = Unmanaged<ResolveBox>.fromOpaque(ctx).takeUnretainedValue()
            let host = code == kDNSServiceErr_NoError ? target.map { String(cString: $0) } : nil
            box.finish(host, Int(UInt16(bigEndian: port)))
        }, Unmanaged.passUnretained(box).toOpaque())
        guard err == kDNSServiceErr_NoError, let ref else { return }
        box.ref = ref
        resolving[instance] = box
        DNSServiceSetDispatchQueue(ref, .main)
        // a home that does not answer within 10 s is not listed
        DispatchQueue.main.asyncAfter(deadline: .now() + 10) { box.finish(nil, 0) }
    }

    /// A machine of a home (`role=node`) is not one to sign in to.
    nonisolated static func isHome(_ txt: [String: String]) -> Bool {
        txt["role"].map { $0 == "home" } ?? true
    }

    /// The home an announcement stands for (nil: a machine of a home, or no address). Its
    /// id is TXT `home` (the home's instance id, on every machine of it), else `id` (an
    /// older home announces only its own), lower-cased as the identity route says it.
    nonisolated static func found(instance: String, host: String, apiPort: Int, txt: [String: String]) -> Found? {
        guard isHome(txt), let url = homeURL(host: host, apiPort: apiPort, txt: txt) else { return nil }
        let name = txt["name"]?.trimmingCharacters(in: .whitespacesAndNewlines).nilIfEmpty ?? instance
        let id = (txt["home"] ?? txt["id"])?.trimmingCharacters(in: .whitespaces).lowercased().nilIfEmpty
        return Found(name: name, url: url, homeID: id)
    }

    /// The address to use for a home announced at `host`: its web port (TXT `web`), else
    /// the announced port; https when it says `tls=1`; its web base `path` when not `/`.
    nonisolated static func homeURL(host: String, apiPort: Int, txt: [String: String]) -> URL? {
        var bare = host.split(separator: "%").first.map(String.init) ?? host
        if bare.hasSuffix(".") { bare.removeLast() }
        // a home's mDNS library adds ".local." to a host name that already has it (the Mac's
        // own is "name.local"): "name.local.local" resolves to nothing
        while bare.lowercased().hasSuffix(".local.local") { bare.removeLast(".local".count) }
        guard !bare.isEmpty else { return nil }
        var c = URLComponents()
        c.scheme = ["1", "true", "yes"].contains(txt["tls"]?.lowercased() ?? "") ? "https" : "http"
        c.host = bare
        c.port = txt["web"].flatMap(Int.init) ?? apiPort
        c.path = basePath(txt["path"]) ?? ""
        return c.url
    }

    /// TXT `path` as a URL path: a leading slash, no trailing one; nil for none or `/`.
    nonisolated static func basePath(_ raw: String?) -> String? {
        guard var p = raw?.trimmingCharacters(in: .whitespaces), !p.isEmpty else { return nil }
        if !p.hasPrefix("/") { p = "/" + p }
        while p.hasSuffix("/") { p.removeLast() }
        guard !p.isEmpty, !p.contains("?"), !p.contains("#") else { return nil }
        return p
    }
}

/// The announcements seen, by instance name, and the homes they make: one per home id
/// (else per address), the first instance by name standing for it.
struct FoundHomes {
    private var byInstance: [String: HomeBrowser.Found] = [:]

    func has(_ instance: String) -> Bool { byInstance[instance] != nil }

    mutating func add(_ instance: String, _ found: HomeBrowser.Found) {
        byInstance[instance] = found
    }

    /// Only the instances still announced stay.
    mutating func keep(_ instances: Set<String>) {
        byInstance = byInstance.filter { instances.contains($0.key) }
    }

    var list: [HomeBrowser.Found] {
        var seen = Set<String>()
        var out: [HomeBrowser.Found] = []
        for (_, f) in byInstance.sorted(by: { $0.key < $1.key }) where seen.insert(f.id).inserted {
            out.append(f)
        }
        return out.sorted { $0.name.localizedStandardCompare($1.name) == .orderedAscending }
    }
}

/// One resolve in flight: its DNS-SD reference and what to do with the answer (once).
final class ResolveBox: @unchecked Sendable {
    var ref: DNSServiceRef?
    private var done: (@MainActor (String?, Int) -> Void)?

    init(_ done: @escaping @MainActor (String?, Int) -> Void) {
        self.done = done
    }

    /// Called on the main queue (the resolve's queue, and the timeout's).
    func finish(_ host: String?, _ port: Int) {
        MainActor.assumeIsolated {
            guard let d = done else { return }
            done = nil
            if let ref { DNSServiceRefDeallocate(ref) }
            ref = nil
            d(host, port)
        }
    }
}
