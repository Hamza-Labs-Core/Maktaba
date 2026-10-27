import Foundation
import Observation
import os

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

    public init(url: URL, name: String, homeID: String? = nil, addresses: [URL]? = nil) {
        self.url = url
        self.name = name
        self.homeID = homeID
        self.addresses = addresses
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
    /// The saved sign-in is being checked with the home (at launch, on picking a home).
    public private(set) var restoring = false
    /// The saved sign-in could not be checked: the home did not answer, or failed. The
    /// tokens stay; the sign-in screen says why and offers `retryRestore`.
    public private(set) var restoreProblem: AppError?

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

    public init(app: String, defaults: UserDefaults = .standard, probe: HomeProbe = HomeProbe(), asksHomes: Bool = true) {
        self.app = app
        self.defaults = defaults
        self.probe = probe
        self.asksHomes = asksHomes
        #if DEBUG
        // UI tests: start with no homes
        if CommandLine.arguments.contains("-maktaba-session-reset") {
            defaults.removeObject(forKey: "homes")
            defaults.removeObject(forKey: "home")
        }
        #endif
        homes = Self.decodeHomes(defaults.data(forKey: "homes"), defaults: defaults)
        if let last = defaults.url(forKey: "home"), let h = homes.first(where: { $0.url == last }) {
            use(h)
        }
    }

    /// Picks a home: its client, and signed in at once when the Keychain still has tokens.
    public func use(_ home: Home) {
        use(home, ask: asksHomes)
    }

    private func use(_ home: Home, ask: Bool) {
        let home = remember(home)
        current = home
        defaults.set(home.url, forKey: "home")
        let tokens = TokenStore(app: app, home: home.url)
        client = APIClient(home: home.url, tokens: tokens)
        phase = .signingIn
        signInMethods = nil
        restoreProblem = nil
        // checked at once: no sign-in form flashes while it is
        restoring = tokens.access != nil
        if restoring {
            Task { await restore() }
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
        restoreProblem = nil
        restoring = false
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
        restoreProblem = nil
        phase = .signingIn
    }

    /// A screen saw `APIError.signedOut`: back to signing in.
    public func signedOut() {
        approvingSignIn = false
        restoreProblem = nil
        phase = .signingIn
    }

    /// Checks the saved sign-in again (the sign-in screen's Try again after `restoreProblem`).
    public func retryRestore() async {
        await restore()
    }

    /// Signed in with the saved tokens, when the home still takes them. A home that does
    /// not answer leaves the tokens and says so (`restoreProblem`): the person is not
    /// signed out because the home was off for a moment.
    private func restore() async {
        guard let client else { return }
        restoring = true
        restoreProblem = nil
        defer { if self.client === client { restoring = false } }
        do {
            let me = try await client.whoami()
            guard self.client === client else { return }
            signedIn(me)
        } catch {
            guard self.client === client else { return }
            Log.failure("restore sign-in", error, logger: Log.session)
            // signed out: the client cleared the tokens, the sign-in screen it is
            if case APIError.signedOut = error { return }
            let e = AppError(error)
            if !e.isCancellation, e.kind != .signedOut { restoreProblem = e }
        }
    }

    private func signedIn(_ me: Me) {
        restoreProblem = nil
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
        do {
            defaults.set(try JSONEncoder().encode(homes), forKey: "homes")
        } catch {
            // never write nothing over the list
            Log.session.error("saving the homes failed: \(String(describing: error), privacy: .private)")
        }
    }

    /// The saved homes. A list this release cannot read (one from a newer release, a
    /// damaged one) is kept aside under `homes.unreadable` and logged, not lost silently.
    nonisolated static func decodeHomes(_ data: Data?, defaults: UserDefaults = .standard) -> [Home] {
        guard let data else { return [] }
        do {
            return try JSONDecoder().decode([Home].self, from: data)
        } catch {
            Log.session.fault("the saved homes could not be read (\(data.count, privacy: .public) bytes): \(String(describing: error), privacy: .private)")
            defaults.set(data, forKey: "homes.unreadable")
            return []
        }
    }
}
