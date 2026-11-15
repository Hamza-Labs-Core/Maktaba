import Foundation
import Network
import os
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
        /// After this many body bytes the home goes silent (the connection stays open) until
        /// `goOn` (it then writes the rest). A home that goes away mid-body is `DroppingHome`.
        var silentAfter: Int?
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
    private var silenced: [@Sendable () -> Void] = []
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

    /// Every answer that went silent (`silentThen`) goes on: the rest of its body is written.
    func goOn() {
        let all = lock.withLock { () -> [@Sendable () -> Void] in
            defer { silenced = [] }
            return silenced
        }
        all.forEach { $0() }
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
        let whole = range, cutAt = a.silentAfter.map { range.lowerBound + $0 }, chunked = !a.saysLength
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
            // the home says nothing more, the connection open, until `goOn`
            lock.withLock { silenced.append { [weak self] in self?.write(conn, rest, done, chunked: chunked) } }
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

/// A home that goes away mid-body: each request gets its head and `cutAfter` bytes of the
/// body (`LoopbackHome.byte(at:)`), then the connection is reset. A plain socket closed with
/// `SO_LINGER` 0: the reset leaves in `close()` itself. (`LoopbackHome` cut a body with
/// `NWConnection.forceCancel()`, which is no close the other side is sure to see. An orderly
/// close, a FIN, is no failure here: a URL session takes a chunked body that ends with the
/// connection as whole.) Even a reset is not always heard at once: on a CI runner the proxy's
/// URL session went on waiting after it, reading nothing from the socket, until the proxy's
/// patience with a silent home ran out and cancelled the request.
/// A path ending `/known.ts` says its length; any other goes in chunks.
final class DroppingHome: @unchecked Sendable {
    let port: UInt16
    private let fd: Int32
    private let length: Int
    private let cutAfter: Int
    private let accepting: DispatchSourceRead
    private let serving = DispatchQueue(label: "test.dropping-home", attributes: .concurrent)
    private let lock = NSLock()
    private var dropped = 0

    init(length: Int, cutAfter: Int) throws {
        self.length = length
        self.cutAfter = cutAfter
        fd = socket(AF_INET, SOCK_STREAM, 0)
        var on: Int32 = 1
        setsockopt(fd, SOL_SOCKET, SO_REUSEADDR, &on, socklen_t(MemoryLayout<Int32>.size))
        var a = sockaddr_in()
        a.sin_len = UInt8(MemoryLayout<sockaddr_in>.size)
        a.sin_family = sa_family_t(AF_INET)
        a.sin_port = 0
        a.sin_addr.s_addr = inet_addr("127.0.0.1")
        let fd = fd
        let bound = withUnsafePointer(to: &a) { $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { bind(fd, $0, socklen_t(MemoryLayout<sockaddr_in>.size)) } }
        guard bound == 0, listen(fd, 16) == 0 else { Darwin.close(fd); throw POSIXError(.EADDRNOTAVAIL) }
        var len = socklen_t(MemoryLayout<sockaddr_in>.size)
        _ = withUnsafeMutablePointer(to: &a) { $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { getsockname(fd, $0, &len) } }
        port = UInt16(bigEndian: a.sin_port)
        _ = fcntl(fd, F_SETFL, fcntl(fd, F_GETFL) | O_NONBLOCK)
        accepting = DispatchSource.makeReadSource(fileDescriptor: fd, queue: DispatchQueue(label: "test.dropping-home.accept"))
        accepting.setEventHandler { [weak self] in self?.acceptAll() }
        accepting.setCancelHandler { Darwin.close(fd) }
        accepting.resume()
    }

    var url: URL { URL(string: "http://127.0.0.1:\(port)")! }
    /// Connections reset mid-body so far.
    var drops: Int { lock.withLock { dropped } }

    func stop() { accepting.cancel() }

    private func acceptAll() {
        while true {
            let c = accept(fd, nil, nil)
            guard c >= 0 else { return }
            var on: Int32 = 1
            setsockopt(c, SOL_SOCKET, SO_NOSIGPIPE, &on, socklen_t(MemoryLayout<Int32>.size))
            serving.async { [self] in serve(c) }
        }
    }

    private func serve(_ c: Int32) {
        _ = fcntl(c, F_SETFL, fcntl(c, F_GETFL) & ~O_NONBLOCK)
        var head = Data()
        var buf = [UInt8](repeating: 0, count: 4096)
        while head.range(of: Data("\r\n\r\n".utf8)) == nil {
            let n = recv(c, &buf, buf.count, 0)
            guard n > 0 else { Darwin.close(c); return }
            head.append(contentsOf: buf[0..<n])
        }
        let saysLength = String(decoding: head, as: UTF8.self).contains("/known.ts ")
        var out = Data("HTTP/1.1 200 OK\r\nContent-Type: video/mp2t\r\n".utf8)
        out += Data((saysLength ? "Content-Length: \(length)\r\n" : "Transfer-Encoding: chunked\r\n").utf8)
        out += Data("Connection: close\r\n\r\n".utf8)
        var at = 0
        while at < cutAfter {
            let n = min(64 << 10, cutAfter - at)
            let piece = LoopbackHome.body(at..<at + n)
            out += saysLength ? piece : Data("\(String(n, radix: 16))\r\n".utf8) + piece + Data("\r\n".utf8)
            at += n
        }
        _ = out.withUnsafeBytes { raw -> Bool in
            var sent = 0
            while sent < raw.count {
                let n = send(c, raw.baseAddress! + sent, raw.count - sent, 0)
                if n <= 0 { return false }
                sent += n
            }
            return true
        }
        // gone, mid-body: a reset, at once
        var reset = linger(l_onoff: 1, l_linger: 0)
        setsockopt(c, SOL_SOCKET, SO_LINGER, &reset, socklen_t(MemoryLayout<linger>.size))
        Darwin.close(c)
        lock.withLock { dropped += 1 }
    }
}

/// A player's connection by hand: it can stop reading, close its sending side, or go away,
/// when a test says. A plain socket, not a Network.framework connection: on a Mac where many
/// loopback connections come and go (this suite beside others, a home running on the same
/// machine) an `NWConnection` client at times cannot start ("address already in use", for as
/// long as it is tried again) while a socket's `connect` to the same listener succeeds
/// (measured: listeners made one after another beside eight loops of short connections).
final class RawPlayer: @unchecked Sendable {
    /// What the proxy and the home under test look like (set by the test's setup), for the
    /// line a player prints when it gave up waiting.
    static let about = OSAllocatedUnfairLock<(@Sendable () -> String)?>(initialState: nil)

    private let lock = NSLock()
    private var fd: Int32
    private var states: [String] = []
    private let reading = DispatchQueue(label: "test.raw-player")
    /// What happened to the connection so far (a failed test says it).
    var history: String { lock.withLock { states.joined(separator: ">") } }

    init(port: UInt16) {
        fd = socket(AF_INET, SOCK_STREAM, 0)
        var on: Int32 = 1
        setsockopt(fd, SOL_SOCKET, SO_NOSIGPIPE, &on, socklen_t(MemoryLayout<Int32>.size))
        var a = sockaddr_in()
        a.sin_len = UInt8(MemoryLayout<sockaddr_in>.size)
        a.sin_family = sa_family_t(AF_INET)
        a.sin_port = port.bigEndian
        a.sin_addr.s_addr = inet_addr("127.0.0.1")
        let r = withUnsafePointer(to: &a) { $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { connect(fd, $0, socklen_t(MemoryLayout<sockaddr_in>.size)) } }
        states.append(r == 0 ? "connected" : "connect failed (\(errno))")
    }

    deinit { close() }

    func ask(_ target: String, method: String = "GET") {
        let bytes = Array("\(method) \(target) HTTP/1.1\r\nHost: 127.0.0.1\r\n\r\n".utf8)
        let sent = lock.withLock { fd >= 0 ? send(fd, bytes, bytes.count, 0) : -1 }
        if sent != bytes.count { lock.withLock { states.append("request not sent (\(errno))") } }
    }

    /// Closes this side only (a FIN after the request): the answer is still read.
    func finishSending() {
        lock.withLock { if fd >= 0 { shutdown(fd, SHUT_WR) } }
    }

    /// One read of up to `count` bytes, waiting at most `seconds`: the bytes (empty at the
    /// end of the connection), nil on an error, a reset or when the wait ran out.
    private func take(_ count: Int, within seconds: Double) -> Data? {
        let fd = lock.withLock { self.fd }
        guard fd >= 0 else { return nil }
        var limit = timeval(tv_sec: Int(seconds), tv_usec: Int32((seconds - Double(Int(seconds))) * 1_000_000))
        setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, &limit, socklen_t(MemoryLayout<timeval>.size))
        var buf = [UInt8](repeating: 0, count: count)
        let n = recv(fd, &buf, count, 0)
        if n < 0 {
            let why = errno
            lock.withLock { states.append(why == EAGAIN ? "gave up waiting" : "read failed (\(why))") }
            if why == EAGAIN {
                // what everything looked like, in the test's log (a recurrence says where it waited)
                print("GAVE UP WAITING: player \(history); \(RawPlayer.about.withLock { $0?() ?? "-" })")
            }
            return nil
        }
        return Data(buf[0..<n])
    }

    /// Reads up to `count` bytes (fewer when the other side closed first, or nothing came
    /// for `seconds`: never for ever).
    func read(_ count: Int, within seconds: Double = 30) async -> Data {
        await withCheckedContinuation { done in
            reading.async {
                var got = Data()
                while got.count < count {
                    guard let more = self.take(count - got.count, within: seconds), !more.isEmpty else { break }
                    got += more
                }
                done.resume(returning: got)
            }
        }
    }

    /// Reads to the end of the connection: what came, and whether it ended in an error (a
    /// reset, or nothing for `seconds`) and not an orderly close.
    func readToEnd(within seconds: Double = 30) async -> (data: Data, failed: Bool) {
        await withCheckedContinuation { done in
            reading.async {
                var got = Data()
                while true {
                    guard let more = self.take(1 << 20, within: seconds) else { done.resume(returning: (got, true)); return }
                    if more.isEmpty { done.resume(returning: (got, false)); return }
                    got += more
                }
            }
        }
    }

    /// Lets the connection go (with unread bytes waiting, that is a reset for the other side).
    func close() {
        lock.withLock {
            guard fd >= 0 else { return }
            Darwin.close(fd)
            fd = -1
            states.append("closed")
        }
    }
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
                      homePatience: Duration = StreamProxy.homePatience, requestTimeout: TimeInterval? = nil,
                      reach: StreamProxy.Reach = .loopback,
                      sleep: @escaping @Sendable (Duration) async throws -> Void = { try await Task.sleep(for: $0) }) throws -> StreamProxy {
        let proxy = try proxy(at: home.url, headPatience: headPatience, homePatience: homePatience,
                              requestTimeout: requestTimeout, reach: reach, sleep: sleep)
        RawPlayer.about.withLock {
            $0 = { [weak proxy, weak home] in
                "proxy accepted \(proxy?.acceptedCount ?? -1), serving \(proxy?.servingStates ?? []); home saw \(home?.seen.count ?? -1), wrote \(home?.bytesWritten ?? -1), answered \(home?.answered ?? -1), abandoned \(home?.abandoned ?? -1)"
            }
        }
        return proxy
    }

    /// A proxy in front of a home at `url` (with a token for it).
    static func proxy(at url: URL, headPatience: Duration = StreamProxy.headPatience,
                      homePatience: Duration = StreamProxy.homePatience, requestTimeout: TimeInterval? = nil,
                      reach: StreamProxy.Reach = .loopback,
                      sleep: @escaping @Sendable (Duration) async throws -> Void = { try await Task.sleep(for: $0) }) throws -> StreamProxy {
        let claims = Data(#"{"exp":4102444800}"#.utf8).base64EncodedString()
            .replacingOccurrences(of: "+", with: "-").replacingOccurrences(of: "/", with: "_").replacingOccurrences(of: "=", with: "")
        let base = url.absoluteString
        let secrets = FakeSecrets(["\(base)#access": "h.\(claims).s", "\(base)#refresh": "r1"])
        let tokens = TokenStore(service: "test.\(UUID().uuidString)", home: url, primary: secrets, legacy: nil)
        // a session with a delegate of its own, as the app's (`PinnedTrust.session`) has
        let config = URLSessionConfiguration.ephemeral
        config.httpMaximumConnectionsPerHost = perHost
        // the session's own timer, short in a test that proves it is not what times a pause
        if let requestTimeout { config.timeoutIntervalForRequest = requestTimeout }
        let session = URLSession(configuration: config, delegate: PinnedTrust.shared, delegateQueue: nil)
        return try StreamProxy(client: APIClient(home: url, tokens: tokens, session: session), session: session,
                               reach: reach, headPatience: headPatience, homePatience: homePatience, sleep: sleep)
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

    /// Cancelling ends the wait for the head or the body at once, on its own: it does not
    /// wait for the session to say the request ended. That word comes on the session's
    /// callback queue, and if it never comes (here the queue is stopped) a waiter left to it
    /// would hold its request's place for good, with the silence no longer counted.
    @Test(arguments: [false, true]) func cancellingEndsTheWaitWithoutTheSession(_ headCame: Bool) async throws {
        let home = try await LoopbackHome()
        defer { home.stop() }
        // a head and a little of the body, then nothing more; or nothing at all
        home.on("/stream/abc/x.ts", .init(length: 1 << 20, silentAfter: 4 << 10))
        home.on("/stream/abc/never.ts", .init(length: 1, holds: true))
        let callbacks = OperationQueue()
        callbacks.maxConcurrentOperationCount = 1
        let session = URLSession(configuration: .ephemeral, delegate: nil, delegateQueue: callbacks)
        defer { callbacks.isSuspended = false; session.invalidateAndCancel() }
        let path = headCame ? "stream/abc/x.ts" : "stream/abc/never.ts"
        let body = UpstreamBody(URLRequest(url: home.url.appending(path: path)), session: session, silence: .seconds(3600))
        if headCame {
            #expect(try await body.response()?.statusCode == 200)
            var got = 0
            while got < 4 << 10, let more = try await body.next() { got += more.count }
        }
        // from now the session tells nothing
        callbacks.isSuspended = true
        let waiter = Task { () -> String in
            do {
                if headCame { _ = try await body.next() } else { _ = try await body.response() }
                return "returned"
            } catch {
                return "threw"
            }
        }
        try await Task.sleep(for: .milliseconds(100))
        waiter.cancel()
        // whichever comes first (a waiter that never ends must not hold the test up)
        let outcome = await withCheckedContinuation { (first: CheckedContinuation<String, Never>) in
            let once = Once()
            Task { let v = await waiter.value; once.run { first.resume(returning: v) } }
            Task { try? await Task.sleep(for: .seconds(5)); once.run { first.resume(returning: "still waiting") } }
        }
        #expect(outcome == "threw", "\(headCame ? "next()" : "response()"): \(outcome); \(body.state)")
        // a second cancel, and the session's word when it does come, change nothing
        body.cancel()
        callbacks.isSuspended = false
    }

    /// What is kept of a body is its mark and one delivery, whoever reads and however slowly:
    /// never a share of the body. Nobody reading; then a reader that stops after each chunk
    /// (the case that ran away when the bound was a suspended task: 20 MB on CI, hundreds of
    /// megabytes against a fast home).
    @Test func aBodyIsKeptOnlyToItsMarkAndOneDelivery() async throws {
        let home = try await LoopbackHome()
        defer { home.stop() }
        let size = 192 << 20
        home.on("/stream/abc/big.mp4", .init(length: size))
        var r = URLRequest(url: home.url.appending(path: "stream/abc/big.mp4"))
        r.httpMethod = "GET"
        let body = UpstreamBody(r, session: URLSession(configuration: .ephemeral))
        #expect(try await body.response()?.statusCode == 200)
        // nobody asks for the body: the home stops writing once the mark and the sockets are full
        let wrote = try #require(await settled { home.bytesWritten }, "the home never stopped writing")
        #expect(wrote < size / 4, "the home wrote \(wrote) of \(size) bytes to a reader that took none")
        #expect(body.peak < UpstreamBody.highWater + body.biggestDelivery, "kept \(body.peak), biggest delivery \(body.biggestDelivery)")
        // a reader that stops after every chunk: every byte comes, in order, and the home is
        // never far ahead of what was taken
        var offset = 0, ahead = 0, chunks = 0
        while let chunk = try await body.next() {
            #expect(LoopbackHome.matches(chunk, from: offset))
            offset += chunk.count
            chunks += 1
            if chunks % 8 == 0 { try await Task.sleep(for: .milliseconds(5)) }
            ahead = max(ahead, home.bytesWritten - offset)
        }
        #expect(offset == size)
        // the property: held to the mark and one delivery, a small part of the body
        #expect(body.peak < UpstreamBody.highWater + body.biggestDelivery, "kept \(body.peak), biggest delivery \(body.biggestDelivery)")
        #expect(body.peak < size / 8, "kept \(body.peak) of \(size)")
        #expect(ahead < size / 4, "the home was \(ahead) bytes ahead of the reader")
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
        // every timer is short here (a request's head 200 ms, the home's silence 400 ms, the
        // URL session's own request timeout 1 s): the slow read outlasts each several times,
        // and one delivery (up to 4 MB) takes far longer than any of them to drain
        let proxy = try Self.proxy(home, headPatience: .milliseconds(200), homePatience: .milliseconds(400), requestTimeout: 1)
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
        // the home's silence is timed at 400 ms and the URL session's own request timeout is
        // 1 s: the pause lasts 3 s, several times each (a pause is not the home's silence)
        let proxy = try Self.proxy(home, headPatience: .milliseconds(200), homePatience: .milliseconds(400), requestTimeout: 1)
        try await proxy.start()
        defer { proxy.stop() }
        let player = RawPlayer(port: try #require(proxy.port))
        defer { player.close() }
        player.ask(Self.target(proxy, "/stream/abc/big.mp4"))
        var got = await player.read(100_000)
        // paused: the home stops writing, and nothing happens for many timers
        _ = try #require(await settled { home.bytesWritten }, "the home never stopped writing")
        try await Task.sleep(for: .seconds(3))
        #expect(proxy.serving == 1 && home.abandoned == 0, "a paused player is not dropped")
        let (rest, failed) = await player.readToEnd()
        got += rest
        let (head, body) = split(got)
        #expect(!failed && head.hasPrefix("HTTP/1.1 200"))
        #expect(body.count == size && LoopbackHome.matches(body, from: 0), "\(body.count) of \(size) bytes")
        #expect(home.seen.count == 1, "one request: nothing was asked again")
    }

    /// A home that goes silent mid-body (its connection open) is given up after the time the
    /// proxy gives it, and the player sees an error: a body short of its length, or in chunks
    /// without its last chunk.
    @Test func aHomeThatGoesSilentMidBodyIsGivenUp() async throws {
        let home = try await LoopbackHome()
        defer { home.stop() }
        home.on("/stream/abc/known.ts", .init(length: 8 << 20, silentAfter: 1 << 20))
        home.on("/stream/abc/unknown.ts", .init(length: 8 << 20, saysLength: false, silentAfter: 1 << 20))
        let proxy = try Self.proxy(home, homePatience: .milliseconds(500))
        try await proxy.start()
        defer { proxy.stop() }
        let port = try #require(proxy.port)
        for path in ["known.ts", "unknown.ts"] {
            let player = RawPlayer(port: port)
            defer { player.close() }
            player.ask(Self.target(proxy, "/stream/abc/\(path)"))
            let began = ContinuousClock.now
            let all = await player.readToEnd(within: 20)
            let (head, body) = split(all.data)
            #expect(head.hasPrefix("HTTP/1.1 200"), "\(path): \(all.data.count) bytes came; player \(player.history); proxy accepted \(proxy.acceptedCount), serving \(proxy.servingStates); home saw \(home.seen.count), wrote \(home.bytesWritten)")
            #expect(ContinuousClock.now - began < .seconds(5), "\(path): given up in the proxy's time, not the session's minute")
            if path == "known.ts" {
                #expect(body.count < 8 << 20, "\(path): short of its length (\(body.count))")
            } else {
                #expect(head.contains("Transfer-Encoding: chunked") && all.data.suffix(5) != Data("0\r\n\r\n".utf8), "\(path): no last chunk")
            }
            #expect(await eventually { proxy.serving == 0 }, "\(path): serving \(proxy.serving)")
        }
        #expect(await eventually { home.abandoned == 2 }, "the home's requests were ended: \(home.abandoned)")
    }

    /// The home's silence is counted only while the home is what is waited on. With the
    /// player not taking (nobody asks for more), fewer bytes kept than the mark that holds a
    /// callback, and the home silent for several times its patience, the request lives; it
    /// completes when the player takes again and the home goes on.
    @Test func silenceIsNotCountedWhileThePlayerIsNotTaking() async throws {
        let home = try await LoopbackHome()
        defer { home.stop() }
        let size = 2 << 20, first = 100_000
        #expect(first < UpstreamBody.highWater)
        home.on("/stream/abc/film.mp4", .init(length: size, silentAfter: first))
        let body = UpstreamBody(URLRequest(url: home.url.appending(path: "stream/abc/film.mp4")),
                                session: URLSession(configuration: .ephemeral), silence: .milliseconds(300))
        #expect(try await body.response()?.statusCode == 200)
        // the player's write is in flight (nobody asks for more), the home has said all it
        // will for now: five times the patience passes
        #expect(await eventually { home.bytesWritten == first })
        try await Task.sleep(for: .milliseconds(1500))
        #expect(!body.state.contains("ended"), "\(body.state)")
        // the player takes again and the home goes on: every byte, in order
        home.goOn()
        var offset = 0
        while let chunk = try await body.next() {
            #expect(LoopbackHome.matches(chunk, from: offset))
            offset += chunk.count
        }
        #expect(offset == size, "\(offset) of \(size)")
    }

    /// A stream the home redirects (within itself) is paused like any other: the redirected
    /// request is not timed by the session either.
    @Test func aRedirectedStreamIsNotTimedWhilePaused() async throws {
        let home = try await LoopbackHome()
        defer { home.stop() }
        let size = 48 << 20
        home.on("/stream/abc/moved.mp4", .init(status: 302, headers: ["Location": "/stream/abc/there.mp4"]))
        home.on("/stream/abc/there.mp4", .init(length: size))
        // the URL session's own request timeout is 1 s: the pause lasts 2.5 s. (Checked: the
        // request URLSession builds for the redirect keeps the first one's long timeout, so
        // the redirect rule sets nothing; this test is what keeps that true.)
        let proxy = try Self.proxy(home, requestTimeout: 1)
        try await proxy.start()
        defer { proxy.stop() }
        let player = RawPlayer(port: try #require(proxy.port))
        defer { player.close() }
        player.ask(Self.target(proxy, "/stream/abc/moved.mp4"))
        var got = await player.read(100_000)
        _ = try #require(await settled { home.bytesWritten }, "the home never stopped writing")
        try await Task.sleep(for: .milliseconds(2500))
        let (rest, failed) = await player.readToEnd()
        got += rest
        let (head, body) = split(got)
        #expect(!failed && head.hasPrefix("HTTP/1.1 200"), "\(head.prefix(40)); player \(player.history)")
        #expect(body.count == size && LoopbackHome.matches(body, from: 0), "\(body.count) of \(size) bytes")
    }

    /// A home that never sends an answer's head is given up: the player gets a 502.
    @Test func aHomeThatNeverAnswersIsGivenUp() async throws {
        let home = try await LoopbackHome()
        defer { home.stop() }
        home.on("/stream/abc/slow.m3u8", .init(holds: true))
        let proxy = try Self.proxy(home, homePatience: .milliseconds(500))
        try await proxy.start()
        defer { proxy.stop() }
        let began = ContinuousClock.now
        let (_, response) = try await URLSession.shared.data(from: try #require(proxy.local(home.url.appending(path: "stream/abc/slow.m3u8"))))
        #expect((response as? HTTPURLResponse)?.statusCode == 502)
        #expect(ContinuousClock.now - began < .seconds(5))
        #expect(await eventually { proxy.serving == 0 && home.abandoned == 1 })
    }

    /// Every place taken by requests nobody waits for (their players closed their side before
    /// the home answered: tunes and seeks that moved on while the home was silent): the
    /// oldest gives its place to a new request, which is served, not refused.
    @Test func aFullProxyGivesAnAbandonedPlaceToANewRequest() async throws {
        let home = try await LoopbackHome()
        defer { home.stop() }
        home.on("/stream/abc/slow.m3u8", .init(holds: true))
        home.on("/stream/abc/small.ts", .init(length: 1000))
        let proxy = try Self.proxy(home)
        try await proxy.start()
        defer { proxy.stop() }
        let port = try #require(proxy.port)
        let gone = (0..<StreamProxy.mostAtOnce).map { _ in RawPlayer(port: port) }
        defer { gone.forEach { $0.close() } }
        for p in gone {
            p.ask(Self.target(proxy, "/stream/abc/slow.m3u8"))
            p.finishSending()
        }
        #expect(await eventually { proxy.serving == StreamProxy.mostAtOnce && home.seen.count == StreamProxy.mostAtOnce },
                "serving \(proxy.serving), the home saw \(home.seen.count)")
        // a new request is served: the oldest abandoned one gave its place, and its request
        // to the home was ended
        let small = try #require(proxy.local(home.url.appending(path: "stream/abc/small.ts")))
        let (data, response) = try await URLSession.shared.data(from: small)
        #expect((response as? HTTPURLResponse)?.statusCode == 200 && data.count == 1000)
        #expect(await eventually { home.abandoned == 1 && proxy.serving == StreamProxy.mostAtOnce - 1 },
                "abandoned \(home.abandoned), serving \(proxy.serving)")
        // full again, and again a new request is served
        let another = RawPlayer(port: port)
        defer { another.close() }
        another.ask(Self.target(proxy, "/stream/abc/slow.m3u8"))
        another.finishSending()
        #expect(await eventually { proxy.serving == StreamProxy.mostAtOnce })
        let (more, again) = try await URLSession.shared.data(from: small)
        #expect((again as? HTTPURLResponse)?.statusCode == 200 && more.count == 1000)
        #expect(await eventually { home.abandoned == 2 }, "abandoned \(home.abandoned)")
    }

    /// Requests that share a URL session one after another, and requests under way at the
    /// same time, never see each other's bytes.
    @Test func reusedSessionsNeverCrossStreams() async throws {
        let home = try await LoopbackHome()
        defer { home.stop() }
        home.on("/stream/abc/file.mp4", .init(length: 64 << 20))
        let proxy = try Self.proxy(home)
        try await proxy.start()
        defer { proxy.stop() }
        let local = try #require(proxy.local(home.url.appending(path: "stream/abc/file.mp4")))
        for round in 0..<4 {
            // six ranges at once, each its own part of the file, of different lengths
            try await withThrowingTaskGroup(of: Void.self) { group in
                for k in 0..<6 {
                    let from = (round * 6 + k) * 1_000_003, count = 300_000 + k * 700_001
                    group.addTask {
                        var r = URLRequest(url: local)
                        r.setValue("bytes=\(from)-\(from + count - 1)", forHTTPHeaderField: "Range")
                        let (data, _) = try await URLSession.shared.data(for: r)
                        #expect(data.count == count && LoopbackHome.matches(data, from: from), "range from \(from)")
                    }
                }
                try await group.waitForAll()
            }
            #expect(await eventually { proxy.serving == 0 })
            #expect(proxy.idleSessions <= UpstreamSessions.keptIdle)
        }
    }

    /// A request cancelled while its callback is held gives its session back, and the next
    /// request on that same session gets its own answer, whole.
    @Test func cancellingAHeldRequestFreesItsSessionForTheNext() async throws {
        let home = try await LoopbackHome()
        defer { home.stop() }
        home.on("/stream/abc/big.mp4", .init(length: 128 << 20))
        home.on("/stream/abc/next.ts", .init(length: 2 << 20))
        let sessions = UpstreamSessions(like: URLSession(configuration: .ephemeral))
        defer { sessions.close() }
        for _ in 0..<3 {
            let first = sessions.take()
            let held = UpstreamBody(URLRequest(url: home.url.appending(path: "stream/abc/big.mp4")), session: first) { sessions.give(first) }
            #expect(try await held.response()?.statusCode == 200)
            // nobody takes the body: its callback is held at the mark
            #expect(await eventually { held.peak >= UpstreamBody.highWater }, "kept \(held.peak)")
            held.cancel()
            #expect(await eventually { sessions.idleCount == 1 }, "the cancelled request gave its session back")
            let again = sessions.take()
            #expect(again === first, "the same session serves the next request")
            let body = UpstreamBody(URLRequest(url: home.url.appending(path: "stream/abc/next.ts")), session: again) { sessions.give(again) }
            #expect(try await body.response()?.statusCode == 200)
            var got = Data()
            while let chunk = try await body.next() { got += chunk }
            #expect(got.count == 2 << 20 && LoopbackHome.matches(got, from: 0), "\(got.count) bytes")
            #expect(await eventually { sessions.idleCount == 1 })
        }
    }

    /// A connection the LAN proxy accepted really has keepalive on; one the loopback proxy
    /// accepted has not.
    @Test func anAcceptedLANConnectionHasKeepalive() async throws {
        let home = try await LoopbackHome()
        defer { home.stop() }
        // the "network" address of a test is this Mac's loopback: the proxy is the LAN kind
        let lan = try Self.proxy(home, reach: .lan(host: "127.0.0.1"))
        let loop = try Self.proxy(home)
        try await lan.start()
        try await loop.start()
        defer { lan.stop(); loop.stop() }
        let a = RawPlayer(port: try #require(lan.port)), b = RawPlayer(port: try #require(loop.port))
        defer { a.close(); b.close() }
        #expect(await eventually { lan.servingOptions.count == 1 && loop.servingOptions.count == 1 })
        let onLAN = try #require(lan.servingOptions.first), onLoop = try #require(loop.servingOptions.first)
        #expect(onLAN.enableKeepalive && onLAN.keepaliveIdle == 30 && onLAN.keepaliveInterval == 10 && onLAN.keepaliveCount == 3)
        #expect(onLAN.connectionDropTime == 120)
        #expect(!onLoop.enableKeepalive)
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
        // accepted, then closed (looked at by the count: on a busy Mac both can pass between two looks)
        #expect(await eventually { proxy.acceptedCount == 1 }, "the connection: \(silent.history)")
        #expect(await eventually(3) { proxy.waitingForHead == 0 }, "waiting \(proxy.waitingForHead)")
        #expect(proxy.serving == 0, "a connection with no head never took a place")
        #expect(home.seen.isEmpty)
    }

    /// Sockets that connect and say nothing, more than every place and every waiting room
    /// together, keep no player out: they take none of `mostAtOnce`'s places, the oldest of
    /// them is closed to make room, and a request under way is never the one pushed out.
    @Test func silentSocketsBeyondTheLimitDoNotKeepAPlayerOut() async throws {
        let home = try await LoopbackHome()
        defer { home.stop() }
        home.on("/stream/abc/big.mp4", .init(length: 64 << 20))
        home.on("/stream/abc/small.ts", .init(length: 1000))
        // the head's patience as shipped: nothing here waits for a silent socket to time out
        let proxy = try Self.proxy(home)
        try await proxy.start()
        defer { proxy.stop() }
        let port = try #require(proxy.port)
        // a request under way, its player reading nothing for now
        let playing = RawPlayer(port: port)
        defer { playing.close() }
        playing.ask(Self.target(proxy, "/stream/abc/big.mp4"))
        #expect(await eventually { proxy.serving == 1 && home.seen.count == 1 })

        let flood = StreamProxy.mostAtOnce + StreamProxy.mostWaiting + 8
        let silent = (0..<flood).map { _ in RawPlayer(port: port) }
        defer { silent.forEach { $0.close() } }
        #expect(await eventually { proxy.acceptedCount == flood + 1 }, "accepted \(proxy.acceptedCount)")
        #expect(await eventually { proxy.waitingForHead <= StreamProxy.mostWaiting }, "waiting \(proxy.waitingForHead)")
        #expect(proxy.serving == 1 && home.abandoned == 0, "the request under way keeps its place: \(proxy.servingStates)")

        // a player's request is served at once, though every silent socket is still open
        let small = try #require(proxy.local(home.url.appending(path: "stream/abc/small.ts")))
        let (data, response) = try await URLSession.shared.data(from: small)
        #expect((response as? HTTPURLResponse)?.statusCode == 200 && data.count == 1000)
        #expect(home.seen.contains { $0.target.hasSuffix("small.ts") })
        // and the one under way still delivers
        #expect(String(decoding: await playing.read(12), as: UTF8.self) == "HTTP/1.1 200")
    }

    /// A request with a wrong secret is refused before it takes a place: with every place
    /// delivering it is a 404, not a 503, the home is never asked, and the count of requests
    /// served never moves.
    @Test func aWrongSecretNeverHoldsAPlace() async throws {
        let home = try await LoopbackHome()
        defer { home.stop() }
        home.on("/stream/abc/big.mp4", .init(length: 64 << 20))
        home.on("/stream/abc/small.ts", .init(length: 1000))
        let proxy = try Self.proxy(home)
        try await proxy.start()
        defer { proxy.stop() }
        let port = try #require(proxy.port)

        // with no place taken: refused, and nothing counted while the refusal is read
        let wrong = (0..<(StreamProxy.mostAtOnce + 4)).map { _ in RawPlayer(port: port) }
        defer { wrong.forEach { $0.close() } }
        for p in wrong { p.ask("/not-the-secret/stream/abc/small.ts") }
        for p in wrong {
            #expect(String(decoding: await p.read(12), as: UTF8.self) == "HTTP/1.1 404", "\(p.history)")
        }
        #expect(proxy.serving == 0, "serving \(proxy.servingStates)")
        let (data, response) = try await URLSession.shared.data(from: try #require(proxy.local(home.url.appending(path: "stream/abc/small.ts"))))
        #expect((response as? HTTPURLResponse)?.statusCode == 200 && data.count == 1000)

        // with every place delivering: still a 404 (it never asked for one), never a 503
        let held = (0..<StreamProxy.mostAtOnce).map { _ in RawPlayer(port: port) }
        defer { held.forEach { $0.close() } }
        held.forEach { $0.ask(Self.target(proxy, "/stream/abc/big.mp4")) }
        #expect(await eventually { proxy.serving == StreamProxy.mostAtOnce }, "serving \(proxy.serving)")
        let late = RawPlayer(port: port)
        defer { late.close() }
        late.ask("/\(UUID().uuidString.lowercased())/stream/abc/small.ts")
        #expect(String(decoding: await late.read(12), as: UTF8.self) == "HTTP/1.1 404")
        #expect(proxy.serving == StreamProxy.mostAtOnce)
        #expect(home.seen.filter { $0.target.hasSuffix("small.ts") }.count == 1, "the home is asked only for the real request")
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
            #expect(await eventually { proxy.serving == 5 && home.seen.count == 5 * round }, "serving \(proxy.serving), the home saw \(home.seen.count); players \(players.map(\.history))")
            players.forEach { $0.close() }
            home.answerHeld()
            #expect(await eventually { proxy.serving == 0 }, "round \(round): serving \(proxy.serving)")
        }
        // each request had a URL session of its own; only a few are kept for the next ones
        #expect(await eventually { proxy.idleSessions >= 1 && proxy.idleSessions <= UpstreamSessions.keptIdle }, "idle sessions \(proxy.idleSessions)")
        proxy.stop()
        #expect(proxy.idleSessions == 0, "and none once the proxy stopped")
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
    /// never comes. The home goes away with a reset (`DroppingHome`), and the request's place
    /// is given back. A URL session does not always hear the reset (on a CI runner it waited
    /// on, reading nothing, for the whole minute of the proxy's patience): the proxy here is
    /// given 5 s of patience with a home gone silent, so either way the request ends within
    /// this test's wait.
    @Test func aBodyCutShortIsAnErrorForThePlayer() async throws {
        let home = try DroppingHome(length: 8 << 20, cutAfter: 1 << 20)
        defer { home.stop() }
        let proxy = try Self.proxy(at: home.url, homePatience: .seconds(5))
        try await proxy.start()
        defer { proxy.stop() }
        let port = try #require(proxy.port)

        let known = RawPlayer(port: port)
        defer { known.close() }
        var began = ContinuousClock.now
        known.ask(Self.target(proxy, "/stream/abc/known.ts"))
        let a = await known.readToEnd()
        let tookKnown = ContinuousClock.now - began
        let first = split(a.data)
        #expect(first.head.contains("Content-Length: \(8 << 20)") && first.body.count < 8 << 20, "short of its length: \(first.body.count)")
        #expect(!first.body.contains(Data("HTTP/1.1".utf8)), "no second status line inside the body")

        let unknown = RawPlayer(port: port)
        defer { unknown.close() }
        began = ContinuousClock.now
        unknown.ask(Self.target(proxy, "/stream/abc/unknown.ts"))
        let b = await unknown.readToEnd()
        let tookUnknown = ContinuousClock.now - began
        let second = split(b.data)
        #expect(!second.head.contains("Content-Length") && second.head.contains("Transfer-Encoding: chunked"))
        #expect(b.data.suffix(5) != Data("0\r\n\r\n".utf8), "a body that was cut short must not end with the last chunk")
        #expect(await eventually { proxy.serving == 0 }, "serving \(proxy.servingStates); home dropped \(home.drops); read in \(tookKnown) and \(tookUnknown)")
        #expect(home.drops == 2, "home dropped \(home.drops); read in \(tookKnown) and \(tookUnknown)")

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

    /// What one reader that takes nothing pins: a 1 GB body, nobody reading, looked at for
    /// several seconds; the chunked copy (`UpstreamBody`) and the copy as it was (`AsyncBytes`).
    @Test(.enabled(if: on)) func whatAReaderThatTakesNothingPins() async throws {
        let size = 1 << 30
        for way in ["UpstreamBody", "AsyncBytes"] {
            let home = try await LoopbackHome()
            home.on("/stream/abc/huge.mp4", .init(length: size))
            let url = home.url.appending(path: "stream/abc/huge.mp4")
            // MAKTABA_PROXY_BENCH_STARVE=ms: the session's delegate queue is held that long
            // right after the request starts (a busy device), while the home keeps writing
            let starve = Int(ProcessInfo.processInfo.environment["MAKTABA_PROXY_BENCH_STARVE"] ?? "") ?? 0
            let callbacks = OperationQueue()
            callbacks.maxConcurrentOperationCount = 1
            let session = URLSession(configuration: .ephemeral, delegate: nil, delegateQueue: callbacks)
            if starve > 0 { callbacks.addOperation { Thread.sleep(forTimeInterval: Double(starve) / 1000) } }
            var body: UpstreamBody?
            var bytes: URLSession.AsyncBytes?
            if way == "UpstreamBody" {
                body = UpstreamBody(URLRequest(url: url), session: session)
                _ = try await body?.response()
                // read a little, stop: a player that paused after it had been reading
                for _ in 0..<3 {
                    try await Task.sleep(for: .milliseconds(300))
                    _ = try await body?.next()
                }
            } else {
                let (b, _) = try await session.bytes(for: URLRequest(url: url))
                bytes = b
                // one byte read, as a copy that has started and then waits on a write
                var it = b.makeAsyncIterator()
                _ = try await it.next()
            }
            var line = "PIN \(way):"
            for _ in 0..<8 {
                try await Task.sleep(for: .milliseconds(500))
                line += " \(home.bytesWritten >> 20)"
                if let body { line += "/\(body.peak >> 20)" }
            }
            print(line + "  (MB the home wrote" + (body != nil ? " / MB kept" : "") + ", every 0.5 s)")
            body?.cancel()
            bytes?.task.cancel()
            session.invalidateAndCancel()
            home.stop()
        }
    }

    /// What a slow reader pins while it reads: the most the home had written beyond what the
    /// reader had taken (kept in the copy, the session and the sockets), over a 1 GB body read
    /// with a pause of MAKTABA_PROXY_BENCH_PAUSE ms (default 5) after each chunk (64 KB for
    /// the copy as it was).
    @Test(.enabled(if: on)) func whatASlowReaderPins() async throws {
        let size = 1 << 30
        let pause = Duration.milliseconds(Int(ProcessInfo.processInfo.environment["MAKTABA_PROXY_BENCH_PAUSE"] ?? "") ?? 5)
        let limit = ContinuousClock.now + .seconds(12)
        for way in ["UpstreamBody", "AsyncBytes"] {
            let home = try await LoopbackHome()
            home.on("/stream/abc/huge.mp4", .init(length: size))
            let url = home.url.appending(path: "stream/abc/huge.mp4")
            let session = URLSession(configuration: .ephemeral)
            var taken = 0, ahead = 0, biggest = 0
            let until = min(limit, ContinuousClock.now + .seconds(6))
            if way == "UpstreamBody" {
                let body = UpstreamBody(URLRequest(url: url), session: session)
                _ = try await body.response()
                while ContinuousClock.now < until, let chunk = try await body.next() {
                    taken += chunk.count
                    biggest = max(biggest, chunk.count)
                    ahead = max(ahead, home.bytesWritten - taken)
                    try await Task.sleep(for: pause)
                }
                print("PIN slow reader, UpstreamBody: kept at most \(body.peak >> 10) KB, biggest delivery \(body.biggestDelivery >> 10) KB, biggest chunk \(biggest >> 10) KB, the home at most \(ahead >> 10) KB ahead; \(taken >> 20) MB read")
                body.cancel()
            } else {
                let (bytes, _) = try await session.bytes(for: URLRequest(url: url))
                var n = 0
                for try await _ in bytes {
                    n += 1
                    if n == 64 << 10 {
                        taken += n
                        n = 0
                        ahead = max(ahead, home.bytesWritten - taken)
                        if ContinuousClock.now >= until { break }
                        try await Task.sleep(for: pause)
                    }
                }
                print("PIN slow reader, AsyncBytes: the home at most \(ahead >> 10) KB ahead; \(taken >> 20) MB read")
                bytes.task.cancel()
            }
            session.invalidateAndCancel()
            home.stop()
        }
    }
}
