import Foundation
import Network
import Testing
@testable import MaktabaKit

/// A home on this Mac's loopback for the stream proxy's tests: a real HTTP/1.1 listener (a
/// `URLProtocol` stub has no socket, so nothing can hold it back or be seen to go away).
/// Each path answers as its `Answer` says; the home counts what it wrote and notices a
/// request that was given up before its answer was whole.
final class LoopbackHome: @unchecked Sendable {
    struct Answer: Sendable {
        var status = 200
        var headers: [String: String] = [:]
        /// The body's length (its bytes are `LoopbackHome.byte(at:)`).
        var length = 0
        /// Whether the length is said (`Content-Length`); without it the body goes in chunks
        /// (`Transfer-Encoding: chunked`), as a home's does.
        var saysLength = true
        /// Never answers: the request stays open until the caller lets go (or `dropHeld`).
        var holds = false
        /// The connection is dropped after this many body bytes (the home failing mid-body).
        var cutAfter: Int?
    }

    struct Seen: Sendable {
        let method: String
        let target: String
        let headers: [String: String]
    }

    private let listener: NWListener
    private let queue = DispatchQueue(label: "test.loopback-home")
    private let lock = NSLock()
    private var answers: [String: [Answer]] = [:]
    private var requests: [Seen] = []
    private var written = 0
    private var givenUp = 0
    private var whole = 0
    private var conns: [NWConnection] = []
    private var held: [NWConnection] = []
    private(set) var port: UInt16 = 0

    init() async throws {
        let params = NWParameters.tcp
        params.requiredLocalEndpoint = .hostPort(host: .ipv4(.loopback), port: .any)
        listener = try NWListener(using: params)
        try await withCheckedThrowingContinuation { (done: CheckedContinuation<Void, Error>) in
            let once = Once()
            listener.stateUpdateHandler = { [listener] state in
                switch state {
                case .ready: once.run { done.resume() }
                case let .failed(e): once.run { done.resume(throwing: e) }
                default: _ = listener
                }
            }
            listener.newConnectionHandler = { [weak self] conn in self?.accept(conn) }
            listener.start(queue: queue)
        }
        port = listener.port?.rawValue ?? 0
    }

    var url: URL { URL(string: "http://127.0.0.1:\(port)")! }

    func stop() {
        listener.cancel()
        lock.withLock { conns }.forEach { $0.cancel() }
    }

    /// Answers every request the home is holding (`holds`), late: a short 200.
    func answerHeld() {
        let waiting = lock.withLock { () -> [NWConnection] in
            defer { held = [] }
            return held
        }
        for conn in waiting {
            conn.send(content: Data("HTTP/1.1 200 X\r\nContent-Length: 4\r\nConnection: close\r\n\r\nlate".utf8), completion: .contentProcessed { _ in
                conn.send(content: nil, contentContext: .finalMessage, isComplete: true, completion: .idempotent)
            })
        }
    }

    /// The next answer for `path` (the last one given stays for every later request).
    func on(_ path: String, _ answer: Answer) { lock.withLock { answers[path, default: []].append(answer) } }

    var seen: [Seen] { lock.withLock { requests } }
    /// Body bytes handed to the network so far, over every request.
    var bytesWritten: Int { lock.withLock { written } }
    /// Requests whose caller went away before the answer was whole.
    var abandoned: Int { lock.withLock { givenUp } }
    /// Requests answered whole.
    var answered: Int { lock.withLock { whole } }

    /// The body's byte at `offset`: any slice can be checked without keeping the body.
    static func byte(at offset: Int) -> UInt8 { UInt8(truncatingIfNeeded: offset &* 31 &+ (offset >> 11)) }

    /// One whole period of `byte(at:)` (it repeats every 512 KB), made once: a body is cut
    /// from it, so serving one costs a copy, not a sum for each byte.
    private static let period: Data = {
        var d = Data(count: 1 << 19)
        d.withUnsafeMutableBytes { raw in
            let p = raw.bindMemory(to: UInt8.self)
            for o in 0..<(1 << 19) { p[o] = byte(at: o) }
        }
        return d
    }()

    static func body(_ range: Range<Int>) -> Data {
        var d = Data(capacity: range.count)
        var o = range.lowerBound
        while o < range.upperBound {
            let at = o & ((1 << 19) - 1)
            let n = min(range.upperBound - o, (1 << 19) - at)
            d.append(period[at..<at + n])
            o += n
        }
        return d
    }

    /// Whether `data` is the body's bytes from `offset` on.
    static func matches(_ data: Data, from offset: Int) -> Bool {
        data == body(offset..<offset + data.count)
    }

    private func accept(_ conn: NWConnection) {
        lock.withLock { conns.append(conn) }
        conn.start(queue: queue)
        readHead(conn, Data())
    }

    private func readHead(_ conn: NWConnection, _ sofar: Data) {
        conn.receive(minimumIncompleteLength: 1, maximumLength: 16 << 10) { [weak self] data, _, complete, error in
            guard let self, error == nil, let data, !data.isEmpty else { conn.cancel(); return }
            let buf = sofar + data
            guard buf.range(of: Data("\r\n\r\n".utf8)) != nil else {
                if complete { conn.cancel() } else { self.readHead(conn, buf) }
                return
            }
            guard let req = Request(buf) else { conn.cancel(); return }
            self.answer(conn, req)
        }
    }

    private func answer(_ conn: NWConnection, _ req: Request) {
        let path = String(req.target.split(separator: "?").first ?? "")
        let a = lock.withLock { () -> Answer in
            requests.append(Seen(method: req.method, target: req.target, headers: req.headers))
            guard var list = answers[path], !list.isEmpty else { return Answer(status: 404) }
            let first = list.removeFirst()
            if !list.isEmpty { answers[path] = list }
            return first
        }
        let done = Once()
        // the caller closing its side before the answer is whole: it gave up
        conn.receive(minimumIncompleteLength: 1, maximumLength: 1) { [weak self] _, _, _, _ in
            done.run { self?.lock.withLock { self?.givenUp += 1 } }
            conn.cancel()
        }
        if a.holds {
            lock.withLock { held.append(conn) }
            return
        }
        var range = 0..<a.length
        var status = a.status
        var head = ""
        if let r = req.headers["range"], r.hasPrefix("bytes="), a.status == 200 {
            let parts = r.dropFirst(6).split(separator: "-", omittingEmptySubsequences: false)
            let from = Int(parts.first ?? "") ?? 0
            let to = parts.count > 1 ? Int(parts[1]) ?? a.length - 1 : a.length - 1
            range = from..<min(to + 1, a.length)
            status = 206
            head += "Content-Range: bytes \(range.lowerBound)-\(range.upperBound - 1)/\(a.length)\r\n"
        }
        head = "HTTP/1.1 \(status) X\r\n" + head
        head += a.saysLength ? "Content-Length: \(range.count)\r\n" : "Transfer-Encoding: chunked\r\n"
        for (k, v) in a.headers { head += "\(k): \(v)\r\n" }
        head += "Connection: close\r\n\r\n"
        let whole = range, cutAt = a.cutAfter.map { range.lowerBound + $0 }, chunked = !a.saysLength
        conn.send(content: Data(head.utf8), completion: .contentProcessed { [weak self] error in
            guard let self, error == nil else { return }
            if req.method == "HEAD" { self.end(conn, done) } else { self.write(conn, whole, done, cutAt: cutAt, chunked: chunked) }
        })
    }

    private func write(_ conn: NWConnection, _ rest: Range<Int>, _ done: Once, cutAt: Int? = nil, chunked: Bool = false) {
        guard !rest.isEmpty else {
            if chunked { conn.send(content: Data("0\r\n\r\n".utf8), completion: .idempotent) }
            end(conn, done)
            return
        }
        if let cutAt, rest.lowerBound >= cutAt {
            // the home fails here: no orderly end
            done.run {}
            conn.forceCancel()
            return
        }
        let n = min(64 << 10, rest.count, cutAt.map { $0 - rest.lowerBound } ?? Int.max)
        let piece = rest.lowerBound..<rest.lowerBound + n
        var out = Self.body(piece)
        if chunked { out = Data("\(String(n, radix: 16))\r\n".utf8) + out + Data("\r\n".utf8) }
        conn.send(content: out, completion: .contentProcessed { [weak self] error in
            guard let self else { return }
            guard error == nil else {
                done.run { self.lock.withLock { self.givenUp += 1 } }
                return
            }
            self.lock.withLock { self.written += n }
            self.write(conn, piece.upperBound..<rest.upperBound, done, cutAt: cutAt, chunked: chunked)
        })
    }

    private func end(_ conn: NWConnection, _ done: Once) {
        done.run { lock.withLock { whole += 1 } }
        conn.send(content: nil, contentContext: .finalMessage, isComplete: true, completion: .idempotent)
    }
}

/// A player's connection by hand: it can stop reading, or go away, when a test says.
final class RawPlayer: @unchecked Sendable {
    private let conn: NWConnection
    private let queue = DispatchQueue(label: "test.raw-player")

    init(port: UInt16) {
        conn = NWConnection(host: "127.0.0.1", port: NWEndpoint.Port(rawValue: port)!, using: .tcp)
        conn.start(queue: queue)
    }

    func ask(_ target: String, method: String = "GET") {
        conn.send(content: Data("\(method) \(target) HTTP/1.1\r\nHost: 127.0.0.1\r\n\r\n".utf8), completion: .idempotent)
    }

    /// Reads up to `count` bytes (fewer when the other side closed first).
    func read(_ count: Int) async -> Data {
        var got = Data()
        while got.count < count {
            let more: Data? = await withCheckedContinuation { done in
                conn.receive(minimumIncompleteLength: 1, maximumLength: count - got.count) { data, _, _, error in
                    done.resume(returning: error == nil ? data : nil)
                }
            }
            guard let more, !more.isEmpty else { break }
            got += more
        }
        return got
    }

    /// Closes this side only (a FIN after the request): the answer is still read.
    func finishSending() {
        conn.send(content: nil, contentContext: .finalMessage, isComplete: true, completion: .idempotent)
    }

    /// Reads to the end of the connection: what came, and whether it ended in an error
    /// (a reset) and not an orderly close.
    func readToEnd() async -> (data: Data, failed: Bool) {
        var got = Data()
        while true {
            let (more, complete, failed): (Data?, Bool, Bool) = await withCheckedContinuation { done in
                conn.receive(minimumIncompleteLength: 1, maximumLength: 1 << 20) { data, _, complete, error in
                    done.resume(returning: (data, complete, error != nil))
                }
            }
            if let more { got += more }
            if failed { return (got, true) }
            if complete || more?.isEmpty != false { return (got, false) }
        }
    }

    func close() { conn.cancel() }
}

/// Splits a raw HTTP/1.1 response into its head and what follows.
func split(_ response: Data) -> (head: String, body: Data) {
    guard let r = response.range(of: Data("\r\n\r\n".utf8)) else { return (String(decoding: response, as: UTF8.self), Data()) }
    return (String(decoding: response[..<r.lowerBound], as: UTF8.self), Data(response[r.upperBound...]))
}

/// Waits until `value` has stopped changing (the same over 300 ms), at most `seconds`; the
/// value it settled at, nil when it never did.
func settled(_ seconds: Double = 10, _ value: @Sendable () -> Int) async -> Int? {
    let until = ContinuousClock.now + .seconds(seconds)
    var last = value(), same = 0
    while ContinuousClock.now < until {
        try? await Task.sleep(for: .milliseconds(50))
        let now = value()
        same = now == last ? same + 1 : 0
        last = now
        if same >= 6 { return now }
    }
    return nil
}

/// Waits (at most `seconds`) until `ok` holds; false when it never did.
func eventually(_ seconds: Double = 5, _ ok: @Sendable () -> Bool) async -> Bool {
    let until = ContinuousClock.now + .seconds(seconds)
    while ContinuousClock.now < until {
        if ok() { return true }
        try? await Task.sleep(for: .milliseconds(20))
    }
    return ok()
}

/// The stream proxy's copy (the body handed on in the chunks it arrives in, the home held back
/// while the player reads slowly, the home's request ended when the player goes).
@Suite(.serialized) struct ProxyCopy {
    final class Waits: @unchecked Sendable {
        private let lock = NSLock()
        private var list: [Duration] = []
        func add(_ d: Duration) { lock.withLock { list.append(d) } }
        var all: [Duration] { lock.withLock { list } }
    }

    /// A proxy for `home`, signed in with a token that has not expired.
    static func proxy(_ home: LoopbackHome, headPatience: Duration = StreamProxy.headPatience,
                      sleep: @escaping @Sendable (Duration) async throws -> Void = { try await Task.sleep(for: $0) }) throws -> StreamProxy {
        let claims = Data(#"{"exp":4102444800}"#.utf8).base64EncodedString()
            .replacingOccurrences(of: "+", with: "-").replacingOccurrences(of: "/", with: "_").replacingOccurrences(of: "=", with: "")
        let base = home.url.absoluteString
        let secrets = FakeSecrets(["\(base)#access": "h.\(claims).s", "\(base)#refresh": "r1"])
        let tokens = TokenStore(service: "test.\(UUID().uuidString)", home: home.url, primary: secrets, legacy: nil)
        // a session with a delegate of its own, as the app's (`PinnedTrust.session`) has
        let config = URLSessionConfiguration.ephemeral
        config.httpMaximumConnectionsPerHost = perHost
        let session = URLSession(configuration: config, delegate: PinnedTrust.shared, delegateQueue: nil)
        return try StreamProxy(client: APIClient(home: home.url, tokens: tokens, session: session), session: session,
                               headPatience: headPatience, sleep: sleep)
    }

    /// The most connections the proxy's session opens to the home at once.
    static let perHost = 6

    static func target(_ proxy: StreamProxy, _ path: String) -> String { "/\(proxy.secret)\(path)" }

    @Test func aLargeBodyArrivesWholeAndInOrder() async throws {
        let home = try await LoopbackHome()
        defer { home.stop() }
        let size = 24 << 20
        home.on("/stream/abc/seg-1.ts", .init(headers: ["Content-Type": "video/mp2t", "Cache-Control": "no-store", "ETag": "\"e1\""], length: size))
        let proxy = try Self.proxy(home)
        try await proxy.start()
        defer { proxy.stop() }
        let local = try #require(proxy.local(home.url.appending(path: "stream/abc/seg-1.ts")))
        let (data, response) = try await URLSession.shared.data(from: local)
        let http = try #require(response as? HTTPURLResponse)
        #expect(http.statusCode == 200 && data.count == size)
        #expect(LoopbackHome.matches(data, from: 0), "every byte, in order")
        #expect(http.value(forHTTPHeaderField: "Content-Type") == "video/mp2t")
        #expect(http.value(forHTTPHeaderField: "Content-Length") == "\(size)")
        #expect(http.value(forHTTPHeaderField: "Cache-Control") == "no-store" && http.value(forHTTPHeaderField: "ETag") == "\"e1\"")
        #expect(home.seen.last?.headers["authorization"]?.hasPrefix("Bearer h.") == true, "the token goes on every request")
        #expect(await eventually { proxy.serving == 0 }, "nothing is kept once the answer is whole")
    }

    /// A body whose length the home does not say goes to the player in chunks, whole.
    @Test func aBodyWithNoLengthGoesInChunks() async throws {
        let home = try await LoopbackHome()
        defer { home.stop() }
        home.on("/stream/abc/live.m3u8", .init(length: 300_000, saysLength: false))
        let proxy = try Self.proxy(home)
        try await proxy.start()
        defer { proxy.stop() }
        let (data, _) = try await URLSession.shared.data(from: try #require(proxy.local(home.url.appending(path: "stream/abc/live.m3u8"))))
        #expect(data.count == 300_000 && LoopbackHome.matches(data, from: 0))
        // as it goes to the player: in chunks, ended by the last chunk
        let player = RawPlayer(port: try #require(proxy.port))
        defer { player.close() }
        player.ask(Self.target(proxy, "/stream/abc/live.m3u8"))
        let raw = await player.readToEnd()
        #expect(split(raw.data).head.contains("Transfer-Encoding: chunked") && raw.data.suffix(5) == Data("0\r\n\r\n".utf8))
    }

    @Test func aRangeAndAHeadPassThrough() async throws {
        let home = try await LoopbackHome()
        defer { home.stop() }
        home.on("/stream/abc/file.mp4", .init(headers: ["Accept-Ranges": "bytes", "Last-Modified": "Sat, 14 Nov 2026 10:00:00 GMT"], length: 5 << 20))
        let proxy = try Self.proxy(home)
        try await proxy.start()
        defer { proxy.stop() }
        let local = try #require(proxy.local(home.url.appending(path: "stream/abc/file.mp4")))

        var ranged = URLRequest(url: local)
        ranged.setValue("bytes=1000000-1999999", forHTTPHeaderField: "Range")
        let (part, r1) = try await URLSession.shared.data(for: ranged)
        let partial = try #require(r1 as? HTTPURLResponse)
        #expect(partial.statusCode == 206 && part.count == 1_000_000)
        #expect(LoopbackHome.matches(part, from: 1_000_000))
        #expect(partial.value(forHTTPHeaderField: "Content-Range") == "bytes 1000000-1999999/\(5 << 20)")
        #expect(partial.value(forHTTPHeaderField: "Accept-Ranges") == "bytes")
        #expect(home.seen.last?.headers["range"] == "bytes=1000000-1999999")

        var head = URLRequest(url: local)
        head.httpMethod = "HEAD"
        let (none, r2) = try await URLSession.shared.data(for: head)
        let answered = try #require(r2 as? HTTPURLResponse)
        #expect(answered.statusCode == 200 && none.isEmpty && home.seen.last?.method == "HEAD")
        #expect(answered.value(forHTTPHeaderField: "Content-Length") == "\(5 << 20)")
        #expect(answered.value(forHTTPHeaderField: "Last-Modified") == "Sat, 14 Nov 2026 10:00:00 GMT")
        #expect(await eventually { proxy.serving == 0 })
    }

    /// The body is kept only up to the ceiling: a request nobody reads from holds the home back.
    @Test func aBodyNobodyTakesIsKeptOnlyToTheCeiling() async throws {
        let home = try await LoopbackHome()
        defer { home.stop() }
        let size = 96 << 20
        home.on("/stream/abc/big.mp4", .init(length: size))
        var r = URLRequest(url: home.url.appending(path: "stream/abc/big.mp4"))
        r.httpMethod = "GET"
        let body = UpstreamBody(r, session: URLSession(configuration: .ephemeral))
        #expect(try await body.response()?.statusCode == 200)
        // nobody asks for the body: the home stops writing once the mark and the sockets are full
        let wrote = try #require(await settled { home.bytesWritten }, "the home never stopped writing")
        #expect(wrote < size / 2, "the home wrote \(wrote) of \(size) bytes to a reader that took none")
        #expect(body.peak >= UpstreamBody.highWater && body.peak <= UpstreamBody.ceiling, "kept \(body.peak) bytes")
        // and then every byte still comes, in order
        var offset = 0
        while let chunk = try await body.next() {
            #expect(LoopbackHome.matches(chunk, from: offset))
            offset += chunk.count
        }
        #expect(offset == size)
        #expect(body.peak <= UpstreamBody.ceiling, "kept \(body.peak) bytes while read")
    }

    /// A player that reads nothing (paused, its buffer full) holds the home back through the
    /// proxy: memory does not grow with the file.
    @Test func aPlayerThatReadsNothingHoldsTheHomeBack() async throws {
        let home = try await LoopbackHome()
        defer { home.stop() }
        let size = 256 << 20
        home.on("/stream/abc/big.mp4", .init(length: size))
        let proxy = try Self.proxy(home)
        try await proxy.start()
        defer { proxy.stop() }
        let player = RawPlayer(port: try #require(proxy.port))
        defer { player.close() }
        player.ask(Self.target(proxy, "/stream/abc/big.mp4"))
        let first = await player.read(4096)
        #expect(String(decoding: first.prefix(12), as: UTF8.self) == "HTTP/1.1 200")
        let wrote = try #require(await settled { home.bytesWritten }, "the home never stopped writing")
        #expect(wrote < size / 4, "the home wrote \(wrote) of \(size) bytes while the player read 4 KB")
        #expect(proxy.serving == 1, "and the player keeps its connection")
    }

    /// A player reading slowly, far longer than anything the proxy times, is never cut: the
    /// reviewer's case (64 KB every 250 ms was closed mid-body by a write timer).
    @Test func aSlowReaderIsNeverCut() async throws {
        let home = try await LoopbackHome()
        defer { home.stop() }
        let size = 48 << 20
        home.on("/stream/abc/big.mp4", .init(length: size))
        // the one timer (a request's head) is 200 ms here: the slow read lasts fifteen of them
        let proxy = try Self.proxy(home, headPatience: .milliseconds(200))
        try await proxy.start()
        defer { proxy.stop() }
        let player = RawPlayer(port: try #require(proxy.port))
        defer { player.close() }
        player.ask(Self.target(proxy, "/stream/abc/big.mp4"))
        var got = Data()
        // under 100 KB a second: 16 KB every 200 ms, for 3 s
        for _ in 0..<15 {
            got += await player.read(16 << 10)
            try await Task.sleep(for: .milliseconds(200))
        }
        #expect(proxy.serving == 1, "still served while it reads slowly")
        // then the rest, to the end
        let (rest, failed) = await player.readToEnd()
        got += rest
        let (head, body) = split(got)
        #expect(!failed && head.hasPrefix("HTTP/1.1 200"))
        #expect(body.count == size, "\(body.count) of \(size) bytes")
        #expect(LoopbackHome.matches(body, from: 0), "whole and in order")
    }

    /// A paused player (it takes nothing, far longer than anything the proxy times) and then
    /// resumes: the rest comes on the same connection, intact.
    @Test func aPausedPlayerResumesOnTheSameConnection() async throws {
        let home = try await LoopbackHome()
        defer { home.stop() }
        let size = 48 << 20
        home.on("/stream/abc/big.mp4", .init(length: size))
        let proxy = try Self.proxy(home, headPatience: .milliseconds(200))
        try await proxy.start()
        defer { proxy.stop() }
        let player = RawPlayer(port: try #require(proxy.port))
        defer { player.close() }
        player.ask(Self.target(proxy, "/stream/abc/big.mp4"))
        var got = await player.read(100_000)
        // paused: the home stops writing, and nothing happens for ten head-timers
        _ = try #require(await settled { home.bytesWritten }, "the home never stopped writing")
        try await Task.sleep(for: .seconds(2))
        #expect(proxy.serving == 1 && home.abandoned == 0, "a paused player is not dropped")
        let (rest, failed) = await player.readToEnd()
        got += rest
        let (head, body) = split(got)
        #expect(!failed && head.hasPrefix("HTTP/1.1 200"))
        #expect(body.count == size && LoopbackHome.matches(body, from: 0), "\(body.count) of \(size) bytes")
        #expect(home.seen.count == 1, "one request: nothing was asked again")
    }

    /// A client that connects and never sends a whole request is closed.
    @Test func aClientThatNeverAsksIsClosed() async throws {
        let home = try await LoopbackHome()
        defer { home.stop() }
        let proxy = try Self.proxy(home, headPatience: .milliseconds(300))
        try await proxy.start()
        defer { proxy.stop() }
        let silent = RawPlayer(port: try #require(proxy.port))
        defer { silent.close() }
        #expect(await eventually(2) { proxy.serving == 1 })
        #expect(await eventually(3) { proxy.serving == 0 }, "serving \(proxy.serving)")
        #expect(home.seen.isEmpty)
    }

    /// On the network a receiver can vanish without closing: the LAN proxy's connections use
    /// TCP keepalive, the loopback's do not (the system closes a gone player's sockets).
    @Test func onlyTheLANProxyUsesKeepalive() throws {
        let lan = try #require(StreamProxy.parameters(for: .lan(host: "192.0.2.10")).defaultProtocolStack.transportProtocol as? NWProtocolTCP.Options)
        #expect(lan.enableKeepalive && lan.keepaliveIdle == 30 && lan.keepaliveInterval == 10 && lan.keepaliveCount == 3)
        #expect(lan.connectionDropTime == 120)
        let loop = try #require(StreamProxy.parameters(for: .loopback).defaultProtocolStack.transportProtocol as? NWProtocolTCP.Options)
        #expect(!loop.enableKeepalive && loop.connectionDropTime == 0)
        #expect(StreamProxy.slice <= 64 << 10, "the player is written to in bounded slices")
    }

    /// A client that sends its request and closes its sending side still gets its answer.
    @Test func aClientThatHalfClosesStillGetsItsAnswer() async throws {
        let home = try await LoopbackHome()
        defer { home.stop() }
        let size = 6 << 20
        home.on("/stream/abc/seg-2.ts", .init(length: size))
        let proxy = try Self.proxy(home)
        try await proxy.start()
        defer { proxy.stop() }
        let player = RawPlayer(port: try #require(proxy.port))
        defer { player.close() }
        player.ask(Self.target(proxy, "/stream/abc/seg-2.ts"))
        player.finishSending()
        let (all, failed) = await player.readToEnd()
        let (head, body) = split(all)
        #expect(!failed && head.hasPrefix("HTTP/1.1 200"), "\(head.prefix(40))")
        #expect(body.count == size && LoopbackHome.matches(body, from: 0), "\(body.count) of \(size) bytes")
        #expect(await eventually { proxy.serving == 0 })
    }

    /// The player closing mid-body (a channel change, a seek) ends the home's request at once.
    @Test func thePlayerLeavingEndsTheHomesRequest() async throws {
        let home = try await LoopbackHome()
        defer { home.stop() }
        home.on("/stream/abc/big.mp4", .init(length: 256 << 20))
        let proxy = try Self.proxy(home)
        try await proxy.start()
        defer { proxy.stop() }
        let player = RawPlayer(port: try #require(proxy.port))
        player.ask(Self.target(proxy, "/stream/abc/big.mp4"))
        _ = await player.read(100_000)
        #expect(proxy.serving == 1 && home.abandoned == 0)
        player.close()
        #expect(await eventually(3) { home.abandoned == 1 }, "the home's request was not ended")
        #expect(await eventually(3) { proxy.serving == 0 })
    }

    /// Requests the player abandons before the home has answered end when the home answers
    /// or gives up (a closed sending side alone is not "left": see the half-close test), and
    /// then all are released.
    @Test func requestsAbandonedBeforeTheHomeAnswersAreReleasedWhenItDoes() async throws {
        let home = try await LoopbackHome()
        defer { home.stop() }
        home.on("/stream/abc/slow.m3u8", .init(holds: true))
        let proxy = try Self.proxy(home)
        try await proxy.start()
        defer { proxy.stop() }
        let port = try #require(proxy.port)
        for round in 1...4 {
            let players = (0..<5).map { _ in RawPlayer(port: port) }
            players.forEach { $0.ask(Self.target(proxy, "/stream/abc/slow.m3u8")) }
            #expect(await eventually { proxy.serving == 5 && home.seen.count == 5 * round }, "serving \(proxy.serving), the home saw \(home.seen.count)")
            players.forEach { $0.close() }
            home.answerHeld()
            #expect(await eventually { proxy.serving == 0 }, "round \(round): serving \(proxy.serving)")
        }
    }

    /// More requests at once than a player makes are refused, not queued; a place that is
    /// given back is served again.
    @Test func tooManyAtOnceAreRefusedAndAPlaceGivenBackIsServed() async throws {
        let home = try await LoopbackHome()
        defer { home.stop() }
        home.on("/stream/abc/big.mp4", .init(length: 64 << 20))
        home.on("/stream/abc/small.ts", .init(length: 1000))
        let proxy = try Self.proxy(home)
        try await proxy.start()
        defer { proxy.stop() }
        let port = try #require(proxy.port)
        // every place taken by a player that reads nothing
        let held = (0..<StreamProxy.mostAtOnce).map { _ in RawPlayer(port: port) }
        defer { held.forEach { $0.close() } }
        held.forEach { $0.ask(Self.target(proxy, "/stream/abc/big.mp4")) }
        #expect(await eventually { proxy.serving == StreamProxy.mostAtOnce }, "serving \(proxy.serving)")
        let extra = RawPlayer(port: port)
        extra.ask(Self.target(proxy, "/stream/abc/small.ts"))
        #expect(String(decoding: await extra.read(12), as: UTF8.self) == "HTTP/1.1 503")
        extra.close()
        #expect(!home.seen.contains { $0.target.hasSuffix("small.ts") }, "the home is not asked for a refused request")
        #expect(await eventually { proxy.serving == StreamProxy.mostAtOnce }, "the refused one gives its place back")

        // the players go: every place comes back, and the next request is served
        held.forEach { $0.close() }
        #expect(await eventually(10) { proxy.serving == 0 }, "serving \(proxy.serving)")
        let (data, response) = try await URLSession.shared.data(from: try #require(proxy.local(home.url.appending(path: "stream/abc/small.ts"))))
        #expect((response as? HTTPURLResponse)?.statusCode == 200 && data.count == 1000)
    }

    /// Stopping the proxy (the player screen closed) ends what it was serving.
    @Test func stoppingEndsEveryRequest() async throws {
        let home = try await LoopbackHome()
        defer { home.stop() }
        home.on("/stream/abc/big.mp4", .init(length: 256 << 20))
        home.on("/stream/abc/slow.m3u8", .init(holds: true))
        let proxy = try Self.proxy(home)
        try await proxy.start()
        let port = try #require(proxy.port)
        let a = RawPlayer(port: port), b = RawPlayer(port: port)
        defer { a.close(); b.close() }
        a.ask(Self.target(proxy, "/stream/abc/big.mp4"))
        b.ask(Self.target(proxy, "/stream/abc/slow.m3u8"))
        _ = await a.read(4096)
        #expect(await eventually { home.seen.count == 2 })
        proxy.stop()
        #expect(await eventually(3) { home.abandoned == 2 && proxy.serving == 0 }, "abandoned \(home.abandoned)")
    }

    /// A redirect to another origin is followed without the token.
    @Test func aRedirectElsewhereLosesTheToken() async throws {
        let home = try await LoopbackHome()
        let other = try await LoopbackHome()
        defer { home.stop(); other.stop() }
        home.on("/stream/abc/moved.ts", .init(status: 302, headers: ["Location": "\(other.url.absoluteString)/stream/abc/there.ts"]))
        home.on("/stream/abc/same.ts", .init(status: 302, headers: ["Location": "/stream/abc/here.ts"]))
        home.on("/stream/abc/here.ts", .init(length: 1000))
        other.on("/stream/abc/there.ts", .init(length: 1000))
        let proxy = try Self.proxy(home)
        try await proxy.start()
        defer { proxy.stop() }
        let (a, _) = try await URLSession.shared.data(from: try #require(proxy.local(home.url.appending(path: "stream/abc/moved.ts"))))
        #expect(a.count == 1000 && other.seen.count == 1)
        #expect(other.seen.first?.headers["authorization"] == nil, "the token went to another origin")
        let (b, _) = try await URLSession.shared.data(from: try #require(proxy.local(home.url.appending(path: "stream/abc/same.ts"))))
        #expect(b.count == 1000 && home.seen.last?.target == "/stream/abc/here.ts", "a redirect within the home is followed")
    }

    /// The home's 429 (a restricted account's media read too far ahead) is waited out and
    /// asked again, over a real connection and through the chunked copy; the player gets the
    /// media, never the 429.
    @Test func aPacedStreamIsWaitedOutOverARealConnection() async throws {
        let home = try await LoopbackHome()
        defer { home.stop() }
        let size = 3 << 20
        home.on("/stream/abc/seg-7.m4s", .init(status: 429, headers: ["Retry-After": "30"], length: 20))
        home.on("/stream/abc/seg-7.m4s", .init(status: 429, length: 20))
        home.on("/stream/abc/seg-7.m4s", .init(length: size))
        let waits = Waits()
        let proxy = try Self.proxy(home) { waits.add($0) }
        try await proxy.start()
        defer { proxy.stop() }
        let (data, response) = try await URLSession.shared.data(from: try #require(proxy.local(home.url.appending(path: "stream/abc/seg-7.m4s"))))
        #expect((response as? HTTPURLResponse)?.statusCode == 200 && data.count == size && LoopbackHome.matches(data, from: 0))
        #expect(waits.all == [.seconds(25), .seconds(2)], "as the home said, 25 s at most; 2 s when it named no time")
        let asks = home.seen.filter { $0.target.hasPrefix("/stream/abc/seg-7.m4s") }
        #expect(asks.count == 3 && asks.allSatisfy { $0.headers["authorization"]?.hasPrefix("Bearer ") == true }, "a token on each ask")
    }

    /// The home failing mid-body is an error for the player, never a body that looks whole:
    /// with a length the body is short of it; with none it goes in chunks and its last chunk
    /// never comes.
    @Test func aBodyCutShortIsAnErrorForThePlayer() async throws {
        let home = try await LoopbackHome()
        defer { home.stop() }
        home.on("/stream/abc/known.ts", .init(length: 8 << 20, cutAfter: 1 << 20))
        home.on("/stream/abc/unknown.ts", .init(length: 8 << 20, saysLength: false, cutAfter: 1 << 20))
        let proxy = try Self.proxy(home)
        try await proxy.start()
        defer { proxy.stop() }
        let port = try #require(proxy.port)

        let known = RawPlayer(port: port)
        defer { known.close() }
        known.ask(Self.target(proxy, "/stream/abc/known.ts"))
        let a = await known.readToEnd()
        let first = split(a.data)
        #expect(first.head.contains("Content-Length: \(8 << 20)") && first.body.count < 8 << 20, "short of its length: \(first.body.count)")
        #expect(!first.body.contains(Data("HTTP/1.1".utf8)), "no second status line inside the body")

        let unknown = RawPlayer(port: port)
        defer { unknown.close() }
        unknown.ask(Self.target(proxy, "/stream/abc/unknown.ts"))
        let b = await unknown.readToEnd()
        let second = split(b.data)
        #expect(!second.head.contains("Content-Length") && second.head.contains("Transfer-Encoding: chunked"))
        #expect(b.data.suffix(5) != Data("0\r\n\r\n".utf8), "a body that was cut short must not end with the last chunk")
        #expect(await eventually { proxy.serving == 0 })

        // and URLSession, as a player would read it, fails
        await #expect(throws: (any Error).self) {
            _ = try await URLSession.shared.data(from: try #require(proxy.local(home.url.appending(path: "stream/abc/unknown.ts"))))
        }
    }

    /// A home that cannot be reached is a 502 for the player, not a reset.
    @Test func aHomeThatFailsBeforeAnsweringIsABadGateway() async throws {
        let home = try await LoopbackHome()
        let proxy = try Self.proxy(home)
        try await proxy.start()
        defer { proxy.stop() }
        let local = try #require(proxy.local(home.url.appending(path: "stream/abc/x.ts")))
        home.stop()
        let (_, response) = try await URLSession.shared.data(from: local)
        #expect((response as? HTTPURLResponse)?.statusCode == 502)
    }
}

/// What the copy costs, measured: not part of the normal run (hundreds of megabytes, several
/// seconds). Run it with
/// `MAKTABA_PROXY_BENCH=1 swift test -c release --filter ProxyCopyCost` in apps/apple/MaktabaKit.
@Suite(.serialized) struct ProxyCopyCost {
    static let on = ProcessInfo.processInfo.environment["MAKTABA_PROXY_BENCH"] != nil
    static let size = (Int(ProcessInfo.processInfo.environment["MAKTABA_PROXY_BENCH_MB"] ?? "") ?? 256) << 20

    /// This process's CPU time (user and system), in seconds.
    static func cpu() -> Double {
        var u = rusage()
        getrusage(RUSAGE_SELF, &u)
        return Double(u.ru_utime.tv_sec + u.ru_stime.tv_sec) + Double(u.ru_utime.tv_usec + u.ru_stime.tv_usec) / 1e6
    }

    static func measure(_ name: String, bytes: Int, _ work: () async throws -> Void) async rethrows {
        let c0 = cpu(), t0 = ContinuousClock.now
        try await work()
        let c = cpu() - c0, t = ContinuousClock.now - t0
        let wall = Double(t.components.seconds) + Double(t.components.attoseconds) / 1e18
        let mb = Double(bytes) / Double(1 << 20)
        print(String(format: "BENCH %@: %.0f MB in %.2f s = %.0f MB/s; CPU %.2f s = %.2f ms per MB",
                     name, mb, wall, mb / wall, c, c * 1000 / mb))
    }

    @Test(.enabled(if: on)) func perByteAgainstChunks() async throws {
        let home = try await LoopbackHome()
        defer { home.stop() }
        let size = Self.size
        home.on("/stream/abc/big.ts", .init(length: size))
        let url = home.url.appending(path: "stream/abc/big.ts")
        let session = URLSession(configuration: .ephemeral)

        // the home's own cost and URLSession's, with no copy at all (the floor of the two below)
        try await Self.measure("floor: chunks counted, not copied", bytes: size) {
            let body = UpstreamBody(URLRequest(url: url), session: session)
            _ = try await body.response()
            var n = 0
            while let chunk = try await body.next() { n += chunk.count }
            #expect(n == size)
        }
        // as the proxy copied before: one async-sequence step and one append a byte
        try await Self.measure("per byte (URLSession.AsyncBytes into 64 KB)", bytes: size) {
            let (bytes, _) = try await session.bytes(for: URLRequest(url: url))
            var chunk = Data(), n = 0
            chunk.reserveCapacity(64 << 10)
            for try await b in bytes {
                chunk.append(b)
                if chunk.count >= 64 << 10 {
                    n += chunk.count
                    chunk.removeAll(keepingCapacity: true)
                }
            }
            #expect(n + chunk.count == size)
        }
        // through the whole proxy as it is now, read by a URLSession client
        let proxy = try ProxyCopy.proxy(home)
        try await proxy.start()
        defer { proxy.stop() }
        let local = try #require(proxy.local(url))
        try await Self.measure("whole proxy, chunks (home + proxy + reader)", bytes: size) {
            let body = UpstreamBody(URLRequest(url: local), session: session)
            _ = try await body.response()
            var n = 0
            while let chunk = try await body.next() { n += chunk.count }
            #expect(n == size)
        }
        // a connection for each request: 500 small playlists, one after another
        home.on("/stream/abc/live.m3u8", .init(headers: ["Content-Type": "application/vnd.apple.mpegurl"], length: 1200))
        let playlist = try #require(proxy.local(home.url.appending(path: "stream/abc/live.m3u8")))
        let c0 = Self.cpu(), t0 = ContinuousClock.now
        for _ in 0..<500 { _ = try await session.data(from: playlist) }
        let t = ContinuousClock.now - t0
        let wall = Double(t.components.seconds) + Double(t.components.attoseconds) / 1e18
        print(String(format: "BENCH 500 playlist requests, a connection each: %.2f ms each, CPU %.2f ms each",
                     wall * 2, (Self.cpu() - c0) * 2))
    }
}
