import Foundation
import Testing
@testable import MaktabaKit

/// A home whose process exits mid-answer: a plain-socket listener on this Mac's loopback
/// that sends its answer's head and the first `sends` bytes of a `length`-byte body, then
/// closes in order (a FIN, as the kernel closes the sockets of a process that exited), with
/// the body unfinished. The body says its length (`Content-Length`) or goes in chunks with
/// no last chunk.
final class ClosingHome: @unchecked Sendable {
    let length: Int
    let sends: Int
    let saysLength: Bool
    private let fd: Int32
    let port: UInt16
    private let lock = NSLock()
    private var answered = 0
    private var stopped = false

    /// How many answers it cut short so far.
    var cuts: Int { lock.withLock { answered } }
    var url: URL { URL(string: "http://127.0.0.1:\(port)")! }

    init(length: Int, sends: Int, saysLength: Bool) throws {
        self.length = length
        self.sends = sends
        self.saysLength = saysLength
        let fd = socket(AF_INET, SOCK_STREAM, 0)
        var on: Int32 = 1
        setsockopt(fd, SOL_SOCKET, SO_REUSEADDR, &on, socklen_t(MemoryLayout<Int32>.size))
        var a = sockaddr_in()
        a.sin_len = UInt8(MemoryLayout<sockaddr_in>.size)
        a.sin_family = sa_family_t(AF_INET)
        a.sin_port = 0
        a.sin_addr.s_addr = inet_addr("127.0.0.1")
        let bound = withUnsafePointer(to: &a) { $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { bind(fd, $0, socklen_t(MemoryLayout<sockaddr_in>.size)) } }
        guard bound == 0, listen(fd, 16) == 0 else { close(fd); throw URLError(.cannotConnectToHost) }
        var got = sockaddr_in()
        var size = socklen_t(MemoryLayout<sockaddr_in>.size)
        _ = withUnsafeMutablePointer(to: &got) { $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { getsockname(fd, $0, &size) } }
        port = UInt16(bigEndian: got.sin_port)
        self.fd = fd
        let thread = Thread { [self] in serve() }
        thread.start()
    }

    func stop() {
        lock.withLock { stopped = true }
        shutdown(fd, SHUT_RDWR)
        close(fd)
    }

    private func serve() {
        while true {
            let c = accept(fd, nil, nil)
            if c < 0 { return }
            if lock.withLock({ stopped }) { close(c); return }
            var on: Int32 = 1
            setsockopt(c, SOL_SOCKET, SO_NOSIGPIPE, &on, socklen_t(MemoryLayout<Int32>.size))
            answer(c)
        }
    }

    private func answer(_ c: Int32) {
        // the request's head
        var head = Data()
        var buf = [UInt8](repeating: 0, count: 4096)
        while head.range(of: Data("\r\n\r\n".utf8)) == nil {
            let n = recv(c, &buf, buf.count, 0)
            if n <= 0 { close(c); return }
            head.append(contentsOf: buf[0..<n])
        }
        var out = "HTTP/1.1 200 OK\r\nContent-Type: video/mp2t\r\n"
        out += saysLength ? "Content-Length: \(length)\r\n" : "Transfer-Encoding: chunked\r\n"
        out += "\r\n"
        var wire = Data(out.utf8)
        let body = LoopbackHome.body(0..<sends)
        if saysLength {
            wire.append(body)
        } else {
            // 64 KB chunks, every one whole; the last chunk ("0\r\n\r\n") never comes
            var at = 0
            while at < body.count {
                let end = min(at + (64 << 10), body.count)
                wire.append(Data("\(String(end - at, radix: 16))\r\n".utf8))
                wire.append(body[at..<end])
                wire.append(Data("\r\n".utf8))
                at = end
            }
        }
        _ = wire.withUnsafeBytes { raw -> Bool in
            var at = 0
            while at < raw.count {
                let n = send(c, raw.baseAddress! + at, raw.count - at, 0)
                if n <= 0 { return false }
                at += n
            }
            return true
        }
        lock.withLock { answered += 1 }
        // an orderly close: what was sent is delivered, then a FIN
        close(c)
    }
}

/// A home that closes its side in order mid-body (its process exited or restarted) must
/// never have its truncated segment reach the player as a whole one.
@Suite(.serialized) struct ProxyCutByAClose {
    static let length = 8 << 20
    static let sends = 1 << 20

    /// Why the home sends every segment with its length: the URL session takes a chunked
    /// body closed in order before its last chunk for a whole one (an eighth of the body,
    /// 200, no error), so nothing the proxy is told can tell it apart. Should this ever fail
    /// (the session reports the cut), the proxy could rely on the session for chunked
    /// bodies as well.
    @Test func theURLSessionTakesAChunkedBodyClosedEarlyForAWholeOne() async throws {
        let home = try ClosingHome(length: Self.length, sends: Self.sends, saysLength: false)
        defer { home.stop() }
        let (data, response) = try await URLSession(configuration: .ephemeral).data(from: home.url.appending(path: "seg-1.ts"))
        #expect((response as? HTTPURLResponse)?.statusCode == 200)
        #expect(data.count == Self.sends)
    }

    /// With its length, the same close is an error for the URL session.
    @Test func theURLSessionFailsABodyWithALengthClosedEarly() async throws {
        let home = try ClosingHome(length: Self.length, sends: Self.sends, saysLength: true)
        defer { home.stop() }
        await #expect(throws: URLError.self) {
            _ = try await URLSession(configuration: .ephemeral).data(from: home.url.appending(path: "seg-1.ts"))
        }
    }

    /// The proxy on a home that sends its length (as every home's segment now does): the
    /// player gets the length, fewer bytes and a connection cut off, never a whole segment;
    /// a player reading it with the URL session gets an error, and asks again.
    @Test func aTruncatedSegmentIsNotDeliveredAsWhole() async throws {
        let home = try ClosingHome(length: Self.length, sends: Self.sends, saysLength: true)
        defer { home.stop() }
        let proxy = try ProxyCut.proxy(home.url)
        try await proxy.start()
        defer { proxy.stop() }
        let port = try #require(proxy.port)

        let player = RawPlayer(port: port)
        defer { player.close() }
        player.ask("/\(proxy.secret)/stream/abc/seg-1.ts")
        let got = await player.readToEnd()
        let (head, body) = split(got.data)
        #expect(head.hasPrefix("HTTP/1.1 200"), "\(head)")
        #expect(head.contains("Content-Length: \(Self.length)"), "\(head)")
        #expect(!head.contains("Transfer-Encoding"), "\(head)")
        #expect(body.count < Self.length, "a cut segment came whole: \(body.count)")
        #expect(LoopbackHome.matches(body, from: 0), "what came is the segment's start")
        #expect(await eventually { proxy.serving == 0 }, "\(proxy.servingStates)")

        await #expect(throws: (any Error).self) {
            _ = try await URLSession(configuration: .ephemeral).data(from: try #require(proxy.local(home.url.appending(path: "stream/abc/seg-1.ts"))))
        }
        #expect(home.cuts == 2)
    }

    /// The length the proxy holds the home's answer to: the `Content-Length` it said, none
    /// when the session decoded the body or the status has none.
    @Test func theDeclaredLength() throws {
        func answer(_ headers: [String: String], status: Int = 200) -> HTTPURLResponse {
            HTTPURLResponse(url: URL(string: "http://h/x")!, statusCode: status, httpVersion: "HTTP/1.1", headerFields: headers)!
        }
        #expect(StreamProxy.declaredLength(answer(["Content-Length": "8388608"])) == 8 << 20)
        #expect(StreamProxy.declaredLength(answer(["Content-Length": "100", "Content-Range": "bytes 0-99/1000"], status: 206)) == 100)
        #expect(StreamProxy.declaredLength(answer(["Content-Length": "0"])) == 0)
        #expect(StreamProxy.declaredLength(answer(["Content-Length": " 12 "])) == 12)
        #expect(StreamProxy.declaredLength(answer(["Transfer-Encoding": "chunked"])) == nil)
        #expect(StreamProxy.declaredLength(answer(["Content-Length": "10", "Content-Encoding": "gzip"])) == nil)
        #expect(StreamProxy.declaredLength(answer(["Content-Length": "10", "Content-Encoding": "identity"])) == 10)
        #expect(StreamProxy.declaredLength(answer(["Content-Length": "10"], status: 304)) == nil)
        #expect(StreamProxy.declaredLength(answer(["Content-Length": "x"])) == nil)
        #expect(StreamProxy.declaredLength(nil) == nil)
    }
}

/// A proxy for a home at `url` (no `LoopbackHome`), signed in with a token that has not expired.
enum ProxyCut {
    static func proxy(_ url: URL) throws -> StreamProxy {
        let claims = Data(#"{"exp":4102444800}"#.utf8).base64EncodedString()
            .replacingOccurrences(of: "+", with: "-").replacingOccurrences(of: "/", with: "_").replacingOccurrences(of: "=", with: "")
        let base = url.absoluteString
        let secrets = FakeSecrets(["\(base)#access": "h.\(claims).s", "\(base)#refresh": "r1"])
        let tokens = TokenStore(service: "test.\(UUID().uuidString)", home: url, primary: secrets, legacy: nil)
        let session = URLSession(configuration: .ephemeral, delegate: PinnedTrust.shared, delegateQueue: nil)
        return try StreamProxy(client: APIClient(home: url, tokens: tokens, session: session), session: session) { try await Task.sleep(for: $0) }
    }
}
