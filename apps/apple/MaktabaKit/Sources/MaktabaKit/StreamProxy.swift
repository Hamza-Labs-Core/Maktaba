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
/// - one chunk is written before the next is taken, and at most about 1 MB of a body is kept:
///   a player that reads slowly or is paused holds the home back, memory does not grow;
/// - the request to the home ends the moment the player closes its connection (a channel
///   change, a seek, a rendition switch: the connection fails or a write to it does) and when
///   the proxy is stopped; at most `mostAtOnce` requests are served at once.
/// A player that is connected and takes nothing (a paused film, a full buffer) is never
/// dropped, however long: it holds its place and the home is held back. On the device's
/// loopback a player that has gone is known by its connection (the system closes its
/// sockets); an AirPlay receiver on the network can vanish without a word, so the LAN
/// proxy's connections use TCP keepalive (`lanKeepalive`) and the stack finds it gone.
/// A body the home gives no length for goes to the player in chunks (`Transfer-Encoding:
/// chunked`), so one cut short is an error for the player and never a whole body.
/// Every response says `Connection: close`: on the loopback a new connection costs a few
/// milliseconds a playlist or segment (measured, `ProxyCopyCost`), the requests to the home
/// reuse the URL session's connections all the same, and one request a connection is what
/// lets a closed connection mean "the player left".
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
    private let session: URLSession
    private let listener: NWListener
    private let queue = DispatchQueue(label: "com.hamzalabs.maktaba.stream-proxy", qos: .utility)
    /// The path prefix a request must carry: a per-proxy secret.
    let secret = UUID().uuidString.lowercased()
    private let lock = NSLock()
    private var boundPort: UInt16?
    /// The requests being served now, each with its task (cancelled by `stop`).
    private var open: [ObjectIdentifier: (peer: Peer, task: Task<Void, Never>?)] = [:]
    private var stopped = false
    public let reach: Reach
    /// The only home paths served (`/stream/channel/{id}/`), nil: any `/stream/` path.
    public let onlyUnder: String?

    /// The most requests served at once: a player opens a handful (a playlist, a segment or
    /// two, a rendition switch); one that opens more without closing any is answered 503, so
    /// connections, tasks and requests to the home cannot pile up.
    static let mostAtOnce = 32
    /// How long a connection may take to send its request's head before it is closed (a
    /// client that connects and says nothing). Nothing else is timed: a player that reads
    /// slowly, or not at all for a while, keeps its connection.
    static let headPatience: Duration = .seconds(30)
    private let headPatience: Duration
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
        try self.init(client: client, session: session, reach: reach, onlyUnder: onlyUnder, headPatience: Self.headPatience) { try await Task.sleep(for: $0) }
    }

    init(client: APIClient, session: URLSession, reach: Reach = .loopback, onlyUnder: String? = nil,
         headPatience: Duration = StreamProxy.headPatience,
         sleep: @escaping @Sendable (Duration) async throws -> Void) throws {
        self.headPatience = headPatience
        self.client = client
        self.session = session
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

    /// How many requests are being served now.
    var serving: Int { lock.withLock { open.count } }

    /// A new connection: served in a task of its own, below the app's interface in priority
    /// (copying a stream must never compete with the remote), and cancelled the moment the
    /// player lets the connection go.
    private func accept(_ conn: NWConnection) {
        let peer = Peer(conn, queue: queue, headPatience: headPatience)
        let id = ObjectIdentifier(peer)
        let full = lock.withLock { () -> Bool? in
            if stopped { return nil }
            open[id] = (peer, nil)
            return open.count > Self.mostAtOnce
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
            let body = UpstreamBody(r, session: session)
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

/// One request to the home, its answer's body handed on in the chunks it arrives in.
///
/// `URLSession.AsyncBytes` gives a body one byte at a time: copying a stream through it costs
/// an async-sequence step and an append for every byte, for as long as a channel plays. Here
/// the session's data callbacks are taken as they come (tens of kilobytes each) and kept
/// until `next()` asks. At `highWater` kept the task is suspended (URLSession stops reading
/// the socket, so TCP holds the home back) and it is resumed when the kept bytes are taken.
/// What is kept can pass the mark by what URLSession had already read when it was suspended
/// (about 4 MB was seen on a Mac's loopback; `ceiling` is what the tests hold it to): bounded
/// by the session's own buffers, not by the body's length.
///
/// The session's delegate queue is not lowered in priority: it is the home session's own
/// (`PinnedTrust.session`), shared with every API request; these callbacks only append and
/// hand on. Cancelling it (the player left) ends the request
/// to the home at once, also while suspended.
final class UpstreamBody: NSObject, URLSessionDataDelegate, @unchecked Sendable {
    /// The bytes kept before the home is held back.
    static let highWater = 1 << 20
    /// The most that may ever be kept (the mark and URLSession's overshoot).
    static let ceiling = 8 << 20

    private let lock = NSLock()
    private var task: URLSessionDataTask?
    private var kept = Data()
    private var paused = false
    private var head: Result<HTTPURLResponse?, Error>?
    private var ended: Result<Void, Error>?
    private var headWaiter: CheckedContinuation<HTTPURLResponse?, Error>?
    private var bodyWaiter: CheckedContinuation<Data?, Error>?
    private var most = 0

    init(_ request: URLRequest, session: URLSession) {
        super.init()
        let t = session.dataTask(with: request)
        t.delegate = self
        task = t
        t.resume()
    }

    /// The most bytes that were ever kept at once.
    var peak: Int { lock.withLock { most } }

    /// The answer's status and headers (nil when it is not HTTP).
    func response() async throws -> HTTPURLResponse? {
        try await withTaskCancellationHandler {
            try await withCheckedThrowingContinuation { (c: CheckedContinuation<HTTPURLResponse?, Error>) in
                let now = lock.withLock { () -> Result<HTTPURLResponse?, Error>? in
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
                let now = lock.withLock { () -> Result<Data?, Error>? in
                    if !kept.isEmpty {
                        let data = kept
                        kept = Data()
                        if paused {
                            paused = false
                            task?.resume()
                        }
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
        lock.withLock { task }?.cancel()
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
        let waiter = lock.withLock { () -> CheckedContinuation<HTTPURLResponse?, Error>? in
            head = .success(http)
            defer { headWaiter = nil }
            return headWaiter
        }
        waiter?.resume(returning: http)
        completionHandler(.allow)
    }

    func urlSession(_ session: URLSession, dataTask: URLSessionDataTask, didReceive data: Data) {
        let waiter = lock.withLock { () -> CheckedContinuation<Data?, Error>? in
            if let w = bodyWaiter {
                // asked for already: handed straight on (nothing is kept while it waits)
                bodyWaiter = nil
                return w
            }
            if kept.isEmpty { kept = data } else { kept.append(data) }
            most = max(most, kept.count)
            if kept.count >= Self.highWater, !paused {
                paused = true
                dataTask.suspend()
            }
            return nil
        }
        waiter?.resume(returning: data)
    }

    func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: Error?) {
        let (h, b) = lock.withLock { () -> (CheckedContinuation<HTTPURLResponse?, Error>?, CheckedContinuation<Data?, Error>?) in
            let end: Result<Void, Error> = error.map { .failure($0) } ?? .success(())
            ended = end
            if head == nil { head = .failure(error ?? URLError(.badServerResponse)) }
            self.task = nil
            defer { headWaiter = nil; bodyWaiter = nil }
            return (headWaiter, bodyWaiter)
        }
        h?.resume(throwing: error ?? URLError(.badServerResponse))
        if let error { b?.resume(throwing: error) } else { b?.resume(returning: nil) }
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
