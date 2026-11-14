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
        /// Whether the length is said (`Content-Length`); without it the body ends at the close.
        var saysLength = true
        /// Never answers: the request stays open until the caller lets go.
        var holds = false
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
        if a.holds { return }
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
        if a.saysLength { head += "Content-Length: \(range.count)\r\n" }
        for (k, v) in a.headers { head += "\(k): \(v)\r\n" }
        head += "Connection: close\r\n\r\n"
        conn.send(content: Data(head.utf8), completion: .contentProcessed { [weak self] error in
            guard let self, error == nil else { return }
            if req.method == "HEAD" { self.end(conn, done) } else { self.write(conn, range, done) }
        })
    }

    private func write(_ conn: NWConnection, _ rest: Range<Int>, _ done: Once) {
        guard !rest.isEmpty else { end(conn, done); return }
        let n = min(64 << 10, rest.count)
        let piece = rest.lowerBound..<rest.lowerBound + n
        conn.send(content: Self.body(piece), completion: .contentProcessed { [weak self] error in
            guard let self else { return }
            guard error == nil else {
                done.run { self.lock.withLock { self.givenUp += 1 } }
                return
            }
            self.lock.withLock { self.written += n }
            self.write(conn, piece.upperBound..<rest.upperBound, done)
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

    func close() { conn.cancel() }
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
    /// A proxy for `home`, signed in with a token that has not expired.
    static func proxy(_ home: LoopbackHome, patience: Duration = StreamProxy.patience) throws -> StreamProxy {
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
                               patience: patience) { try await Task.sleep(for: $0) }
    }

    /// The connections the proxy's session opens to the home at once (URLSession's own default).
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

    /// A body whose length the home does not say ends where the home closes.
    @Test func aBodyWithNoLengthEndsAtTheClose() async throws {
        let home = try await LoopbackHome()
        defer { home.stop() }
        home.on("/stream/abc/live.m3u8", .init(length: 300_000, saysLength: false))
        let proxy = try Self.proxy(home)
        try await proxy.start()
        defer { proxy.stop() }
        let (data, _) = try await URLSession.shared.data(from: try #require(proxy.local(home.url.appending(path: "stream/abc/live.m3u8"))))
        #expect(data.count == 300_000 && LoopbackHome.matches(data, from: 0))
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

    /// The body is kept only up to the mark: a request nobody reads from holds the home back.
    @Test func aBodyNobodyTakesIsKeptOnlyToTheMark() async throws {
        let home = try await LoopbackHome()
        defer { home.stop() }
        let size = 96 << 20
        home.on("/stream/abc/big.mp4", .init(length: size))
        var r = URLRequest(url: home.url.appending(path: "stream/abc/big.mp4"))
        r.httpMethod = "GET"
        let body = UpstreamBody(r, session: URLSession(configuration: .ephemeral))
        #expect(try await body.response()?.statusCode == 200)
        // nobody asks for the body for a second: the home may write what the sockets take, no more
        try await Task.sleep(for: .seconds(1))
        let kept = body.peak, wrote = home.bytesWritten
        #expect(kept <= 4 * UpstreamBody.highWater, "kept \(kept) bytes")
        #expect(wrote <= 32 << 20, "the home wrote \(wrote) of \(size) bytes to a reader that took none")
        // and then every byte still comes, in order
        var offset = 0
        while let chunk = try await body.next() {
            #expect(LoopbackHome.matches(chunk, from: offset))
            offset += chunk.count
        }
        #expect(offset == size)
        #expect(body.peak <= 4 * UpstreamBody.highWater, "kept \(body.peak) bytes while read")
    }

    /// A player that reads nothing (paused, its buffer full) holds the home back through the
    /// proxy: memory does not grow with the file.
    @Test func aPlayerThatReadsSlowlyHoldsTheHomeBack() async throws {
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
        try await Task.sleep(for: .seconds(1))
        let wrote = home.bytesWritten
        #expect(wrote <= 48 << 20, "the home wrote \(wrote) of \(size) bytes while the player read 4 KB")
        try await Task.sleep(for: .milliseconds(500))
        #expect(home.bytesWritten - wrote <= 1 << 20, "and it stays held back")
    }

    /// The player closing (a channel change, a seek) ends the home's request at once.
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
        #expect(await eventually(2) { home.abandoned == 1 }, "the home's request was not ended")
        #expect(await eventually(2) { proxy.serving == 0 })
    }

    /// A player that stops reading without closing is given up after `patience`.
    @Test func aPlayerThatTakesNothingIsGivenUp() async throws {
        let home = try await LoopbackHome()
        defer { home.stop() }
        home.on("/stream/abc/big.mp4", .init(length: 256 << 20))
        let proxy = try Self.proxy(home, patience: .milliseconds(400))
        try await proxy.start()
        defer { proxy.stop() }
        let player = RawPlayer(port: try #require(proxy.port))
        defer { player.close() }
        player.ask(Self.target(proxy, "/stream/abc/big.mp4"))
        _ = await player.read(4096)
        #expect(await eventually(3) { home.abandoned == 1 && proxy.serving == 0 }, "serving \(proxy.serving), abandoned \(home.abandoned)")

        // one that connects and never asks is given up too
        let silent = RawPlayer(port: try #require(proxy.port))
        defer { silent.close() }
        #expect(await eventually(1) { proxy.serving == 1 })
        #expect(await eventually(3) { proxy.serving == 0 })
    }

    /// Requests the player abandons while the home has not answered (a slow home, a player
    /// that asks again) are all released: nothing piles up.
    @Test func abandonedRequestsAreAllReleased() async throws {
        let home = try await LoopbackHome()
        defer { home.stop() }
        home.on("/stream/abc/slow.m3u8", .init(holds: true))
        let proxy = try Self.proxy(home)
        try await proxy.start()
        defer { proxy.stop() }
        let port = try #require(proxy.port)
        for round in 1...6 {
            // five at a time: the session opens at most `perHost` connections to the home
            let players = (0..<5).map { _ in RawPlayer(port: port) }
            players.forEach { $0.ask(Self.target(proxy, "/stream/abc/slow.m3u8")) }
            #expect(await eventually { proxy.serving == 5 && home.seen.count == 5 * round }, "serving \(proxy.serving)")
            players.forEach { $0.close() }
            #expect(await eventually { proxy.serving == 0 && home.abandoned == 5 * round },
                    "round \(round): serving \(proxy.serving), abandoned \(home.abandoned)")
        }
    }

    /// More requests at once than a player makes are refused, not queued.
    @Test func tooManyAtOnceAreRefused() async throws {
        let home = try await LoopbackHome()
        defer { home.stop() }
        home.on("/stream/abc/slow.m3u8", .init(holds: true))
        let proxy = try Self.proxy(home)
        try await proxy.start()
        defer { proxy.stop() }
        let port = try #require(proxy.port)
        let held = (0..<StreamProxy.mostAtOnce).map { _ in RawPlayer(port: port) }
        defer { held.forEach { $0.close() } }
        held.forEach { $0.ask(Self.target(proxy, "/stream/abc/slow.m3u8")) }
        #expect(await eventually { proxy.serving == StreamProxy.mostAtOnce && home.seen.count == Self.perHost },
                "serving \(proxy.serving), the home saw \(home.seen.count)")
        let asked = home.seen.count
        let extra = RawPlayer(port: port)
        defer { extra.close() }
        extra.ask(Self.target(proxy, "/stream/abc/slow.m3u8"))
        #expect(String(decoding: await extra.read(12), as: UTF8.self) == "HTTP/1.1 503")
        #expect(home.seen.count == asked, "the home is not asked")
        held.forEach { $0.close() }
        extra.close()
        #expect(await eventually { proxy.serving == 0 }, "serving \(proxy.serving)")
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
        #expect(await eventually(2) { home.abandoned == 2 && proxy.serving == 0 }, "abandoned \(home.abandoned)")
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
