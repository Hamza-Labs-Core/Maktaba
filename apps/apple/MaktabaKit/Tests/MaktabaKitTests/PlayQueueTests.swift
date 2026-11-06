import Foundation
import Testing
@testable import MaktabaKit

/// Epic 56 story 56.02 T08: the queue model's requests and window, against a fake home.
@MainActor
struct PlayQueueTests {
    final class Fake: QueueTransport, @unchecked Sendable {
        var calls: [(String, String, [String: Any]?)] = []
        var answers: [Result<String, Error>] = []
        func queueCall(_ method: String, _ path: String, json: Data?) async throws -> Data {
            calls.append((method, path, json.flatMap { try? JSONSerialization.jsonObject(with: $0) as? [String: Any] }))
            switch answers.removeFirst() {
            case .success(let s): return Data(s.utf8)
            case .failure(let e): throw e
            }
        }
    }

    static func win(_ cur: Int, _ ids: [String], version: Int = 1, shuffle: Bool = false, done: Bool = false) -> String {
        let items = ids.enumerated().map { #"{"pos":\#($0.offset),"id":"\#($0.element)","title":"T\#($0.element)"}"# }.joined(separator: ",")
        return #"{"id":"Q","source":{"kind":"library","id":"L"},"shuffle":\#(shuffle),"current_pos":\#(cur),"version":\#(version),"source_done":\#(done),"items":[\#(items)]}"#
    }

    @Test func shuffleCreatesAndNextMovesWithTheVersion() async throws {
        let f = Fake()
        let m = PlayQueueModel(transport: f, deviceID: "tv-1")
        f.answers = [.success(Self.win(0, ["a", "b", "c", "d", "e", "f", "g"], shuffle: true))]
        let first = try await m.create(QueueSource(kind: "library", id: "L"), shuffle: true)
        #expect(first?.videoID == "a")
        #expect(f.calls[0].0 == "POST" && (f.calls[0].2?["shuffle"] as? Bool) == true)
        #expect(((f.calls[0].2?["source"] as? [String: Any])?["kind"] as? String) == "library")
        f.answers = [.success(Self.win(1, ["a", "b", "c", "d", "e", "f", "g"], version: 2, shuffle: true))]
        #expect(try await m.next()?.videoID == "b")
        #expect(f.calls[1].1 == "/api/me/play-queues/Q/current")
        #expect((f.calls[1].2?["version"] as? Int) == 1 && (f.calls[1].2?["pos"] as? Int) == 1)
    }

    @Test func nearTheEdgeTheNextWindowIsRead() async throws {
        let f = Fake()
        let m = PlayQueueModel(transport: f, deviceID: "tv-1")
        f.answers = [.success(Self.win(0, ["a", "b", "c"])), .success(Self.win(1, ["a", "b", "c"], version: 2)),
                     .success(Self.win(1, ["a", "b", "c", "d"], version: 2))]
        try await m.create(QueueSource(kind: "series", id: "S"), shuffle: false)
        #expect(try await m.next()?.videoID == "b")
        #expect(f.calls.last?.0 == "GET" && f.calls.last?.1.hasPrefix("/api/me/play-queues/Q?around=1") == true)
    }

    @Test func aStaleMoveTakesTheLatest() async throws {
        let f = Fake()
        let m = PlayQueueModel(transport: f, deviceID: "tv-1")
        f.answers = [.success(Self.win(0, ["a", "b"])),
                     .failure(APIError.status(409, #"{"title":"stale_queue","queue":\#(Self.win(1, ["a", "b"], version: 5, done: true))}"#))]
        try await m.create(QueueSource(kind: "library", id: "L"), shuffle: false)
        #expect(try await m.move(to: 1)?.videoID == "b")
        #expect(m.staleSeen && m.doc?.version == 5)
    }

    @Test func emptySourceReshuffleAndAdd() async throws {
        let f = Fake()
        let m = PlayQueueModel(transport: f, deviceID: "tv-1")
        f.answers = [.failure(APIError.status(422, #"{"title":"empty_source"}"#))]
        await #expect(throws: EmptySource.self) { try await m.create(QueueSource(kind: "library", id: "L"), shuffle: false) }
        f.answers = [.success(Self.win(0, ["a", "b"], shuffle: true, done: true)), .success(Self.win(0, ["a", "x"], shuffle: true, done: true)),
                     .success(Self.win(0, ["a", "n", "x"], shuffle: true, done: true))]
        try await m.create(QueueSource(kind: "library", id: "L"), shuffle: true)
        try await m.reshuffle()
        #expect(f.calls.last?.1 == "/api/me/play-queues/Q/reshuffle")
        try await m.add(["n"], where: "next")
        #expect((f.calls.last?.2?["where"] as? String) == "next")
        f.answers = [.success("")]
        #expect(try await m.fetchCurrent() == nil)
    }
}
