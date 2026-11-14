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
///   change, a seek, a rendition switch), when the player takes nothing for `patience`, and
///   when the proxy is stopped; at most `mostAtOnce` requests are served at once.
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
    /// How long the player may take nothing while a response waits to be written before the
    /// request is given up (the home's request is cancelled, the connection closed): a
    /// player that stopped reading without closing does not hold a request to the home open
    /// for ever. A paused player that comes back asks again, with a Range.
    static let patience: Duration = .seconds(60)
    private let patience: Duration

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
        try self.init(client: client, session: session, reach: reach, onlyUnder: onlyUnder, patience: Self.patience) { try await Task.sleep(for: $0) }
    }

    init(client: APIClient, session: URLSession, reach: Reach = .loopback, onlyUnder: String? = nil,
         patience: Duration = StreamProxy.patience,
         sleep: @escaping @Sendable (Duration) async throws -> Void) throws {
        self.patience = patience
        self.client = client
        self.session = session
        self.sleep = sleep
        self.reach = reach
        self.onlyUnder = onlyUnder
        let params = NWParameters.tcp
        switch reach {
        case .loopback: params.requiredLocalEndpoint = .hostPort(host: .ipv4(.loopback), port: .any)
        // the LAN address alone: not cellular, not a VPN
        case let .lan(host): params.requiredLocalEndpoint = .hostPort(host: NWEndpoint.Host(host), port: .any)
        }
        listener = try NWListener(using: params)
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
        let peer = Peer(conn, queue: queue, patience: patience)
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
            // given up part-way (the player left, the home failed mid-body): closed at once,
            // so a body cut short is never taken for a whole one
            peer.close()
        }
    }

    /// Ends the response and closes once the player has read it (cancelling at once could
    /// drop bytes still on their way), or 5 s later.
    private func finish(_ peer: Peer) async {
        peer.conn.send(content: nil, contentContext: .finalMessage, isComplete: true, completion: .idempotent)
        await withCheckedContinuation { (done: CheckedContinuation<Void, Never>) in
            let once = Once()
            peer.whenGone { once.run { done.resume() } }
            queue.asyncAfter(deadline: .now() + 5) { once.run { done.resume() } }
        }
        peer.close()
    }

    /// Answers one request. False when the response was cut short.
    private func handle(_ peer: Peer) async -> Bool {
        guard let head = try? await receiveHead(peer.conn), let req = Request(head) else {
            return (try? await send(peer, Self.status(400, "Bad Request"))) != nil
        }
        // from here on nothing more is expected from the player: when its side closes, it left
        peer.watch()
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
            out += "Connection: close\r\n\r\n"
            try await send(peer, Data(out.utf8))
            guard req.method == "GET" else { return true }
            // the body as it arrives: one chunk is written (and taken by the player) before
            // the next is asked for, so a slow player slows the home's answer, not grows memory
            while let chunk = try await body.next() {
                try await send(peer, chunk)
            }
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

    private func receiveHead(_ conn: NWConnection) async throws -> Data {
        var buf = Data()
        let end = Data("\r\n\r\n".utf8)
        while buf.range(of: end) == nil {
            let more: Data = try await withCheckedThrowingContinuation { done in
                conn.receive(minimumIncompleteLength: 1, maximumLength: 16 << 10) { data, _, complete, error in
                    if let error { done.resume(throwing: error) } else if let data, !data.isEmpty { done.resume(returning: data) } else if complete { done.resume(throwing: URLError(.networkConnectionLost)) } else { done.resume(returning: Data()) }
                }
            }
            buf += more
            if buf.count > 32 << 10 { throw URLError(.dataLengthExceedsMaximum) }
        }
        return buf
    }

    /// Writes `data` to the player; returns once the connection took it (which is what holds
    /// the next chunk back while the player reads slowly). Throws when the connection fails
    /// or the player has gone (it closed, the proxy stopped, or it took nothing for `patience`).
    private func send(_ peer: Peer, _ data: Data) async throws {
        try await peer.send(data)
    }
}

/// The player's end of one request: its connection, and the moment the player let it go
/// (closed it, reset it, or the proxy closed it). Everything waiting on the request hears.
final class Peer: @unchecked Sendable {
    let conn: NWConnection
    private let queue: DispatchQueue
    private let lock = NSLock()
    private var gone = false
    private var waiting: [@Sendable () -> Void] = []
    /// The write under way, and since when (one at a time).
    private var writer: CheckedContinuation<Void, Error>?
    private var writingSince: ContinuousClock.Instant?
    private let patience: Duration

    init(_ conn: NWConnection, queue: DispatchQueue, patience: Duration) {
        self.conn = conn
        self.queue = queue
        self.patience = patience
    }

    /// Writes `data`; returns once the connection took it, throws when it failed or the
    /// player has gone.
    func send(_ data: Data) async throws {
        try await withCheckedThrowingContinuation { (done: CheckedContinuation<Void, Error>) in
            let here = lock.withLock { () -> Bool in
                if gone { return false }
                writer = done
                writingSince = .now
                return true
            }
            guard here else { done.resume(throwing: CancellationError()); return }
            conn.send(content: data, completion: .contentProcessed { [weak self] error in
                guard let w = self?.takeWriter() else { return }
                if let error { w.resume(throwing: error) } else { w.resume() }
            })
        }
    }

    private func takeWriter() -> CheckedContinuation<Void, Error>? {
        lock.withLock {
            defer { writer = nil; writingSince = nil }
            return writer
        }
    }

    /// Looks, a few times within `patience`, whether one write has waited that long (the
    /// player takes nothing and has not closed): the connection is then closed. One timer a
    /// connection, ended when the player has gone.
    private func keepWatch() {
        let step = max(patience / 4, .milliseconds(10))
        let seconds = Double(step.components.seconds) + Double(step.components.attoseconds) / 1e18
        queue.asyncAfter(deadline: .now() + seconds) { [weak self] in
            guard let self else { return }
            let (over, late) = self.lock.withLock { (self.gone, self.writingSince.map { ContinuousClock.now - $0 >= self.patience } ?? false) }
            if over { return }
            if late { self.close() } else { self.keepWatch() }
        }
    }

    func start() {
        conn.stateUpdateHandler = { [weak self] state in
            switch state {
            case .failed, .cancelled: self?.left()
            default: break
            }
        }
        // the request's head is waited for no longer than a write is
        lock.withLock { writingSince = .now }
        conn.start(queue: queue)
        keepWatch()
    }

    /// Reads on after the request's head: a request has no body, so the only news is that the
    /// player closed its side (anything else it sends is dropped: one request a connection).
    func watch() {
        lock.withLock { if writer == nil { writingSince = nil } }
        conn.receive(minimumIncompleteLength: 1, maximumLength: 4 << 10) { [weak self] data, _, complete, error in
            guard let self else { return }
            if error != nil || complete || data?.isEmpty != false { self.left() } else { self.watch() }
        }
    }

    /// Runs `f` once the player has gone (now, when it already has).
    func whenGone(_ f: @escaping @Sendable () -> Void) {
        let now = lock.withLock { () -> Bool in
            if !gone { waiting.append(f) }
            return gone
        }
        if now { f() }
    }

    func close() {
        conn.cancel()
        left()
    }

    private func left() {
        let (run, w) = lock.withLock { () -> ([@Sendable () -> Void], CheckedContinuation<Void, Error>?) in
            gone = true
            defer { waiting = []; writer = nil; writingSince = nil }
            return (waiting, writer)
        }
        w?.resume(throwing: CancellationError())
        run.forEach { $0() }
    }
}

/// One request to the home, its answer's body handed on in the chunks it arrives in.
///
/// `URLSession.AsyncBytes` gives a body one byte at a time: copying a stream through it costs
/// an async-sequence step and an append for every byte, for as long as a channel plays. Here
/// the session's data callbacks are taken as they come (tens of kilobytes each) and kept
/// until `next()` asks. No more than about `highWater` is ever kept: at that mark the task is
/// suspended (URLSession stops reading the socket, so TCP holds the home back), and it is
/// resumed when the kept bytes are taken. Cancelling it (the player left) ends the request
/// to the home at once, also while suspended.
final class UpstreamBody: NSObject, URLSessionDataDelegate, @unchecked Sendable {
    /// The bytes kept before the home is held back.
    static let highWater = 1 << 20

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
    /// lower-cased names
    let headers: [String: String]

    init?(_ head: Data) {
        guard let text = String(data: head, encoding: .utf8) else { return nil }
        var lines = text.components(separatedBy: "\r\n")
        let first = lines.removeFirst().split(separator: " ")
        guard first.count == 3 else { return nil }
        method = String(first[0])
        target = String(first[1])
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
