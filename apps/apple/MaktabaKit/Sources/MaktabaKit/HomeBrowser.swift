import Foundation
import dnssd
import Network
import Observation

/// The homes that announce themselves on this network: a home publishes `_maktaba._tcp`
/// (its API port, with its web port, name and role in the TXT record). The address an app
/// uses is the web port on the address the announcement resolves to, as a person would
/// type it (its `.local` name). An app that browses lists `_maktaba._tcp` under `NSBonjourServices`.
@MainActor @Observable
public final class HomeBrowser {
    public struct Found: Identifiable, Hashable, Sendable {
        /// The home's id (TXT `id`), else its announced name.
        public let id: String
        public let name: String
        public let url: URL
    }

    public private(set) var found: [Found] = []
    private var browser: NWBrowser?
    /// The announcements being resolved to an address, by their id.
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
            // a machine of a home is not one to sign in to
            if let role = txt["role"], role != "home" { continue }
            let id = txt["id"] ?? instance
            seen.insert(id)
            guard !found.contains(where: { $0.id == id }), resolving[id] == nil else { continue }
            resolve(r.endpoint, id: id, name: txt["name"] ?? instance, txt: txt)
        }
        found.removeAll { !seen.contains($0.id) }
    }

    /// The announcement's host (DNS-SD resolve: no connection to the home is needed).
    private func resolve(_ endpoint: NWEndpoint, id: String, name: String, txt: [String: String]) {
        guard case let .service(instance, type, domain, _) = endpoint else { return }
        let done: @MainActor (String?, Int) -> Void = { [weak self] host, port in
            guard let self else { return }
            self.resolving[id] = nil
            guard let host, let url = Self.homeURL(host: host, apiPort: port, txt: txt),
                  !self.found.contains(where: { $0.id == id }) else { return }
            self.found.append(Found(id: id, name: name, url: url))
            self.found.sort { $0.name < $1.name }
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
        resolving[id] = box
        DNSServiceSetDispatchQueue(ref, .main)
        // a home that does not answer within 10 s is not listed
        DispatchQueue.main.asyncAfter(deadline: .now() + 10) { box.finish(nil, 0) }
    }

    /// The address to use for a home announced at `host`: its web port (TXT `web`), else
    /// the announced port; https when it says `tls=1`.
    nonisolated static func homeURL(host: String, apiPort: Int, txt: [String: String]) -> URL? {
        var bare = host.split(separator: "%").first.map(String.init) ?? host
        if bare.hasSuffix(".") { bare.removeLast() }
        guard !bare.isEmpty else { return nil }
        var c = URLComponents()
        c.scheme = txt["tls"] == "1" ? "https" : "http"
        c.host = bare
        c.port = txt["web"].flatMap(Int.init) ?? apiPort
        return c.url
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
