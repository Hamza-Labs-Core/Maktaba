import Foundation
import Network

/// Hands a home's stream to the system video player with the user's token on every request.
///
/// The home's web port serves `/stream/` only to a signed-in caller: the browser sends its
/// cookie, an app sends `Authorization: Bearer <access token>`. AVPlayer takes its headers
/// once, when the video opens, and the access token lasts 15 minutes, so a long video would
/// start failing mid-way. Instead the player reads from this proxy on the device's loopback
/// (`http://127.0.0.1:<port>/<secret>/stream/...`); each request goes on to the home with
/// the token as it is now (refreshed before it ends) and the answer comes back as it arrives.
///
/// Only the loopback listens (`acceptLocalOnly` is not set: the simulator refuses the
/// player's own connections with it), only paths under this proxy's random secret are served (another
/// app on the device cannot borrow the user's token), and only `/stream/` paths of this home.
///
/// A stream is copied for as long as it plays, so the copy is kept cheap and bounded:
/// - the body goes on in the chunks it arrives in (`UpstreamBody`), never byte by byte;
/// - one chunk is written before the next is taken, and a body is kept only up to a mark
///   (256 KB and one delivery of the URL session, measured at 4 MB at most):
///   a player that reads slowly or is paused holds the home back, memory does not grow;
/// - the request to the home ends the moment the player closes its connection (a channel
///   change, a seek, a rendition switch: the connection fails or a write to it does) and when
///   the proxy is stopped; at most `mostAtOnce` requests are served at once, and when
///   every place is taken a request whose player closed its side before any answer went
///   gives its place to a new one (503 only when every place is delivering);
/// - a connection takes one of those places only once its request's head has come and named
///   a path under the secret: until then it waits apart, at most `mostWaiting` of them, the
///   oldest closed first to make room and each closed after `headPatience`, so sockets that
///   connect and say nothing, or ask with a wrong secret, never keep a player out;
/// - a home that says nothing for `homePatience` while it is waited on fails the request.
///
/// A request goes on to the home the moment it comes (`Latency`): AVPlayer gives a segment
/// about one target duration (2 s for a 2 s segment) and then drops the stream's variant,
/// and the playback stops until the app reopens the stream. So nothing on the way waits on
/// anything else: every request is served at user-initiated priority (above the copying,
/// downloads and indexing a device does in the background, still below the interface), and
/// the token is the one held now (`heldPlaybackHeaders`), refreshed apart from any request
/// once it nears its end (`Token`).
///
/// A player that is connected and takes nothing (a paused film, a full buffer) is not
/// dropped for it, however long: it holds its place and the home is held back, and neither
/// the proxy nor the URL session times the pause. What can still end a paused request is the
/// home itself closing a connection it could not write to for too long: the player then
/// gets an error on its next read and asks again. On the device's
/// loopback a player that has gone is known by its connection (the system closes its
/// sockets); an AirPlay receiver on the network can vanish without a word, so the LAN
/// proxy's connections use TCP keepalive (`lanKeepalive`) and the stack finds it gone.
/// A body the home gives no length for goes to the player in chunks (`Transfer-Encoding:
/// chunked`), so one cut short is an error for the player and never a whole body.
/// The proxy can only pass on what the URL session tells it, and the URL session takes a
/// chunked body that the home's side closed in order before its last chunk (the home's
/// process exiting mid-answer) for a whole one: it reports success with what came. So the
/// home sends every segment, playlist and subtitle with its length (the streaming service's
/// `httpx.SetLength`), and here a body is whole only when the bytes that came are the
/// length the home said (`declaredLength`): fewer (or more) and the player's answer is cut
/// off, never completed, so the player asks again instead of decoding a truncated segment.
/// Every response says `Connection: close`: on the loopback a new connection costs a few
/// milliseconds a playlist or segment (measured, `ProxyCopyCost`), the requests to the home
/// reuse the URL session's connections all the same, and one request a connection needs no
/// request framing here.
///
/// An AirPlay receiver (story 48.24) cannot reach the device's loopback. A video hands it the
/// session's signed URL instead; a live channel at home has none (the web port's gate opens
/// channel paths to a signed-in caller only), so while it plays on a receiver a second proxy
/// listens on (and only on) the device's LAN address (`Reach.lan`), serving only that channel's paths
/// (`onlyUnder`) under its own secret, and is stopped when the receiver lets go.
public final class StreamProxy: @unchecked Sendable {
    /// Who can reach the proxy.
    public enum Reach: Sendable, Equatable {
        /// This device only (`127.0.0.1`).
        case loopback
        /// The local network too, at this address (an AirPlay receiver).
        case lan(host: String)
    }

    /// Where requests go on to: a home (with the user's token), or a broadcast's edge (the
    /// viewer token in the path, swapped for the newest at each request: `GuestLive`).
    private let upstream: Upstream
    enum Upstream: Sendable {
        case home(APIClient)
        case live(GuestLive)
    }
    private let sessions: UpstreamSessions
    private let listener: NWListener
    private let queue = DispatchQueue(label: "com.hamzalabs.maktaba.stream-proxy", qos: Latency.qos)
    /// The path prefix a request must carry: a per-proxy secret.
    let secret = UUID().uuidString.lowercased()
    private let lock = NSLock()
    private var boundPort: UInt16?
    /// Every connection the proxy holds, each with its task (cancelled by `stop`): `admitted`
    /// once its head named a path under the secret (it then counts against `mostAtOnce`),
    /// waiting for its head before (counted against `mostWaiting` instead).
    private var open: [ObjectIdentifier: Held] = [:]
    private struct Held {
        let peer: Peer
        var task: Task<Void, Never>?
        let order: Int
        var admitted = false
    }
    private var accepted = 0
    private var stopped = false
    /// A token refresh started ahead of the token's end is under way (`Token`).
    private var refreshingAhead = false
    /// The priority the last request was passed on at (a test reads it).
    private(set) var lastForwardPriority: TaskPriority? {
        get { lock.withLock { forwardPriority } }
        set { lock.withLock { forwardPriority = newValue } }
    }
    private var forwardPriority: TaskPriority?
    public let reach: Reach
    /// The only home paths served (`/stream/channel/{id}/`), nil: any `/stream/` path.
    public let onlyUnder: String?

    /// The most requests served at once: a player opens a handful (a playlist, a segment or
    /// two, a rendition switch); one that opens more without closing any is answered 503, so
    /// connections, tasks and requests to the home cannot pile up.
    static let mostAtOnce = 16
    /// The most connections kept while their request's head is awaited (or a refusal of one
    /// that asked for nothing of ours is delivered). They take none of `mostAtOnce`'s places;
    /// one more closes the oldest of them, so a client that opens sockets and sends nothing
    /// pushes out its own, not a player's request (whose head comes the moment it connects).
    static let mostWaiting = 32
    /// How long a connection may take to send its request's head before it is closed (a
    /// client that connects and says nothing). Nothing else is timed: a player that reads
    /// slowly, or not at all for a while, keeps its connection.
    static let headPatience: Duration = .seconds(30)
    private let headPatience: Duration
    /// How long the home may say nothing while the proxy waits on it (for the answer's head,
    /// or for the next bytes of a body) before the request is given up as failed: the minute
    /// a URL session gives a request. It does not run while the proxy itself holds the
    /// home's bytes for a player that reads slowly or is paused (`UpstreamBody`).
    static let homePatience: Duration = .seconds(60)
    private let homePatience: Duration
    /// The most bytes written to the player in one piece: backpressure and a player's leaving
    /// are felt slice by slice, not once per multi-megabyte chunk.
    static let slice = 64 << 10
    /// TCP keepalive on the LAN proxy's connections: a receiver silent for `idle` seconds is
    /// probed every `interval` seconds, and is gone after `count` unanswered probes (a
    /// minute in all); unacknowledged data gives up after `drop` seconds.
    static let lanKeepalive = (idle: 30, interval: 10, count: 3, drop: 120)

    /// How soon a request goes on: at once, whatever else is under way. Its stages (accepted,
    /// head read, token, passed on, the home's answer) are debug lines in `Log.network`,
    /// with the milliseconds since the connection was accepted.
    enum Latency {
        /// Every connection's callbacks, its task and its session's callbacks: above
        /// background work, below the interface (user-interactive).
        static let qos: DispatchQoS = .userInitiated
        static let priority: TaskPriority = .userInitiated
        static let operationQoS: QualityOfService = .userInitiated
    }

    /// The token a request carries: the one held now, when it is good for `floor` more
    /// (the home checks it when the request comes, not while the body goes). Once it is good
    /// for less than `ahead`, a refresh starts apart from the request (one at a time); only
    /// a request whose token has less than `floor` left (the app slept past it) waits for one.
    enum Token {
        static let floor: TimeInterval = 10
        static let ahead: TimeInterval = 60
    }

    /// How the proxy waits before asking the home again (a test's own).
    private let sleep: @Sendable (Duration) async throws -> Void

    /// Epic 57: a restricted account's media is served as it plays, and the home's streaming
    /// service answers 429 (`media-too-fast`, with `Retry-After`) to a player that reads more
    /// than about two minutes ahead of what plays. That is the player buffering, not a
    /// failure: the proxy waits as the home said and asks again, so the player sees a slow
    /// answer, never an error.
    enum Pace {
        /// How many times one request is asked again before the 429 is handed to the player.
        static let tries = 3
        /// The longest single wait, whatever the home said (its own is 30 s): two of them
        /// stay under the minute a player gives a request before it calls it failed.
        static let longest = 25

        /// The seconds to wait after a 429 with this `Retry-After` (2 when the home named none).
        static func wait(retryAfter: Int?) -> Int { min(max(retryAfter ?? 2, 1), longest) }
    }

    public convenience init(client: APIClient, session: URLSession = PinnedTrust.session, reach: Reach = .loopback,
                            onlyUnder: String? = nil) throws {
        try self.init(upstream: .home(client), session: session, reach: reach, onlyUnder: onlyUnder, headPatience: Self.headPatience, homePatience: Self.homePatience) { try await Task.sleep(for: $0) }
    }

    /// Story 50.27: a guest's broadcast. The edge (`live.maktaba.tv`) wants the viewer token in
    /// every playlist and segment path, and the token lasts 10 minutes: AVPlayer would keep
    /// the first one in every relative address it resolves. The player reads
    /// `http://127.0.0.1:<port>/<secret>/live/<channel>/…` (`localLive`) and each request goes
    /// on with the token `live` holds now (the heartbeat brings each new one).
    public convenience init(live: GuestLive, session: URLSession = .shared) throws {
        try self.init(upstream: .live(live), session: session, reach: .loopback, onlyUnder: nil, headPatience: Self.headPatience, homePatience: Self.homePatience) { try await Task.sleep(for: $0) }
    }

    convenience init(client: APIClient, session: URLSession, reach: Reach = .loopback, onlyUnder: String? = nil,
                     headPatience: Duration = StreamProxy.headPatience,
                     homePatience: Duration = StreamProxy.homePatience,
                     sleep: @escaping @Sendable (Duration) async throws -> Void) throws {
        try self.init(upstream: .home(client), session: session, reach: reach, onlyUnder: onlyUnder,
                      headPatience: headPatience, homePatience: homePatience, sleep: sleep)
    }

    init(upstream: Upstream, session: URLSession, reach: Reach = .loopback, onlyUnder: String? = nil,
         headPatience: Duration = StreamProxy.headPatience,
         homePatience: Duration = StreamProxy.homePatience,
         sleep: @escaping @Sendable (Duration) async throws -> Void) throws {
        self.headPatience = headPatience
        self.homePatience = homePatience
        self.upstream = upstream
        sessions = UpstreamSessions(like: session)
        self.sleep = sleep
        self.reach = reach
        self.onlyUnder = onlyUnder
        listener = try NWListener(using: Self.parameters(for: reach))
    }

    /// How the proxy listens for `reach`: its one address, and on the network TCP keepalive.
    static func parameters(for reach: Reach) -> NWParameters {
        let tcp = NWProtocolTCP.Options()
        if case .lan = reach {
            tcp.enableKeepalive = true
            tcp.keepaliveIdle = lanKeepalive.idle
            tcp.keepaliveInterval = lanKeepalive.interval
            tcp.keepaliveCount = lanKeepalive.count
            tcp.connectionDropTime = lanKeepalive.drop
        }
        let params = NWParameters(tls: nil, tcp: tcp)
        switch reach {
        case .loopback: params.requiredLocalEndpoint = .hostPort(host: .ipv4(.loopback), port: .any)
        // the LAN address alone: not cellular, not a VPN
        case let .lan(host): params.requiredLocalEndpoint = .hostPort(host: NWEndpoint.Host(host), port: .any)
        }
        return params
    }

    deinit { listener.cancel() }

    /// Starts listening; returns once the port is bound.
    public func start() async throws {
        if port != nil { return }
        try await withCheckedThrowingContinuation { (done: CheckedContinuation<Void, Error>) in
            let once = Once()
            listener.stateUpdateHandler = { [weak self] state in
                switch state {
                case .ready:
                    if let p = self?.listener.port?.rawValue { self?.setPort(p) }
                    once.run { done.resume() }
                case let .failed(error):
                    once.run { done.resume(throwing: error) }
                case .cancelled:
                    once.run { done.resume(throwing: CancellationError()) }
                default:
                    break
                }
            }
            listener.newConnectionHandler = { [weak self] conn in
                guard let self else { conn.cancel(); return }
                self.accept(conn)
            }
            listener.start(queue: queue)
        }
    }

    /// Stops listening and gives up every request being served (each one's request to the
    /// home is cancelled and its connection closed).
    public func stop() {
        listener.cancel()
        sessions.close()
        let serving = lock.withLock {
            stopped = true
            defer { open.removeAll() }
            return Array(open.values)
        }
        for s in serving {
            s.task?.cancel()
            s.peer.close()
        }
    }

    /// How many URL sessions wait for the next request (never more than a few).
    var idleSessions: Int { sessions.idleCount }

    /// Where each request being served stands (a failed test says it).
    var servingStates: [String] {
        let (peers, bodies) = lock.withLock { (open.values.map(\.peer), asking.compactMap { $0.body }) }
        return peers.map(\.state) + bodies.map { "home: " + $0.state }
    }

    /// The requests to the home under way (weakly: only for `servingStates`).
    private var asking: [Asking] = []
    private struct Asking { weak var body: UpstreamBody? }

    /// How many connections were accepted since the proxy started.
    var acceptedCount: Int { lock.withLock { accepted } }

    /// How many requests are being served now (admitted: a head under the secret came).
    var serving: Int { lock.withLock { open.values.filter(\.admitted).count } }

    /// How many connections wait for their request's head (or for a refusal to be read).
    var waitingForHead: Int { lock.withLock { open.values.filter { !$0.admitted }.count } }

    /// A new connection: served in a task of its own at `Latency.priority` (below the app's
    /// interface; above background work, so a busy device never holds a segment back past
    /// the player's patience), and cancelled the moment the
    /// player lets the connection go. It waits for its head apart from the requests being
    /// served (`admit` gives it a place once the head names a path under the secret); with
    /// `mostWaiting` already waiting, the oldest of them is closed to make room.
    private func accept(_ conn: NWConnection) {
        let peer = Peer(conn, queue: queue, headPatience: headPatience)
        let id = ObjectIdentifier(peer)
        let since = ContinuousClock.now
        let (taken, pushedOut, number) = lock.withLock { () -> (Bool, Held?, Int) in
            if stopped { return (false, nil, 0) }
            accepted += 1
            open[id] = Held(peer: peer, order: accepted)
            let waiting = open.filter { !$0.value.admitted }
            guard waiting.count > Self.mostWaiting,
                  let oldest = waiting.filter({ $0.key != id }).min(by: { $0.value.order < $1.value.order }) else { return (true, nil, accepted) }
            open.removeValue(forKey: oldest.key)
            return (true, oldest.value, accepted)
        }
        if let pushedOut {
            pushedOut.task?.cancel()
            pushedOut.peer.close()
        }
        guard taken else { conn.cancel(); return }
        Log.network.debug("stream proxy #\(number, privacy: .public): accepted")
        peer.start()
        let trace = Trace(number: number, since: since)
        let task = Task(priority: Latency.priority) { [self] in
            await serve(peer, id, trace)
            lock.withLock { _ = open.removeValue(forKey: id) }
        }
        lock.withLock { if open[id] != nil { open[id]?.task = task } }
        peer.whenGone { task.cancel() }
    }

    private enum Admission { case served, full, gone }

    /// Gives the waiting connection `id` a place among the requests served. Every place
    /// taken: the oldest request nobody waits for any more (its player closed its side before
    /// a byte of the answer went: a tune or a seek that moved on while the home was slow)
    /// gives its place up; only when every place is delivering is the new request refused.
    private func admit(_ id: ObjectIdentifier) -> Admission {
        let (admission, evicted) = lock.withLock { () -> (Admission, Held?) in
            guard !stopped, open[id] != nil else { return (.gone, nil) }
            let served = open.filter { $0.value.admitted }
            var victim: Held?
            if served.count >= Self.mostAtOnce {
                guard let v = served.filter({ $0.value.peer.abandoned }).min(by: { $0.value.order < $1.value.order }) else { return (.full, nil) }
                open.removeValue(forKey: v.key)
                victim = v.value
            }
            open[id]?.admitted = true
            return (.served, victim)
        }
        if let evicted {
            evicted.task?.cancel()
            evicted.peer.close()
        }
        return admission
    }

    /// The TCP options of the connections being served (a test reads the keepalive off them).
    var servingOptions: [NWProtocolTCP.Options] {
        lock.withLock { open.values.map(\.peer) }.compactMap { $0.conn.parameters.defaultProtocolStack.transportProtocol as? NWProtocolTCP.Options }
    }

    var port: UInt16? { lock.withLock { boundPort } }

    /// The address the listener is bound to.
    var boundHost: String? {
        guard case let .hostPort(host, _)? = listener.parameters.requiredLocalEndpoint else { return nil }
        return "\(host)"
    }
    private func setPort(_ p: UInt16) { lock.withLock { boundPort = p } }

    /// Where the player reads `remote` (an address on the home) from.
    public func local(_ remote: URL) -> URL? {
        guard let port, var c = URLComponents(url: remote, resolvingAgainstBaseURL: false) else { return nil }
        c.scheme = "http"
        switch reach {
        case .loopback: c.host = "127.0.0.1"
        case let .lan(host): c.host = host
        }
        c.port = Int(port)
        c.percentEncodedPath = "/" + secret + c.percentEncodedPath
        return c.url
    }

    /// Where the player reads a guest's channel (`StreamProxy(live:)`): its master playlist.
    public func localLive() -> URL? {
        guard let port, case let .live(live) = upstream else { return nil }
        return URL(string: "http://127.0.0.1:\(port)/\(secret)/live/\(live.channel)/master.m3u8")
    }

    /// The home's (or the edge's) address for a request line's target, nil when it is not ours
    /// to serve.
    func remote(forTarget target: String) -> URL? {
        let lower = target.lowercased()
        guard !lower.contains("/.."), !lower.contains("%2e") else { return nil }
        switch upstream {
        case let .home(client):
            let prefix = "/" + secret + "/stream/"
            guard target.hasPrefix(prefix) else { return nil }
            let rest = String(target.dropFirst(secret.count + 1))
            if let onlyUnder, !rest.hasPrefix(onlyUnder) { return nil }
            return URL(string: rest, relativeTo: client.home)?.absoluteURL
        case let .live(live):
            // only this channel's files, under the token held now
            let prefix = "/" + secret + "/live/" + live.channel + "/"
            guard target.hasPrefix(prefix) else { return nil }
            return live.remote(String(target.dropFirst(prefix.count)))
        }
    }

    // MARK: one request

    /// Reads the request's head, refuses what is not ours while the connection still waits
    /// (it takes no place), and serves the rest once `admit` gives it a place.
    private func serve(_ peer: Peer, _ id: ObjectIdentifier, _ trace: Trace) async {
        guard let (head, ended) = try? await receiveHead(peer.conn), let req = Request(head) else {
            trace.log("no request head")
            return await answer(peer, Self.status(400, "Bad Request"))
        }
        trace.log("\(req.method) \(route(req.target)) head read")
        // from here on nothing more is expected from the player; its connection failing is it
        // leaving (its side closing in order is only "no more requests": the answer still goes)
        peer.watch(ended: ended)
        guard req.method == "GET" || req.method == "HEAD", let url = remote(forTarget: req.target) else {
            return await answer(peer, Self.status(404, "Not Found"))
        }
        switch admit(id) {
        case .gone:
            peer.close()
        case .full:
            trace.log("refused: every place delivering")
            await answer(peer, Self.status(503, "Service Unavailable"))
        case .served:
            await conclude(peer, await handle(peer, req, url, trace))
        }
    }

    /// Sends a whole short answer (a refusal) and ends the connection.
    private func answer(_ peer: Peer, _ data: Data) async {
        await conclude(peer, (try? await send(peer, data)) != nil)
    }

    /// Ends the connection: in order when the answer went whole, at once when it was cut short.
    private func conclude(_ peer: Peer, _ whole: Bool) async {
        if whole {
            await finish(peer)
        } else {
            // cut short (the player left, the home failed mid-body): closed at once. The
            // player can tell: a body with a length is short of it, and one without is sent
            // in chunks and lacks its last chunk, so it is an error, never a whole body
            peer.abort()
        }
    }

    /// Ends the response and closes once the player has read it and closed its side
    /// (cancelling at once could drop bytes still on their way), or 5 s later.
    private func finish(_ peer: Peer) async {
        await withCheckedContinuation { (done: CheckedContinuation<Void, Never>) in
            let once = Once()
            peer.whenGone { once.run { done.resume() } }
            peer.conn.send(content: nil, contentContext: .finalMessage, isComplete: true,
                           completion: .contentProcessed { _ in once.run { done.resume() } })
        }
        await withCheckedContinuation { (done: CheckedContinuation<Void, Never>) in
            let once = Once()
            peer.whenReadEnds { once.run { done.resume() } }
            queue.asyncAfter(deadline: .now() + 5) { once.run { done.resume() } }
        }
        peer.close()
    }

    /// Answers one admitted request for the home's `url`. False when the response was cut short.
    private func handle(_ peer: Peer, _ req: Request, _ url: URL, _ trace: Trace) async -> Bool {
        let body: UpstreamBody, http: HTTPURLResponse?
        do {
            (body, http) = try await fetch(req, from: url, trace)
        } catch {
            trace.log("no answer from the home: \(type(of: error))")
            return (try? await send(peer, Self.status(502, "Bad Gateway"))) != nil
        }
        trace.log("the home answered \(http?.statusCode ?? 0)")
        // whatever happens next, the request to the home ends with this one
        defer { body.cancel() }
        do {
            var out = "HTTP/1.1 \(http?.statusCode ?? 502) \(HTTPURLResponse.localizedString(forStatusCode: http?.statusCode ?? 502))\r\n"
            for name in ["Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "Cache-Control", "ETag", "Last-Modified"] {
                if let v = http?.value(forHTTPHeaderField: name) { out += "\(name): \(v)\r\n" }
            }
            // a body whose length the home did not say would end at the close, and one cut
            // short would look whole: it goes in chunks (HTTP/1.1), ended by the last chunk
            // only when the home's answer ended well
            let code = http?.statusCode ?? 502
            let chunked = req.method == "GET" && req.version == "HTTP/1.1" && http?.value(forHTTPHeaderField: "Content-Length") == nil
                && code >= 200 && code != 204 && code != 304
            if chunked { out += "Transfer-Encoding: chunked\r\n" }
            out += "Connection: close\r\n\r\n"
            try await send(peer, Data(out.utf8))
            guard req.method == "GET" else { return true }
            let declared = Self.declaredLength(http)
            var came = 0
            // the body as it arrives, written a slice at a time: each slice is taken by the
            // connection before the next goes, and a chunk is written out before the next is
            // asked for, so a slow player slows the home's answer, not grows memory
            while let chunk = try await body.next() {
                came += chunk.count
                var at = chunk.startIndex
                while at < chunk.endIndex {
                    let end = min(at + Self.slice, chunk.endIndex)
                    if chunked {
                        var framed = Data("\(String(end - at, radix: 16))\r\n".utf8)
                        framed.append(chunk[at..<end])
                        framed.append(contentsOf: [13, 10])
                        try await send(peer, framed)
                    } else {
                        try await send(peer, chunk[at..<end])
                    }
                    at = end
                }
            }
            // the home said a length and the session ended well with another: the answer was
            // cut (the session does not always say so); it is not finished as a whole one
            if let declared, came != declared {
                Log.network.error("stream proxy: the home's answer ended at \(came, privacy: .public) of \(declared, privacy: .public) bytes")
                return false
            }
            if chunked { try await send(peer, Data("0\r\n\r\n".utf8)) }
            return true
        } catch {
            return false
        }
    }

    /// The body's length as the home said it (`Content-Length`), nil when it said none or
    /// the session decoded the body (a `Content-Encoding`: the length is the encoded one's)
    /// or the answer carries no body (204, 304).
    static func declaredLength(_ http: HTTPURLResponse?) -> Int? {
        guard let http, http.statusCode != 204, http.statusCode != 304,
              http.value(forHTTPHeaderField: "Content-Encoding").map({ $0.lowercased() == "identity" }) ?? true,
              let text = http.value(forHTTPHeaderField: "Content-Length")?.trimmingCharacters(in: .whitespaces),
              let n = Int(text), n >= 0 else { return nil }
        return n
    }

    /// Asks the home for `url` with the token as it is now. A 429 (the home paces a
    /// restricted account's media, `Pace`) is waited out as its `Retry-After` says and asked
    /// again, with a fresh token each time; after `Pace.tries` the player gets the 429.
    private func fetch(_ req: Request, from url: URL, _ trace: Trace) async throws -> (UpstreamBody, HTTPURLResponse?) {
        var asked = 0
        while true {
            var r = URLRequest(url: url)
            r.httpMethod = req.method
            for name in ["Range", "If-Range", "Accept"] { if let v = req.headers[name.lowercased()] { r.setValue(v, forHTTPHeaderField: name) } }
            if case let .home(client) = upstream {
                for (k, v) in try await token(client) { r.setValue(v, forHTTPHeaderField: k) }
                trace.log("token held")
            }
            // a session of this request's own: its callbacks are held while the player reads slowly
            let own = sessions.take()
            // the session's own idle timer must not run while a player's pause holds the home's
            // bytes here (it would fail the request and cut the film short on resume): it is
            // set out of the way, and `UpstreamBody` times the home's silence itself
            r.timeoutInterval = UpstreamSessions.never
            let body = UpstreamBody(r, session: own, silence: homePatience) { [sessions] in sessions.give(own) }
            lastForwardPriority = Task.currentPriority
            trace.log("passed on")
            lock.withLock {
                asking.removeAll { $0.body == nil }
                asking.append(Asking(body: body))
            }
            let http: HTTPURLResponse?
            do {
                http = try await body.response()
            } catch {
                body.cancel()
                throw error
            }
            asked += 1
            guard http?.statusCode == 429, asked < Pace.tries else { return (body, http) }
            body.cancel()
            try await sleep(.seconds(Pace.wait(retryAfter: http.flatMap(APIClient.retryAfter))))
        }
    }

    /// The header a request to the home carries (`Token`): the token held now, at once.
    private func token(_ client: APIClient) async throws -> [String: String] {
        guard let held = client.heldPlaybackHeaders(minLife: Token.floor) else {
            return try await client.playbackHeaders(minLife: Token.ahead).headers
        }
        if held.until.timeIntervalSinceNow < Token.ahead { refreshAhead(client) }
        return held.headers
    }

    /// Refreshes the token apart from any request (one refresh at a time).
    private func refreshAhead(_ client: APIClient) {
        let first = lock.withLock { () -> Bool in
            defer { refreshingAhead = true }
            return !refreshingAhead
        }
        guard first else { return }
        Task(priority: .utility) { [weak self] in
            do {
                _ = try await client.playbackHeaders(minLife: Token.ahead)
            } catch {
                Log.network.error("stream proxy: refreshing the token ahead failed: \(String(describing: type(of: error)), privacy: .public)")
            }
            self?.lock.withLock { self?.refreshingAhead = false }
        }
    }

    /// A request's target as a route for the log: the secret and the query left out, the ids
    /// taken out (`Log.route`).
    func route(_ target: String) -> String {
        var path = String(target.split(separator: "?").first ?? "")
        if path.hasPrefix("/" + secret) { path.removeFirst(secret.count + 1) } else { return "(not under the secret)" }
        return Log.route(path)
    }

    /// One request's stages in the log: its number and the milliseconds since it was accepted.
    struct Trace: Sendable {
        let number: Int
        let since: ContinuousClock.Instant
        func log(_ what: String) {
            let d = ContinuousClock.now - since
            let ms = d.components.seconds * 1000 + d.components.attoseconds / 1_000_000_000_000_000
            Log.network.debug("stream proxy #\(number, privacy: .public): \(what, privacy: .public) after \(ms, privacy: .public) ms")
        }
    }

    static func status(_ code: Int, _ reason: String) -> Data {
        Data("HTTP/1.1 \(code) \(reason)\r\nContent-Length: 0\r\nConnection: close\r\n\r\n".utf8)
    }

    /// The request's head, and whether the player's side closed with it (a request and a FIN
    /// together: nothing more can be read, and the answer still goes).
    private func receiveHead(_ conn: NWConnection) async throws -> (head: Data, ended: Bool) {
        var buf = Data()
        let end = Data("\r\n\r\n".utf8)
        var ended = false
        while buf.range(of: end) == nil {
            if ended { throw URLError(.networkConnectionLost) }
            let (more, complete): (Data, Bool) = try await withCheckedThrowingContinuation { done in
                conn.receive(minimumIncompleteLength: 1, maximumLength: 16 << 10) { data, _, complete, error in
                    if let error { done.resume(throwing: error) } else { done.resume(returning: (data ?? Data(), complete)) }
                }
            }
            buf += more
            ended = complete
            if buf.count > 32 << 10 { throw URLError(.dataLengthExceedsMaximum) }
        }
        return (buf, ended)
    }

    /// Writes `data` to the player; returns once the connection took it (which is what holds
    /// the next slice back while the player reads slowly, for as long as it does). Throws
    /// when the connection fails or was closed (the player left, the proxy stopped).
    private func send(_ peer: Peer, _ data: Data) async throws {
        try await peer.send(data)
    }
}

/// The player's end of one request: its connection, and the moment it is over (the
/// connection failed or was reset, a write failed, or the proxy closed it). Everything waiting
/// on the request hears. The player closing only its sending side is not that: it has no more
/// to ask, and still reads its answer.
final class Peer: @unchecked Sendable {
    let conn: NWConnection
    private let queue: DispatchQueue
    private let lock = NSLock()
    private var gone = false
    private var waiting: [@Sendable () -> Void] = []
    /// The player's side closed (or the connection is over): nothing more will be read.
    private var readEnded = false
    private var readWaiting: [@Sendable () -> Void] = []
    /// The request's head arrived in time.
    private var headRead = false
    /// The write under way (one at a time).
    private var writer: CheckedContinuation<Void, Error>?
    /// Something of the answer was handed to the connection.
    private var wrote = false

    /// The player closed its side and nothing of an answer has gone to it yet: nobody may be
    /// waiting for this request any more (or a client that half-closes waits on a slow home).
    var abandoned: Bool { lock.withLock { readEnded && !wrote && !gone } }

    /// Where the request stands, in a few words.
    var state: String {
        lock.withLock { "head \(headRead ? "read" : "awaited"), \(wrote ? "answering" : "nothing written")\(writer != nil ? ", a write waits" : "")\(readEnded ? ", read ended" : "")\(gone ? ", gone" : "")" }
    }
    private let headPatience: Duration

    init(_ conn: NWConnection, queue: DispatchQueue, headPatience: Duration) {
        self.conn = conn
        self.queue = queue
        self.headPatience = headPatience
    }

    /// Writes `data`; returns once the connection took it, however long the player takes;
    /// throws when the connection failed or is over.
    func send(_ data: Data) async throws {
        try await withCheckedThrowingContinuation { (done: CheckedContinuation<Void, Error>) in
            let here = lock.withLock { () -> Bool in
                if gone { return false }
                writer = done
                wrote = true
                return true
            }
            guard here else { done.resume(throwing: CancellationError()); return }
            conn.send(content: data, completion: .contentProcessed { [weak self] error in
                guard let self else { return }
                let w = self.lock.withLock { () -> CheckedContinuation<Void, Error>? in
                    defer { self.writer = nil }
                    return self.writer
                }
                if let error {
                    w?.resume(throwing: error)
                    // a write the connection refused: the player has gone
                    self.left()
                } else {
                    w?.resume()
                }
            })
        }
    }

    func start() {
        conn.stateUpdateHandler = { [weak self] state in
            switch state {
            case .failed, .cancelled: self?.left()
            default: break
            }
        }
        conn.start(queue: queue)
        // a client that connects and sends no whole request is closed; nothing else is timed
        let seconds = Double(headPatience.components.seconds) + Double(headPatience.components.attoseconds) / 1e18
        queue.asyncAfter(deadline: .now() + seconds) { [weak self] in
            guard let self, !self.lock.withLock({ self.headRead || self.gone }) else { return }
            self.close()
        }
    }

    /// Reads on after the request's head (a request has no body; anything more the player
    /// sends is dropped: one request a connection). A failed read is the player gone; its
    /// side closing in order ends the reading only.
    func watch(ended: Bool = false) {
        lock.withLock { headRead = true }
        if ended {
            readEnds()
            return
        }
        conn.receive(minimumIncompleteLength: 1, maximumLength: 4 << 10) { [weak self] data, _, complete, error in
            guard let self else { return }
            if error != nil {
                self.left()
            } else if complete || data?.isEmpty != false {
                self.readEnds()
            } else {
                self.watch()
            }
        }
    }

    /// Runs `f` once the connection is over (now, when it already is).
    func whenGone(_ f: @escaping @Sendable () -> Void) {
        let now = lock.withLock { () -> Bool in
            if !gone { waiting.append(f) }
            return gone
        }
        if now { f() }
    }

    /// Runs `f` once the player's side has closed or the connection is over.
    func whenReadEnds(_ f: @escaping @Sendable () -> Void) {
        let now = lock.withLock { () -> Bool in
            if !readEnded { readWaiting.append(f) }
            return readEnded
        }
        if now { f() }
    }

    /// Closes in order: what was written is delivered, then the connection ends.
    func close() {
        conn.cancel()
        left()
    }

    /// Ends the connection at once, without the orderly close (a response cut short).
    func abort() {
        conn.forceCancel()
        left()
    }

    private func readEnds() {
        let run = lock.withLock { () -> [@Sendable () -> Void] in
            readEnded = true
            defer { readWaiting = [] }
            return readWaiting
        }
        run.forEach { $0() }
    }

    private func left() {
        let (run, w) = lock.withLock { () -> ([@Sendable () -> Void], CheckedContinuation<Void, Error>?) in
            gone = true
            defer { waiting = []; writer = nil }
            return (waiting, writer)
        }
        w?.resume(throwing: CancellationError())
        run.forEach { $0() }
        readEnds()
    }
}

/// The URL sessions the proxy asks the home through: one for each request under way, so that
/// holding one request's callbacks back (`UpstreamBody`) holds nothing else back. Each is the
/// home session over again (its configuration, its delegate: the pinned trust), with a
/// callback queue of its own at the proxy's priority (`StreamProxy.Latency`). A session whose request ended is used
/// again by the next one, with the connection it kept open; a few idle ones are kept.
final class UpstreamSessions: @unchecked Sendable {
    /// How many idle sessions are kept for the next requests.
    static let keptIdle = 4
    /// A timeout that never comes (a week): the session's request and resource timers are
    /// not what ends a request here (`UpstreamBody`'s own count of the home's silence is).
    static let never: TimeInterval = 7 * 24 * 3600
    /// The callback queues' name, as a sample of the app shows them.
    static let queueName = "com.hamzalabs.maktaba.stream-proxy.upstream"

    private let base: URLSession
    private let lock = NSLock()
    private var idle: [URLSession] = []
    private var closed = false

    init(like base: URLSession) { self.base = base }

    deinit { idle.forEach { $0.invalidateAndCancel() } }

    /// A session no other request is using.
    func take() -> URLSession {
        if let s = lock.withLock({ idle.popLast() }) { return s }
        let callbacks = OperationQueue()
        callbacks.name = Self.queueName
        callbacks.maxConcurrentOperationCount = 1
        callbacks.qualityOfService = StreamProxy.Latency.operationQoS
        let config = base.configuration
        config.timeoutIntervalForResource = max(config.timeoutIntervalForResource, Self.never)
        return URLSession(configuration: config, delegate: base.delegate, delegateQueue: callbacks)
    }

    /// The request that took `session` has ended.
    func give(_ session: URLSession) {
        let keep = lock.withLock { () -> Bool in
            guard !closed, idle.count < Self.keptIdle else { return false }
            idle.append(session)
            return true
        }
        // a session holds its delegate until it is invalidated
        if !keep { session.finishTasksAndInvalidate() }
    }

    /// The proxy stopped: no session is kept any more.
    func close() {
        let all = lock.withLock { () -> [URLSession] in
            closed = true
            defer { idle = [] }
            return idle
        }
        all.forEach { $0.invalidateAndCancel() }
    }

    var idleCount: Int { lock.withLock { idle.count } }
}

/// One request to the home, its answer's body handed on in the chunks it arrives in.
///
/// `URLSession.AsyncBytes` gives a body one byte at a time: copying a stream through it costs
/// an async-sequence step and an append for every byte, for as long as a channel plays. Here
/// the session's data callbacks are taken as they come (tens of kilobytes to a few megabytes
/// each) and kept until `next()` asks.
///
/// What is kept is bounded by holding the callback: once `highWater` is kept, the data
/// callback does not return until `next()` has taken the bytes (or the request was
/// cancelled). A URL session delivers nothing more to a callback queue that is busy and
/// stops reading the socket, so TCP holds the home back. So at most `highWater` and one
/// delivery are ever kept. (Suspending the task is not a bound: measured against a fast
/// home, a task suspended after it had been resumed once went on delivering, hundreds of
/// megabytes.) The callback queue is therefore held while a player reads slowly: the session
/// must be this request's own (`UpstreamSessions`), never one shared with other requests.
/// Cancelling (the player left) lets a held callback go and ends the request to the home.
///
/// A held callback parks one thread of its session's queue (URLSession has no asynchronous
/// data callback to hold instead): one for each request whose player reads slowly or is
/// paused, at most `StreamProxy.mostAtOnce` (16) a proxy, 32 with the LAN proxy beside it;
/// in practice the one or two requests a paused player has open. The queues are at the
/// proxy's priority (`StreamProxy.Latency`) and named `UpstreamSessions.queueName`.
///
/// While a callback is held the session reads nothing, so its own idle timer would fail the
/// request about a minute into a pause: the proxy sets that timer out of the way and counts
/// the home's silence here instead (`silence`), only while it is the home that is waited on:
/// for the answer's head, or for more of the body once `next()` has asked and found nothing
/// kept. While bytes are kept, a callback is held or the player's write is in flight, nothing
/// is counted. Silence that
/// long fails the request (`URLError.timedOut`): the player gets an error, never a body
/// that looks whole.
final class UpstreamBody: NSObject, URLSessionDataDelegate, @unchecked Sendable {
    /// The bytes kept before the session's callbacks are held.
    static let highWater = 256 << 10

    private let lock = NSCondition()
    private var task: URLSessionDataTask?
    private var kept = Data()
    private var cancelled = false
    private var head: Result<HTTPURLResponse?, Error>?
    private var ended: Result<Void, Error>?
    private var headWaiter: CheckedContinuation<HTTPURLResponse?, Error>?
    private var bodyWaiter: CheckedContinuation<Data?, Error>?
    private var most = 0
    private var biggest = 0
    private let done: (@Sendable () -> Void)?
    /// A data callback is held (the proxy has the bytes: the home is not being waited on).
    private var held = false
    /// When the home last gave something, or `next()` began to wait for more.
    private var heard = ContinuousClock.now
    private let silence: Duration

    /// `session`: this request's own (its callbacks are held while the body waits to be
    /// taken). `silence`: how long the home may say nothing while it is waited on. `done`:
    /// called once, when the request has ended, however it ended.
    init(_ request: URLRequest, session: URLSession, silence: Duration = StreamProxy.homePatience,
         done: (@Sendable () -> Void)? = nil) {
        self.done = done
        self.silence = silence
        super.init()
        let t = session.dataTask(with: request)
        t.delegate = self
        task = t
        t.resume()
        listen()
    }

    /// Looks, a few times within `silence`, whether the home has said nothing for that long
    /// while it was waited on; the request then fails as timed out. Ends with the request.
    private func listen() {
        let step = max(silence / 4, .milliseconds(10))
        let seconds = Double(step.components.seconds) + Double(step.components.attoseconds) / 1e18
        DispatchQueue.global(qos: .utility).asyncAfter(deadline: .now() + seconds) { [weak self] in
            guard let self else { return }
            // waited on: for the answer's head, or for more of the body by a `next()` that
            // found nothing kept. Not while bytes are kept, a callback is held, or the player's
            // write is in flight (nobody has asked for more): then the proxy is not waiting on
            // the home, however long that lasts
            let (over, silent) = self.locked {
                (self.ended != nil || self.cancelled,
                 (self.head == nil || self.bodyWaiter != nil) && ContinuousClock.now - self.heard >= self.silence)
            }
            if over { return }
            if silent { self.fail(URLError(.timedOut)) } else { self.listen() }
        }
    }

    /// The request failed here (the home went silent): whoever waits hears, and it is cancelled.
    private func fail(_ error: Error) {
        let (h, b, t) = locked { () -> (CheckedContinuation<HTTPURLResponse?, Error>?, CheckedContinuation<Data?, Error>?, URLSessionDataTask?) in
            guard ended == nil else { return (nil, nil, nil) }
            ended = .failure(error)
            if head == nil { head = .failure(error) }
            kept = Data()
            cancelled = true
            lock.broadcast()
            defer { headWaiter = nil; bodyWaiter = nil }
            return (headWaiter, bodyWaiter, task)
        }
        h?.resume(throwing: error)
        b?.resume(throwing: error)
        t?.cancel()
    }

    private func locked<T>(_ f: () -> T) -> T {
        lock.lock()
        defer { lock.unlock() }
        return f()
    }

    /// Where the request stands, in a few words.
    var state: String {
        locked {
            "\(head == nil ? "head awaited" : "head came"), kept \(kept.count)\(held ? ", callback held" : "")\(bodyWaiter != nil ? ", asked for more" : "")"
                + "\(ended != nil ? ", ended" : "")\(cancelled ? ", cancelled" : ""), heard \(ContinuousClock.now - heard) ago, task \(task?.state.rawValue ?? -1)"
        }
    }

    /// The most bytes that were ever kept at once, and the biggest single delivery.
    var peak: Int { locked { most } }
    var biggestDelivery: Int { locked { biggest } }

    /// The answer's status and headers (nil when it is not HTTP).
    func response() async throws -> HTTPURLResponse? {
        try await withTaskCancellationHandler {
            try await withCheckedThrowingContinuation { (c: CheckedContinuation<HTTPURLResponse?, Error>) in
                let now = locked { () -> Result<HTTPURLResponse?, Error>? in
                    if head == nil { headWaiter = c }
                    return head
                }
                if let now { c.resume(with: now) }
            }
        } onCancel: {
            cancel()
        }
    }

    /// The next bytes of the body, in order; nil at its end. One caller at a time.
    func next() async throws -> Data? {
        try await withTaskCancellationHandler {
            try await withCheckedThrowingContinuation { (c: CheckedContinuation<Data?, Error>) in
                let now = locked { () -> Result<Data?, Error>? in
                    if !kept.isEmpty {
                        let data = kept
                        kept = Data()
                        // a callback held at the mark goes on
                        lock.broadcast()
                        return .success(data)
                    }
                    if let ended { return ended.map { nil } }
                    // from now the home is waited on: its silence counts from here
                    bodyWaiter = c
                    heard = .now
                    return nil
                }
                if let now { c.resume(with: now) }
            }
        } onCancel: {
            cancel()
        }
    }

    /// Ends the request to the home (nothing happens when it already ended). Whoever waits
    /// for the head or the body hears at once: not when the session says the task ended. That
    /// word comes on the session's callback queue and is not waited for, so a request whose
    /// word is late or never comes still ends (once cancelled, the silence is not counted,
    /// so nothing else would end it, and its place would be held for good).
    func cancel() {
        let (h, b, t) = locked { () -> (CheckedContinuation<HTTPURLResponse?, Error>?, CheckedContinuation<Data?, Error>?, URLSessionDataTask?) in
            cancelled = true
            lock.broadcast()
            if ended == nil { ended = .failure(CancellationError()) }
            if head == nil { head = .failure(CancellationError()) }
            defer { headWaiter = nil; bodyWaiter = nil }
            return (headWaiter, bodyWaiter, task)
        }
        h?.resume(throwing: CancellationError())
        b?.resume(throwing: CancellationError())
        t?.cancel()
    }

    // MARK: URLSession

    func urlSession(_ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse,
                    newRequest request: URLRequest, completionHandler: @escaping @Sendable (URLRequest?) -> Void) {
        // the token goes only to the origin it was issued by
        completionHandler(SameOriginRedirects.redirect(from: task.originalRequest ?? task.currentRequest, to: request))
    }

    func urlSession(_ session: URLSession, dataTask: URLSessionDataTask, didReceive response: URLResponse,
                    completionHandler: @escaping @Sendable (URLSession.ResponseDisposition) -> Void) {
        let http = response as? HTTPURLResponse
        let waiter = locked { () -> CheckedContinuation<HTTPURLResponse?, Error>? in
            heard = .now
            guard head == nil else { return nil }
            head = .success(http)
            defer { headWaiter = nil }
            return headWaiter
        }
        waiter?.resume(returning: http)
        completionHandler(.allow)
    }

    func urlSession(_ session: URLSession, dataTask: URLSessionDataTask, didReceive data: Data) {
        lock.lock()
        biggest = max(biggest, data.count)
        heard = .now
        if let w = bodyWaiter {
            // asked for already: handed straight on (nothing is kept while it waits)
            bodyWaiter = nil
            lock.unlock()
            w.resume(returning: data)
            return
        }
        if cancelled {
            lock.unlock()
            return
        }
        if kept.isEmpty { kept = data } else { kept.append(data) }
        most = max(most, kept.count)
        // held here until the bytes are taken: the session delivers nothing more meanwhile
        while kept.count >= Self.highWater, !cancelled {
            held = true
            lock.wait()
        }
        // let go: from now it is the home that is waited on again
        held = false
        heard = .now
        lock.unlock()
    }

    func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: Error?) {
        let (h, b) = locked { () -> (CheckedContinuation<HTTPURLResponse?, Error>?, CheckedContinuation<Data?, Error>?) in
            // one that failed here (the home's silence) keeps that failure
            if ended == nil { ended = error.map { .failure($0) } ?? .success(()) }
            if head == nil { head = .failure(error ?? URLError(.badServerResponse)) }
            self.task = nil
            defer { headWaiter = nil; bodyWaiter = nil }
            return (headWaiter, bodyWaiter)
        }
        h?.resume(throwing: error ?? URLError(.badServerResponse))
        if let error { b?.resume(throwing: error) } else { b?.resume(returning: nil) }
        done?()
    }
}

/// The request line and headers of an HTTP/1.1 request.
struct Request {
    let method: String
    let target: String
    /// `HTTP/1.1` (or `HTTP/1.0`).
    let version: String
    /// lower-cased names
    let headers: [String: String]

    init?(_ head: Data) {
        guard let text = String(data: head, encoding: .utf8) else { return nil }
        var lines = text.components(separatedBy: "\r\n")
        let first = lines.removeFirst().split(separator: " ")
        guard first.count == 3 else { return nil }
        method = String(first[0])
        target = String(first[1])
        version = String(first[2])
        var h: [String: String] = [:]
        for line in lines {
            guard let colon = line.firstIndex(of: ":") else { continue }
            h[line[..<colon].lowercased()] = line[line.index(after: colon)...].trimmingCharacters(in: .whitespaces)
        }
        headers = h
    }
}

/// Runs its closure once, from any thread.
final class Once: @unchecked Sendable {
    private let lock = NSLock()
    private var done = false
    func run(_ f: () -> Void) {
        let first = lock.withLock { () -> Bool in
            defer { done = true }
            return !done
        }
        if first { f() }
    }
}
