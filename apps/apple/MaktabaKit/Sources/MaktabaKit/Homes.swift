import Foundation
import Observation

/// A home this app knows: its address, a name to show (its address until the home says
/// its name, or the one the person gave it), and the home's id when it said one (the same
/// home reached at another address stays one home).
///
/// The list lives in the app's defaults: addresses and names, nothing secret. Each home's
/// tokens are in the Keychain (`TokenStore`), one entry per home.
public struct Home: Codable, Hashable, Identifiable, Sendable {
    public var id: URL { url }
    /// The address in use (its tokens in the Keychain are kept under it).
    public var url: URL
    public var name: String
    /// The home's instance id (mDNS TXT `id`, `/api/discovery/home`), when it said one.
    public var homeID: String?
    /// The home's other addresses (a connect link's, `/api/discovery/home`'s `urls`).
    public var addresses: [URL]?
    /// The person named it: the home's own name no longer replaces it.
    public var renamed: Bool?
    public var lastUsed: Date?
    /// The home's id in Maktaba Cloud, when it was chosen through the cloud (story 42.7).
    public var cloudServerID: String?

    public init(url: URL, name: String, homeID: String? = nil, addresses: [URL]? = nil, cloudServerID: String? = nil) {
        self.url = url
        self.name = name
        self.homeID = homeID
        self.addresses = addresses
        self.cloudServerID = cloudServerID
    }

    /// The address to show: host, the port when it has one, and a base path.
    public var displayAddress: String {
        var s = url.host() ?? url.absoluteString
        if let p = url.port { s += ":\(p)" }
        let path = url.path()
        if !path.isEmpty, path != "/" { s += path }
        return s
    }

    /// The name stands for nothing yet (the address's host, until the home says its name).
    var hasPlaceholderName: Bool {
        renamed != true && (name.isEmpty || name == url.host() || name == url.absoluteString)
    }

    /// The home's address from what a person types: a scheme added when missing
    /// (http on a local address, else https), no path, no trailing slash.
    public static func normalize(_ text: String) -> URL? {
        guard var c = components(text) else { return nil }
        c.path = ""
        return c.url
    }

    /// A home's base address keeping its path (a home served under `/maktaba`), without a
    /// trailing slash, query or fragment.
    public static func normalizeBase(_ text: String) -> URL? {
        guard var c = components(text) else { return nil }
        var path = c.path
        while path.hasSuffix("/") { path.removeLast() }
        c.path = path
        return c.url
    }

    /// The addresses to try for what a person typed, in order: as typed (with its path: a
    /// home under a base path), then the bare host; when no scheme was typed, the same with
    /// the other scheme after (a home on the internet without TLS, a local one with it).
    public static func candidates(for text: String) -> [URL] {
        let s = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let first = normalizeBase(s), let root = normalize(s) else { return [] }
        var out = [first, root]
        if !s.contains("://"), var c = URLComponents(url: first, resolvingAgainstBaseURL: false) {
            c.scheme = c.scheme == "https" ? "http" : "https"
            if let other = c.url { out.append(other) }
            c.path = ""
            if let otherRoot = c.url { out.append(otherRoot) }
        }
        var seen = Set<URL>()
        return out.filter { seen.insert($0).inserted }
    }

    private static func components(_ text: String) -> URLComponents? {
        var s = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !s.isEmpty else { return nil }
        if !s.contains("://") {
            let host = s.split(separator: "/").first.map(String.init) ?? s
            s = (isLocal(host) ? "http://" : "https://") + s
        }
        guard var c = URLComponents(string: s), let host = c.host, !host.isEmpty,
              c.scheme == "http" || c.scheme == "https" else { return nil }
        c.query = nil
        c.fragment = nil
        c.user = nil
        c.password = nil
        return c
    }

    static func isLocal(_ hostPort: String) -> Bool {
        let host = hostPort.split(separator: ":").first.map(String.init) ?? hostPort
        return host == "localhost" || host.hasSuffix(".local") || host.hasPrefix("192.168.")
            || host.hasPrefix("10.") || host.hasPrefix("127.") || host.range(of: #"^172\.(1[6-9]|2\d|3[01])\."#, options: .regularExpression) != nil
    }
}

/// The app's state the screens bind to: the homes it knows, the one in use, who is signed
/// in there. One per app (`AppSession(app: "com.hamzalabs.maktaba.player")`).
@MainActor @Observable
public final class AppSession {
    public enum Phase: Equatable {
        case choosingHome
        case signingIn
        case signedIn(User)
    }

    public private(set) var homes: [Home]
    public private(set) var current: Home?
    public private(set) var phase: Phase = .choosingHome
    public private(set) var client: APIClient?
    /// How the current home lets one sign in (nil: it has not said, or is from before).
    public private(set) var signInMethods: SignInMethods?
    /// Addresses are being tried (a typed address, a scanned code, an opened link).
    public private(set) var connecting = false
    /// Why the last address, code or link did not lead to a home (cleared by the screens).
    public var connectProblem: ReachFailure?
    /// The "Approve a sign-in" sheet is up (the account menus open it).
    public var approvingSignIn = false

    /// Maktaba Cloud (story 42.7): its token in memory only, gone when the app quits.
    public private(set) var cloud: CloudClient
    /// The cloud account signed in here (nil: not signed in to the cloud).
    public private(set) var cloudUser: CloudUser?
    /// The homes that account reaches, as the cloud last said.
    public private(set) var cloudHomes: [CloudHome] = []
    /// The "Through Maktaba Cloud" sheet is up (the home picker opens it; it stays up while
    /// the chosen home is reached and signed in to).
    public var choosingThroughCloud = false

    public var signedIn: Bool {
        if case .signedIn = phase { true } else { false }
    }

    /// The homes, the last used first.
    public var recentHomes: [Home] {
        homes.enumerated().sorted { a, b in
            let (x, y) = (a.element.lastUsed ?? .distantPast, b.element.lastUsed ?? .distantPast)
            return x != y ? x > y : a.offset < b.offset
        }.map(\.element)
    }

    private let app: String
    private let defaults: UserDefaults
    private let probe: HomeProbe
    /// Ask a home picked from a list how it signs in (off in tests without a network).
    private let asksHomes: Bool
    private var connectTask: Task<Void, Never>?
    /// The cloud's URL session (a stub in tests; else one that keeps nothing).
    private let cloudSession: URLSession?
    /// The homes' URL session (a stub in tests).
    private let homeSession: URLSession

    public init(app: String, defaults: UserDefaults = .standard, probe: HomeProbe = HomeProbe(), asksHomes: Bool = true,
                homeSession: URLSession = .shared, cloudSession: URLSession? = nil) {
        self.app = app
        self.defaults = defaults
        self.probe = probe
        self.asksHomes = asksHomes
        self.homeSession = homeSession
        self.cloudSession = cloudSession
        cloud = CloudClient(base: CloudAddress.current(defaults), session: cloudSession)
        #if DEBUG
        // UI tests: start with no homes
        if CommandLine.arguments.contains("-maktaba-session-reset") {
            defaults.removeObject(forKey: "homes")
            defaults.removeObject(forKey: "home")
        }
        #endif
        homes = (defaults.data(forKey: "homes")).flatMap { try? JSONDecoder().decode([Home].self, from: $0) } ?? []
        if let last = defaults.url(forKey: "home"), let h = homes.first(where: { $0.url == last }) {
            use(h)
        }
    }

    /// Picks a home: its client, and signed in at once when the Keychain still has tokens.
    public func use(_ home: Home) {
        use(home, ask: asksHomes)
    }

    /// `quietCloud`: with no tokens here (or none that still work), sign in through Maktaba
    /// Cloud without asking when the cloud vouches for this address (`cloudServer(for:)`).
    private func use(_ home: Home, ask: Bool, quietCloud: Bool = true) {
        let home = remember(home)
        current = home
        defaults.set(home.url, forKey: "home")
        let tokens = TokenStore(app: app, home: home.url)
        client = APIClient(home: home.url, tokens: tokens, session: homeSession)
        phase = .signingIn
        signInMethods = nil
        if tokens.access != nil, quietCloud {
            Task { await restore(thenCloud: true) }
        } else if quietCloud {
            Task { await signInQuietlyThroughCloud() }
        }
        if ask {
            // how it signs in, and its id when the list did not say
            Task { [probe] in
                guard case let .success(r) = await probe.check(home.url), current?.url == r.url else { return }
                signInMethods = r.signIn
                if current?.homeID == nil, let id = r.id { update(home.url) { $0.homeID = id } }
            }
        }
    }

    /// Picks a known home: at its address, or the first of its others that answers.
    public func open(_ home: Home) {
        guard let others = home.addresses, !others.isEmpty else { return use(home) }
        connectTask?.cancel()
        connectTask = Task { await connect(to: [home.url] + others, name: home.name, homeID: home.homeID) }
    }

    /// Tries `candidates` in order and uses the first that is a Maktaba home (the one
    /// `homeID` names, when given). nil: connected; else why not (also in `connectProblem`).
    @discardableResult
    public func connect(to candidates: [URL], name: String? = nil, homeID: String? = nil) async -> ReachFailure? {
        connecting = true
        connectProblem = nil
        defer { connecting = false }
        switch await probe.reach(candidates, expectedID: homeID) {
        case let .success(r):
            var others: [URL] = []
            for u in r.addresses + candidates where u != r.url && !others.contains(u) { others.append(u) }
            let fallback = r.url.host() ?? r.url.absoluteString
            use(Home(url: r.url, name: r.name ?? name ?? fallback, homeID: r.id ?? homeID,
                     addresses: others.isEmpty ? nil : others), ask: false)
            signInMethods = r.signIn
            return nil
        case let .failure(f):
            if !Task.isCancelled { connectProblem = f }
            return f
        }
    }

    /// A connect link opened from outside the app, waiting for the person to say yes
    /// (`confirmLink`) or no (`dismissLink`). Nothing is fetched or switched before that.
    public private(set) var pendingLink: ConnectLink?

    /// A home's connect link (`maktaba://connect?…`) opened from outside the app (another
    /// app, a web page, the camera): kept for the person to confirm first, since anyone can
    /// make one. false: not a connect link.
    @discardableResult
    public func open(_ url: URL) -> Bool {
        guard let link = ConnectLink(url) else { return false }
        pendingLink = link
        return true
    }

    /// The person said yes to the link opened from outside.
    public func confirmLink() {
        guard let link = pendingLink else { return }
        pendingLink = nil
        open(link)
    }

    public func dismissLink() {
        pendingLink = nil
    }

    /// A connect link the person gave in the app (scanned or pasted in the home picker):
    /// adds or picks the home. Only the public `/api/discovery/home` is asked at each address;
    /// no token goes anywhere, and a new address is signed in to afresh.
    public func open(_ link: ConnectLink) {
        // already there
        if let id = link.homeID, current?.homeID == id, phase != .choosingHome { return }
        connectTask?.cancel()
        connectTask = Task { await connect(to: link.urls, name: link.name, homeID: link.homeID) }
    }

    /// Gives a home the person's own name (empty: back to the home's).
    public func rename(_ home: Home, to name: String) {
        let name = name.trimmingCharacters(in: .whitespacesAndNewlines)
        update(home.url) { h in
            if name.isEmpty {
                h.name = h.url.host() ?? h.url.absoluteString
                h.renamed = nil
            } else {
                h.name = name
                h.renamed = true
            }
        }
    }

    public func forget(_ home: Home) {
        TokenStore(app: app, home: home.url).clear()
        homes.removeAll { $0.url == home.url }
        save()
        if current?.url == home.url { switchHome() }
    }

    public func switchHome() {
        connectTask?.cancel()
        current = nil
        client = nil
        signInMethods = nil
        phase = .choosingHome
        defaults.removeObject(forKey: "home")
    }

    public func signIn(username: String, password: String) async throws {
        guard let client else { return }
        _ = try await client.signIn(username: username, password: password)
        signedIn(try await client.whoami())
    }

    /// This device was signed in some other way (its code, approved on another device):
    /// who is it now.
    public func signedInByCode() async throws {
        guard let client else { return }
        signedIn(try await client.whoami())
    }

    public func signOut() {
        if let current { TokenStore(app: app, home: current.url).clear() }
        approvingSignIn = false
        phase = .signingIn
    }

    /// A screen saw `APIError.signedOut`: back to signing in (through the cloud at once when
    /// it vouches for this address and the person is signed in to it).
    public func signedOut() {
        approvingSignIn = false
        phase = .signingIn
        Task { await signInQuietlyThroughCloud() }
    }

    /// The tokens kept for this address still work: signed in. Else (`thenCloud`) through
    /// the cloud, when it vouches for the address.
    private func restore(thenCloud: Bool = false) async {
        guard let client else { return }
        if let me = try? await client.whoami() {
            if self.client?.home == client.home { signedIn(me) }
        } else if thenCloud, self.client?.home == client.home {
            await signInQuietlyThroughCloud()
        }
    }

    // MARK: Maktaba Cloud (story 42.7)

    /// The cloud's API address in use.
    public var cloudAddress: URL { cloud.base }

    /// Sets the cloud's API address (empty: the standard one) and signs out of the cloud
    /// (its token belongs to the address that issued it). false: not an address.
    @discardableResult
    public func setCloudAddress(_ text: String) -> Bool {
        guard CloudAddress.set(text, in: defaults) else { return false }
        let base = CloudAddress.current(defaults)
        guard base != cloud.base else { return true }
        cloudSignOut()
        cloud = CloudClient(base: base, session: cloudSession)
        return true
    }

    /// Signs in to Maktaba Cloud and lists the homes the account reaches.
    public func cloudSignIn(email: String, password: String) async throws {
        let client = cloud
        let user = try await client.signIn(email: email.trimmingCharacters(in: .whitespacesAndNewlines), password: password)
        guard client === cloud else { return }
        cloudUser = user
        cloudHomes = []
        try await loadCloudHomes()
    }

    /// Asks the cloud for its homes again. Not signed in any more: signed out here too.
    public func loadCloudHomes() async throws {
        do {
            cloudHomes = try await cloud.homes()
        } catch CloudError.signedOut {
            cloudSignOut()
            throw CloudError.signedOut
        }
    }

    /// Forgets the cloud's token (every home's own sign-in stays).
    public func cloudSignOut() {
        let client = cloud
        Task { await client.signOut() }
        cloudUser = nil
        cloudHomes = []
    }

    /// Picks a home from the cloud's list and signs in to it with a new assertion: at the
    /// first of the addresses the cloud gives that answers (its direct address, then the
    /// relay). Tokens already kept for that address are used when they still work. Only a
    /// home that linked this account is offered (`canSignIn`); owning one is no account in it.
    public func signIn(through home: CloudHome) async throws {
        guard home.canSignIn else { throw CloudError.notLinked }
        connectTask?.cancel()
        connecting = true
        connectProblem = nil
        defer { connecting = false }
        let r: ReachedHome
        switch await probe.reach(home.addresses) {
        case let .success(x): r = x
        case let .failure(f): throw f
        }
        var others: [URL] = []
        for u in r.addresses + home.addresses where u != r.url && !others.contains(u) { others.append(u) }
        use(Home(url: r.url, name: r.name ?? home.name, homeID: r.id, addresses: others.isEmpty ? nil : others,
                 cloudServerID: home.serverID), ask: false, quietCloud: false)
        signInMethods = r.signIn
        guard let client else { return }
        if TokenStore(app: app, home: r.url).access != nil, let me = try? await client.whoami() {
            signedIn(me)
            return
        }
        try await signInWithAssertion(serverID: home.serverID, client: client)
    }

    /// The cloud's id for the home at `url`, when the cloud itself gives that address for a
    /// home linked to this account. Only such an address is handed an assertion: an address the
    /// home announced (mDNS, `/api/discovery/home`) could be anything claiming to be it.
    func cloudServer(for url: URL) -> String? {
        cloudHomes.first { $0.canSignIn && $0.addresses.contains(url) }?.serverID
    }

    /// Signed in to the cloud, at an address it vouches for, and not signed in here: a new
    /// assertion for this address, without asking. Any failure leaves the sign-in screen.
    private func signInQuietlyThroughCloud() async {
        guard cloudUser != nil, phase == .signingIn, let client, let home = current, home.url == client.home else { return }
        if cloudServer(for: home.url) == nil { try? await loadCloudHomes() }
        guard let serverID = cloudServer(for: home.url), phase == .signingIn, self.client?.home == client.home else { return }
        try? await signInWithAssertion(serverID: serverID, client: client)
    }

    /// A new assertion for `serverID`, traded at `client`'s address only for that home's
    /// tokens (kept under that address, as a password sign-in's).
    private func signInWithAssertion(serverID: String, client: APIClient) async throws {
        let a: CloudAssertion
        do {
            a = try await cloud.signInAssertion(serverID: serverID)
        } catch CloudError.signedOut {
            cloudSignOut()
            throw CloudError.signedOut
        }
        // the person moved on to another home meanwhile
        guard self.client?.home == client.home else { return }
        _ = try await client.signIn(assertion: a.assertion)
        let me = try await client.whoami()
        if self.client?.home == client.home { signedIn(me) }
    }

    private func signedIn(_ me: Me) {
        named(me.homeName)
        phase = .signedIn(me.user)
    }

    /// The current home's name, as the home says it (nil: it did not say, so the name
    /// stays; a name the person gave stays too), kept with the homes this app knows.
    func named(_ name: String?) {
        guard let name, let home = current, home.name != name, home.renamed != true else { return }
        update(home.url) { $0.name = name }
    }

    /// Adds `home` to the list, or merges it into the entry for the same address. Never by
    /// its id alone: an id is what the address says of itself (mDNS, `/api/discovery/home`, a
    /// link), so anyone on the network can claim one. Tokens stay with the exact address they
    /// were issued at; the same id at another address is another entry, signed in afresh.
    /// What the person named stays; a placeholder name gives way to a real one.
    private func remember(_ home: Home) -> Home {
        var entry: Home
        if let i = homes.firstIndex(where: { $0.url == home.url }) {
            entry = homes.remove(at: i)
        } else {
            entry = home
            entry.renamed = nil
        }
        if entry.hasPlaceholderName, !home.name.isEmpty, home.name != home.url.host() { entry.name = home.name }
        if let id = home.homeID { entry.homeID = id }
        if let id = home.cloudServerID { entry.cloudServerID = id }
        if let more = home.addresses { entry.addresses = merged(entry.addresses ?? [], more, without: entry.url) }
        entry.lastUsed = .now
        homes.append(entry)
        save()
        return entry
    }

    private func merged(_ a: [URL], _ b: [URL], without: URL) -> [URL]? {
        var out: [URL] = []
        for u in a + b where u != without && !out.contains(u) { out.append(u) }
        return out.isEmpty ? nil : out
    }

    private func update(_ url: URL, _ change: (inout Home) -> Void) {
        guard let i = homes.firstIndex(where: { $0.url == url }) else { return }
        change(&homes[i])
        if current?.url == url { current = homes[i] }
        save()
    }

    private func save() {
        defaults.set(try? JSONEncoder().encode(homes), forKey: "homes")
    }
}
