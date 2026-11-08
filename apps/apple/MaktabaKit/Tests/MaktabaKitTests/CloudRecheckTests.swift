import Foundation
import Testing
@testable import MaktabaKit

/// Epic 50 story 50.11 §4: the app reads Maktaba Cloud's status at once when a request fails
/// as the relay fails (no answer, 502, 503, 504), only for a home reached through the cloud,
/// and at most once every 10 s however many requests fail in an outage. In the stub-home suite:
/// RouteStub and the recheck's last time are shared.
extension AgainstAStubHome {
    @MainActor @Suite(.serialized) struct CloudRecheck {
        static let relayHome = URL(string: "https://h1.relay.maktaba.tv")!

        @Test func onlyTheRelayFailingAHomeReachedThroughTheCloudAsks() {
            CloudStatusRules.resetRecheck()
            defer { CloudStatusRules.resetRecheck() }
            let center = NotificationCenter()
            let t = Date(timeIntervalSince1970: 1_000)
            for code in [200, 400, 401, 404, 429, 500] {
                #expect(!CloudStatusRules.noteRequestFailed(home: Self.relayHome, status: code, now: t, center: center), "\(code)")
            }
            #expect(!CloudStatusRules.noteRequestFailed(home: URL(string: "http://192.168.1.20:8088")!, status: 502, now: t, center: center))
            #expect(CloudStatusRules.noteRequestFailed(home: Self.relayHome, status: 502, now: t, center: center))
        }

        @Test func atMostOnceEvery10Seconds() {
            CloudStatusRules.resetRecheck()
            defer { CloudStatusRules.resetRecheck() }
            let center = NotificationCenter()
            nonisolated(unsafe) var posted = 0
            let watch = center.addObserver(forName: CloudStatusRules.recheckRequested, object: nil, queue: nil) { _ in posted += 1 }
            defer { center.removeObserver(watch) }
            let t = Date(timeIntervalSince1970: 1_000)
            #expect(CloudStatusRules.noteRequestFailed(home: Self.relayHome, status: 0, now: t, center: center))
            #expect(!CloudStatusRules.noteRequestFailed(home: Self.relayHome, status: 503, now: t + 4, center: center))
            #expect(!CloudStatusRules.noteRequestFailed(home: Self.relayHome, status: 504, now: t + 9.9, center: center))
            #expect(CloudStatusRules.noteRequestFailed(home: Self.relayHome, status: 504, now: t + 10, center: center))
            #expect(posted == 2)
        }

        @Test func aRequestTheRelayFailsAsksForTheFeed() async throws {
            RouteStub.reset()
            CloudStatusRules.resetRecheck()
            defer { CloudStatusRules.resetRecheck() }
            RouteStub.on("GET h1.relay.maktaba.tv/api/libraries", .json(502, #"{"error":"bad_gateway","message":""}"#))
            let app = "test.cloud.recheck"
            defer { TokenStore(app: app, home: Self.relayHome).clear() }
            let c = APIClient(home: Self.relayHome, tokens: TokenStore(app: app, home: Self.relayHome), session: RouteStub.session())
            nonisolated(unsafe) var asked = 0
            let watch = NotificationCenter.default.addObserver(forName: CloudStatusRules.recheckRequested, object: nil, queue: nil) { _ in asked += 1 }
            defer { NotificationCenter.default.removeObserver(watch) }
            await #expect(throws: APIError.self) { _ = try await c.sendRaw(await c.request("GET", "/api/libraries"), authorized: false) }
            #expect(asked == 1, "the 502 asked for the cloud's status")
        }

        @Test func theWatcherReadsAgainAtOnceWhenAsked() async throws {
            RouteStub.reset()
            RouteStub.on("GET status.maktaba.tv/summary.json",
                         .json(200, #"{"components":{"relay":"operational","api":"operational"}}"#),
                         .json(200, #"{"components":{"relay":"outage","api":"operational"}}"#))
            let center = NotificationCenter()
            let watcher = CloudStatusWatcher(session: RouteStub.session())
            let running = Task { await watcher.run(every: .seconds(300), center: center) }
            defer { running.cancel() }
            for _ in 0..<100 where watcher.summary == nil { try await Task.sleep(for: .milliseconds(20)) }
            #expect(watcher.summary?.components["relay"] == "operational")
            // a failed request asks (posted until the watcher, between reads, is listening)
            for _ in 0..<150 where watcher.summary?.components["relay"] != "outage" {
                center.post(name: CloudStatusRules.recheckRequested, object: nil)
                try await Task.sleep(for: .milliseconds(20))
            }
            #expect(watcher.summary?.components["relay"] == "outage", "read again well before 5 minutes")
            #expect(RouteStub.seen.filter { $0.route == "GET status.maktaba.tv/summary.json" }.count == 2)
        }
    }
}
