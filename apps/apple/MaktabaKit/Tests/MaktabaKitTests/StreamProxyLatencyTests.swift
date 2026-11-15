import Foundation
import Testing
@testable import MaktabaKit

/// The stream proxy passes a request on the moment it comes (`StreamProxy.Latency`): AVPlayer
/// gives a segment about one target duration (2 s for the test stream), then drops the
/// stream's only variant and stops until the app reopens the stream. On a loaded simulator
/// the proxy passed a segment on 3 s after the player asked (PR #1412's proof run, the
/// iphone shots shard), while the home answered in 0.27 s.
///
/// Each test holds back something the request could wait behind (another request, a token
/// refresh) for as long as the test runs: a request that waits behind it never reaches the
/// home in time, however fast the machine. The bound (`soon`) only absorbs a loaded CI
/// machine; on this Mac a request goes on in a few milliseconds.
@Suite(.serialized) struct ProxyLatency {
    /// How soon the home must see a request: well inside AVPlayer's 2 s.
    static let soon: Duration = .seconds(1)

    /// How long after `sent` the home read the request for `path` (nil: not within `within`).
    static func reached(_ home: LoopbackHome, _ path: String, after sent: ContinuousClock.Instant,
                        within: Double = 5) async -> Duration? {
        _ = await eventually(within) { home.seen.contains { $0.target.hasPrefix(path) } }
        return home.seen.first { $0.target.hasPrefix(path) }.map { $0.at - sent }
    }

    /// A token close to its end is refreshed apart from the request: the request goes on with
    /// the token held now, while the refresh is still waiting on the home.
    @Test func aRequestDoesNotWaitForATokenRefresh() async throws {
        let home = try await LoopbackHome()
        defer { home.stop() }
        // the refresh never comes back while the test runs
        home.on("/api/auth/refresh", .init(holds: true))
        home.on("/stream/abc/seg.ts", .init(length: 1000))
        // good for 30 s more: under `Token.ahead`, over `Token.floor`
        let proxy = try ProxyCopy.proxy(home, tokenLife: 30)
        try await proxy.start()
        defer { proxy.stop() }
        let player = RawPlayer(port: try #require(proxy.port))
        defer { player.close() }
        let sent = ContinuousClock.now
        player.ask(ProxyCopy.target(proxy, "/stream/abc/seg.ts"))
        let took = await Self.reached(home, "/stream/abc/seg.ts", after: sent)
        #expect(took.map { $0 < Self.soon } == true, "the home saw the segment after \(took.map { "\($0)" } ?? "never"): \(home.seen.map(\.target))")
        let (head, body) = split(await player.read(4096, within: 10))
        #expect(head.hasPrefix("HTTP/1.1 200") && body.count == 1000, "\(head)")
        #expect(home.seen.first { $0.target.hasPrefix("/stream/") }?.headers["authorization"]?.hasPrefix("Bearer h.") == true,
                "with the token held now")
        #expect(await eventually { home.seen.contains { $0.method == "POST" && $0.target == "/api/auth/refresh" } },
                "and the token is refreshed ahead of its end: \(home.seen.map(\.target))")
    }

    /// Requests the home holds (it has not answered) and one whose player reads nothing (its
    /// session's callback held) hold no other request back.
    @Test func aRequestDoesNotWaitBehindAnother() async throws {
        let home = try await LoopbackHome()
        defer { home.stop() }
        home.on("/stream/abc/held.ts", .init(holds: true))
        home.on("/stream/abc/big.ts", .init(length: 64 << 20))
        home.on("/stream/abc/seg.ts", .init(length: 1000))
        let proxy = try ProxyCopy.proxy(home)
        try await proxy.start()
        defer { proxy.stop() }
        let port = try #require(proxy.port)
        let held = (0..<4).map { _ in RawPlayer(port: port) }
        defer { held.forEach { $0.close() } }
        held.forEach { $0.ask(ProxyCopy.target(proxy, "/stream/abc/held.ts")) }
        let paused = RawPlayer(port: port)
        defer { paused.close() }
        paused.ask(ProxyCopy.target(proxy, "/stream/abc/big.ts"))
        #expect(await eventually { home.seen.count == 5 }, "the home saw \(home.seen.map(\.target))")
        // the paused player's answer has begun and the home is held back by it
        #expect(String(decoding: await paused.read(12).prefix(12), as: UTF8.self) == "HTTP/1.1 200")
        _ = await settled { home.bytesWritten }

        let player = RawPlayer(port: port)
        defer { player.close() }
        let sent = ContinuousClock.now
        player.ask(ProxyCopy.target(proxy, "/stream/abc/seg.ts"))
        let took = await Self.reached(home, "/stream/abc/seg.ts", after: sent)
        #expect(took.map { $0 < Self.soon } == true, "the home saw the segment after \(took.map { "\($0)" } ?? "never")")
        let (head, body) = split(await player.read(4096, within: 10))
        #expect(head.hasPrefix("HTTP/1.1 200") && body.count == 1000, "\(head)")
    }

    /// Requests are served above background work: on a busy device a task at utility
    /// priority waited seconds for a thread while the player's 2 s ran out.
    @Test func aRequestIsServedAboveBackgroundPriority() async throws {
        let home = try await LoopbackHome()
        defer { home.stop() }
        home.on("/stream/abc/seg.ts", .init(length: 1000))
        let proxy = try ProxyCopy.proxy(home)
        try await proxy.start()
        defer { proxy.stop() }
        let (_, response) = try await URLSession.shared.data(from: try #require(proxy.local(home.url.appending(path: "stream/abc/seg.ts"))))
        #expect((response as? HTTPURLResponse)?.statusCode == 200)
        let priority = try #require(proxy.lastForwardPriority)
        #expect(priority >= .userInitiated, "passed on at \(priority)")
        #expect(StreamProxy.Latency.qos.qosClass == .userInitiated && StreamProxy.Latency.operationQoS == .userInitiated)
    }

    /// The delay itself, on this Mac: every core busy with work at user-initiated priority
    /// (four threads a core), and ten requests through the proxy one after another. At utility
    /// priority the home saw them 100 ms to 1 s after they were sent (28 cores, 112 threads);
    /// at user-initiated, in 2 to 10 ms (one in 150 ms). It loads the whole machine, so it runs
    /// only when asked: `MAKTABA_LOAD_TESTS=1 swift test --filter ProxyLatency`.
    @Test(.enabled(if: ProcessInfo.processInfo.environment["MAKTABA_LOAD_TESTS"] == "1"))
    func aBusyDeviceDoesNotHoldARequestBack() async throws {
        let home = try await LoopbackHome()
        defer { home.stop() }
        home.on("/stream/abc/seg.ts", .init(length: 1000))
        let proxy = try ProxyCopy.proxy(home)
        try await proxy.start()
        defer { proxy.stop() }
        let port = try #require(proxy.port)
        let busy = Busy(threads: ProcessInfo.processInfo.activeProcessorCount * 4)
        defer { busy.stop() }
        try await Task.sleep(for: .milliseconds(300))
        var took: [Duration] = []
        for i in 0..<10 {
            let player = RawPlayer(port: port)
            let sent = ContinuousClock.now
            player.ask(ProxyCopy.target(proxy, "/stream/abc/seg.ts"))
            _ = await eventually(10) { home.seen.count > i }
            took.append(home.seen.count > i ? home.seen[i].at - sent : .seconds(10))
            player.close()
        }
        let ms = took.map { $0.components.seconds * 1000 + $0.components.attoseconds / 1_000_000_000_000_000 }
        print("ProxyLatency under load, ms: \(ms)")
        #expect(ms.sorted()[ms.count / 2] < 50, "the median: \(ms)")
        #expect(ms.max() ?? 0 < 1000, "the slowest: \(ms)")
    }
}

/// Threads that keep every core busy at user-initiated priority until stopped.
final class Busy: @unchecked Sendable {
    private let lock = NSLock()
    private var done = false

    init(threads: Int) {
        for _ in 0..<threads {
            let t = Thread { [self] in
                var x = 0
                while !lock.withLock({ done }) {
                    for i in 0..<10_000 { x &+= i }
                }
                _ = x
            }
            t.qualityOfService = .userInitiated
            t.start()
        }
    }

    func stop() { lock.withLock { done = true } }
}
