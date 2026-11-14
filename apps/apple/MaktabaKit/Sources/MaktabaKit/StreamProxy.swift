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
/// - the body goes on in the chunks it arrives in (`UpstreamBody`), never byte by byte, in
///   tasks below the interface's priority;
/// - one chunk is written before the next is taken, and a body is kept only up to a mark
///   (256 KB and one delivery of the URL session, measured at 4 MB at most):
///   a player that reads slowly or is paused holds the home back, memory does not grow;
/// - the request to the home ends the moment the player closes its connection (a channel
///   change, a seek, a rendition switch: the connection fails or a write to it does) and when
///   the proxy is stopped; at most `mostAtOnce` requests are served at once, and when
///   every place is taken a request whose player closed its side before any answer went
///   gives its place to a new one (503 only when every place is delivering);
/// - a home that says nothing for `homePatience` while it is waited on fails the request.
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

    private let client: APIClient
    private let sessions: UpstreamSessions
    private let listener: NWListener
    private let queue = DispatchQueue(label: "com.hamzalabs.maktaba.stream-proxy", qos: .utility)
    /// The path prefix a request must carry: a per-proxy secret.
    let secret = UUID().uuidString.lowercased()
    private let lock = NSLock()
    private var boundPort: UInt16?
    /// The requests being served now, each with its task (cancelled by `stop`).
    private var open: [ObjectIdentifier: (peer: Peer, task: Task<Void, Never>?, order: Int)] = [:]
    private var accepted = 0
    private var stopped = false
    public let reach: Reach
    /// The only home paths served (`/stream/channel/{id}/`), nil: any `/stream/` path.
    public let onlyUnder: String?

    /// The most requests served at once: a player opens a handful (a playlist, a segment or
    /// two, a rendition switch); one that opens more without closing any is answered 503, so
    /// connections, tasks and requests to the home cannot pile up.
    static let mostAtOnce = 16
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
        try self.init(client: client, session: session, reach: reach, onlyUnder: onlyUnder, headPatience: Self.headPatience, homePatience: Self.homePatience) { try await Task.sleep(for: $0) }
    }

    init(client: APIClient, session: URLSession, reach: Reach = .loopback, onlyUnder: String? = nil,
         headPatience: Duration = StreamProxy.headPatience,
         homePatience: Duration = StreamProxy.homePatience,
         sleep: @escaping @Sendable (Duration) async throws -> Void) throws {
        self.headPatience = headPatience
        self.homePatience = homePatience
        self.client = client
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

    /// How many connections were accepted since the proxy started.
    var acceptedCount: Int { lock.withLock { accepted } }

    /// How many requests are being served now.
    var serving: Int { lock.withLock { open.count } }

    /// A new connection: served in a task of its own, below the app's interface in priority
    /// (copying a stream must never compete with the remote), and cancelled the moment the
    /// player lets the connection go.
    private func accept(_ conn: NWConnection) {
        let peer = Peer(conn, queue: queue, headPatience: headPatience)
        let id = ObjectIdentifier(peer)
        // every place taken: the oldest request nobody waits for any more (its player closed
        // its side before a byte of the answer went: a tune or a seek that moved on while the
        // home was slow) gives its place up; only when every place is delivering is the new
        // request refused
        let (full, evicted) = lock.withLock { () -> (Bool?, (peer: Peer, task: Task<Void, Never>?, order: Int)?) in
            if stopped { return (nil, nil) }
            accepted += 1
            open[id] = (peer, nil, accepted)
            guard open.count > Self.mostAtOnce else { return (false, nil) }
            let victim = open.filter { $0.key != id && $0.value.peer.abandoned }.min { $0.value.order < $1.value.order }
            guard let victim else { return (true, nil) }
            open.removeValue(forKey: victim.key)
            return (false, victim.value)
        }
        if let evicted {
            evicted.task?.cancel()
            evicted.peer.close()
        }
        guard let full else { conn.cancel(); return }
        peer.start()
        let task = Task(priority: .utility) { [self] in
            if full {
                try? await send(peer, Self.status(503, "Service Unavailable"))
                await finish(peer)
            } else {
                await serve(peer)
            }
            lock.withLock { _ = open.removeValue(forKey: id) }
        }
        lock.withLock { if open[id] != nil { open[id]?.task = task } }
        peer.whenGone { task.cancel() }
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

    /// The home's address for a request line's target, nil when it is not ours to serve.
    func remote(forTarget target: String) -> URL? {
        let prefix = "/" + secret + "/stream/"
        let lower = target.lowercased()
        guard target.hasPrefix(prefix), !lower.contains("/.."), !lower.contains("%2e") else { return nil }
        let rest = String(target.dropFirst(secret.count + 1))
        if let onlyUnder, !rest.hasPrefix(onlyUnder) { return nil }
        return URL(string: rest, relativeTo: client.home)?.absoluteURL
    }

    // MARK: one request

    private func serve(_ peer: Peer) async {
        if await handle(peer) {
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

    /// Answers one request. False when the response was cut short.
    private func handle(_ peer: Peer) async -> Bool {
        guard let (head, ended) = try? await receiveHead(peer.conn), let req = Request(head) else {
            return (try? await send(peer, Self.status(400, "Bad Request"))) != nil
        }
        // from here on nothing more is expected from the player; its connection failing is it
        // leaving (its side closing in order is only "no more requests": the answer still goes)
        peer.watch(ended: ended)
        guard req.method == "GET" || req.method == "HEAD", let url = remote(forTarget: req.target) else {
            return (try? await send(peer, Self.status(404, "Not Found"))) != nil
        }
        let body: UpstreamBody, http: HTTPURLResponse?
        do {
            (body, http) = try await fetch(req, from: url)
        } catch {
            return (try? await send(peer, Self.status(502, "Bad Gateway"))) != nil
        }
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
            // the body as it arrives, written a slice at a time: each slice is taken by the
            // connection before the next goes, and a chunk is written out before the next is
            // asked for, so a slow player slows the home's answer, not grows memory
            while let chunk = try await body.next() {
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
            if chunked { try await send(peer, Data("0\r\n\r\n".utf8)) }
            return true
        } catch {
            return false
        }
    }

    /// Asks the home for `url` with the token as it is now. A 429 (the home paces a
    /// restricted account's media, `Pace`) is waited out as its `Retry-After` says and asked
    /// again, with a fresh token each time; after `Pace.tries` the player gets the 429.
    private func fetch(_ req: Request, from url: URL) async throws -> (UpstreamBody, HTTPURLResponse?) {
        var asked = 0
        while true {
            var r = URLRequest(url: url)
            r.httpMethod = req.method
            for name in ["Range", "If-Range", "Accept"] { if let v = req.headers[name.lowercased()] { r.setValue(v, forHTTPHeaderField: name) } }
            for (k, v) in try await client.playbackHeaders(minLife: 60).headers { r.setValue(v, forHTTPHeaderField: k) }
            // a session of this request's own: its callbacks are held while the player reads slowly
            let own = sessions.take()
            // the session's own idle timer must not run while a player's pause holds the home's
            // bytes here (it would fail the request and cut the film short on resume): it is
            // set out of the way, and `UpstreamBody` times the home's silence itself
            r.timeoutInterval = UpstreamSessions.never
            let body = UpstreamBody(r, session: own, silence: homePatience) { [sessions] in sessions.give(own) }
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
/// callback queue of its own at utility priority. A session whose request ended is used
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
        callbacks.qualityOfService = .utility
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
/// in practice the one or two requests a paused player has open. The queues are utility
/// priority and named `UpstreamSessions.queueName`.
///
/// While a callback is held the session reads nothing, so its own idle timer would fail the
/// request about a minute into a pause: the proxy sets that timer out of the way and counts
/// the home's silence here instead (`silence`), only while it is the home that is waited on
/// (for the answer's head, or for more of the body while nothing is held). Silence that
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
    /// When the home last gave something, or a held callback was let go.
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
            let (over, silent) = self.locked { (self.ended != nil || self.cancelled, !self.held && ContinuousClock.now - self.heard >= self.silence) }
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
                    bodyWaiter = c
                    return nil
                }
                if let now { c.resume(with: now) }
            }
        } onCancel: {
            cancel()
        }
    }

    /// Ends the request to the home (nothing happens when it already ended).
    func cancel() {
        let t = locked { () -> URLSessionDataTask? in
            cancelled = true
            lock.broadcast()
            return task
        }
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
