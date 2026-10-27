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
public final class StreamProxy: @unchecked Sendable {
    private let client: APIClient
    private let session: URLSession
    private let listener: NWListener
    private let queue = DispatchQueue(label: "com.hamzalabs.maktaba.stream-proxy")
    /// The path prefix a request must carry: a per-proxy secret.
    let secret = UUID().uuidString.lowercased()
    private let lock = NSLock()
    private var boundPort: UInt16?

    public init(client: APIClient, session: URLSession = .shared) throws {
        self.client = client
        self.session = session
        let params = NWParameters.tcp
        params.requiredLocalEndpoint = .hostPort(host: .ipv4(.loopback), port: .any)
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
                conn.start(queue: self.queue)
                Task { await self.serve(conn) }
            }
            listener.start(queue: queue)
        }
    }

    public func stop() { listener.cancel() }

    var port: UInt16? { lock.withLock { boundPort } }
    private func setPort(_ p: UInt16) { lock.withLock { boundPort = p } }

    /// Where the player reads `remote` (an address on the home) from.
    public func local(_ remote: URL) -> URL? {
        guard let port, var c = URLComponents(url: remote, resolvingAgainstBaseURL: false) else { return nil }
        c.scheme = "http"
        c.host = "127.0.0.1"
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
        return URL(string: rest, relativeTo: client.home)?.absoluteURL
    }

    // MARK: one request

    private func serve(_ conn: NWConnection) async {
        await handle(conn)
        await finish(conn)
    }

    /// Ends the response and closes once the player has read it (cancelling at once could
    /// drop bytes still on their way).
    private func finish(_ conn: NWConnection) async {
        await withCheckedContinuation { (done: CheckedContinuation<Void, Never>) in
            conn.send(content: nil, contentContext: .finalMessage, isComplete: true, completion: .contentProcessed { _ in done.resume() })
        }
        let closer = Task { [queue] in
            try? await Task.sleep(for: .seconds(5))
            queue.async { conn.cancel() }
        }
        await withCheckedContinuation { (done: CheckedContinuation<Void, Never>) in
            conn.receive(minimumIncompleteLength: 1, maximumLength: 1) { _, _, _, _ in done.resume() }
        }
        closer.cancel()
        conn.cancel()
    }

    private func handle(_ conn: NWConnection) async {
        guard let head = try? await receiveHead(conn), let req = Request(head) else {
            try? await send(conn, Self.status(400, "Bad Request"))
            return
        }
        guard req.method == "GET" || req.method == "HEAD", let url = remote(forTarget: req.target) else {
            try? await send(conn, Self.status(404, "Not Found"))
            return
        }
        do {
            var r = URLRequest(url: url)
            r.httpMethod = req.method
            for name in ["Range", "If-Range", "Accept"] { if let v = req.headers[name.lowercased()] { r.setValue(v, forHTTPHeaderField: name) } }
            for (k, v) in try await client.playbackHeaders(minLife: 60).headers { r.setValue(v, forHTTPHeaderField: k) }
            let (bytes, response) = try await session.bytes(for: r, delegate: SameOriginRedirects.shared)
            let http = response as? HTTPURLResponse
            var out = "HTTP/1.1 \(http?.statusCode ?? 502) \(HTTPURLResponse.localizedString(forStatusCode: http?.statusCode ?? 502))\r\n"
            for name in ["Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "Cache-Control", "ETag", "Last-Modified"] {
                if let v = http?.value(forHTTPHeaderField: name) { out += "\(name): \(v)\r\n" }
            }
            out += "Connection: close\r\n\r\n"
            try await send(conn, Data(out.utf8))
            guard req.method == "GET" else { return }
            var chunk = Data()
            chunk.reserveCapacity(64 << 10)
            for try await b in bytes {
                chunk.append(b)
                if chunk.count >= 64 << 10 {
                    try await send(conn, chunk)
                    chunk.removeAll(keepingCapacity: true)
                }
            }
            if !chunk.isEmpty { try await send(conn, chunk) }
        } catch {
            try? await send(conn, Self.status(502, "Bad Gateway"))
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

    private func send(_ conn: NWConnection, _ data: Data) async throws {
        try await withCheckedThrowingContinuation { (done: CheckedContinuation<Void, Error>) in
            conn.send(content: data, completion: .contentProcessed { error in
                if let error { done.resume(throwing: error) } else { done.resume() }
            })
        }
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
private final class Once: @unchecked Sendable {
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
