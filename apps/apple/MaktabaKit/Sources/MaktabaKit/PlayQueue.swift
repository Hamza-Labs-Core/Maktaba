import Foundation

// Epic 56 stories 56.02, 56.11 and 56.12: the server play queue (story 56.01) as the Apple
// TV and the iPhone, iPad and Mac Players use it. Every Play and Shuffle makes a queue on
// the home (POST /api/me/play-queues) from a source; the app keeps the window around the
// current item, moves with PUT …/current (a 409 stale_queue takes the answer's state) and
// reads the next window when within `PlayQueueModel.refillWithin` items of its edge.

/// Where a queue's videos come from.
public struct QueueSource: Codable, Hashable, Sendable {
    /// library, series, season, collection, playlist or video
    public let kind: String
    public let id: String
    /// A library's grid filters, sort and order (the keys of `GET /api/videos`).
    public let filters: [String: String]?

    public init(kind: String, id: String, filters: [String: String]? = nil) {
        self.kind = kind
        self.id = id
        self.filters = filters
    }
}

public struct QueueItem: Decodable, Identifiable, Hashable, Sendable {
    public var id: String { videoID }
    public let pos: Int
    public let origin: String
    public let videoID: String
    public let title: String?
    public let filename: String
    public let durationSec: Double?
    public let positionSec: Double?
    public let completed: Bool?
    public let seriesID: String?
    public let seriesName: String?

    public var name: String { (title?.isEmpty == false ? title : nil) ?? filename }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        func k(_ s: String) -> AnyKey { AnyKey(stringValue: s) }
        pos = c.lenient(k("pos")) ?? 0
        origin = c.lenient(k("origin")) ?? "source"
        videoID = try c.decode(String.self, forKey: k("id"))
        title = c.lenient(k("title"))
        filename = c.lenient(k("filename")) ?? ""
        durationSec = c.lenient(k("duration_sec"))
        positionSec = c.lenient(k("position_sec"))
        completed = c.lenient(k("completed"))
        seriesID = c.lenient(k("series_id"))
        seriesName = c.lenient(k("series_name"))
    }
}

/// A queue and its window, as the home answers it.
public struct QueueDoc: Decodable, Sendable {
    public let id: String
    public let source: QueueSource
    public let shuffle: Bool
    public let currentPos: Int
    public let version: Int
    public let deviceID: String?
    public let sourceDone: Bool
    public let usedAt: Date?
    public let items: [QueueItem]

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        func k(_ s: String) -> AnyKey { AnyKey(stringValue: s) }
        id = try c.decode(String.self, forKey: k("id"))
        source = try c.decode(QueueSource.self, forKey: k("source"))
        shuffle = c.lenient(k("shuffle")) ?? false
        currentPos = c.lenient(k("current_pos")) ?? 0
        version = c.lenient(k("version")) ?? 1
        deviceID = c.lenient(k("device_id"))
        sourceDone = c.lenient(k("source_done")) ?? false
        usedAt = c.lenient(k("used_at"))
        items = c.lenientList(k("items"))
    }

    public var current: QueueItem? { items.first { $0.pos == currentPos } }
    public func after(_ pos: Int) -> QueueItem? { items.filter { $0.pos > pos }.min { $0.pos < $1.pos } }
    public func before(_ pos: Int) -> QueueItem? { items.filter { $0.pos < pos }.max { $0.pos < $1.pos } }
}

/// One call to the home: the answer's body; a refusal throws `APIError.status`.
public protocol QueueTransport: Sendable {
    func queueCall(_ method: String, _ path: String, json: Data?) async throws -> Data
}

extension APIClient: QueueTransport {
    public func queueCall(_ method: String, _ path: String, json: Data?) async throws -> Data {
        var path = path
        var query: [String: String] = [:]
        if let q = path.firstIndex(of: "?") {
            for pair in path[path.index(after: q)...].split(separator: "&") {
                let kv = pair.split(separator: "=", maxSplits: 1)
                if kv.count == 2 { query[String(kv[0])] = String(kv[1]) }
            }
            path = String(path[..<q])
        }
        var r = request(method, path, query: query)
        if let json {
            r.httpBody = json
            r.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        return try await sendRaw(r)
    }
}

/// The source has nothing to play (422 empty_source).
public struct EmptySource: Error {}

@MainActor @Observable
public final class PlayQueueModel {
    public static let path = "/api/me/play-queues"
    /// The window is read again when fewer items than this are left after the current one.
    public static let refillWithin = 5

    private let transport: QueueTransport
    private let deviceID: String
    /// The queue being played, its window around the current item.
    public private(set) var doc: QueueDoc?
    /// A move found the queue moved on another device (the app says so once).
    public var staleSeen = false

    public init(transport: QueueTransport, deviceID: String) {
        self.transport = transport
        self.deviceID = deviceID
    }

    private func call(_ method: String, _ path: String, body: [String: Any]?) async throws -> Data {
        let json = try body.map { try JSONSerialization.data(withJSONObject: $0) }
        return try await transport.queueCall(method, path, json: json)
    }

    private func decode(_ data: Data) throws -> QueueDoc {
        do { return try JSONDecoder.home.decode(QueueDoc.self, from: data) } catch { throw APIError.decoding(String(describing: error)) }
    }

    /// Makes a queue from `source`; the item to play first.
    @discardableResult
    public func create(_ source: QueueSource, shuffle: Bool, startVideo: String? = nil) async throws -> QueueItem? {
        var src: [String: Any] = ["kind": source.kind, "id": source.id]
        if let f = source.filters { src["filters"] = f }
        var body: [String: Any] = ["source": src, "shuffle": shuffle, "device_id": deviceID]
        if let startVideo { body["start_video_id"] = startVideo }
        do {
            let d = try decode(try await call("POST", Self.path, body: body))
            doc = d
            return d.current ?? d.items.first
        } catch let APIError.status(422, text, _) where text.contains("empty_source") {
            throw EmptySource()
        }
    }

    /// Continues a queue read elsewhere (Home's Continue your queue).
    public func resume(_ d: QueueDoc) -> QueueItem? {
        doc = d
        return d.current
    }

    /// The caller's latest queue from any device, nil when none.
    public func fetchCurrent() async throws -> QueueDoc? {
        let data = try await call("GET", "\(Self.path)/current", body: nil)
        return data.isEmpty ? nil : try decode(data)
    }

    /// The item after the current one (reading on when the window runs out), without moving.
    public func peekNext() async -> QueueItem? {
        guard let d = doc else { return nil }
        if let n = d.after(d.currentPos) { return n }
        return await refill(d)?.after(d.currentPos)
    }

    /// Moves to the next item; nil at the end.
    public func next() async throws -> QueueItem? {
        guard let n = await peekNext() else { return nil }
        return try await move(to: n.pos)
    }

    /// Moves to the item before; nil at the start.
    public func previous() async throws -> QueueItem? {
        guard let d = doc else { return nil }
        let p: QueueItem?
        if let b = d.before(d.currentPos) { p = b } else { p = await refill(d, before: 10, after: 0)?.before(d.currentPos) }
        guard let p else { return nil }
        return try await move(to: p.pos)
    }

    /// Sets the current item for the version held; a stale one takes the latest.
    public func move(to pos: Int) async throws -> QueueItem? {
        guard let d = doc else { return nil }
        do {
            let moved = try decode(try await call("PUT", "\(Self.path)/\(d.id)/current", body: ["pos": pos, "version": d.version]))
            doc = moved
            let ahead = moved.items.filter { $0.pos > moved.currentPos }.count
            if ahead < Self.refillWithin, !moved.sourceDone { _ = await refill(moved) }
            return doc?.current
        } catch let APIError.status(409, text, _) {
            staleSeen = true
            if let data = text.data(using: .utf8),
               let wrapper = try? JSONDecoder.home.decode(StaleAnswer.self, from: data) {
                doc = wrapper.queue
            } else {
                _ = await refill(d)
            }
            return doc?.current
        }
    }

    /// Play next (`where` "next") or Add to queue ("end"); a queue starts when none plays.
    public func add(_ videos: [String], where place: String) async throws {
        guard let first = videos.first else { return }
        var ids = videos
        if doc == nil {
            try await create(QueueSource(kind: "video", id: first), shuffle: false)
            ids.removeFirst()
            if ids.isEmpty { return }
        }
        guard let d = doc else { return }
        doc = try decode(try await call("POST", "\(Self.path)/\(d.id)/items", body: ["video_ids": ids, "where": place]))
    }

    /// Shuffle again: a new order after the current item.
    public func reshuffle() async throws {
        guard let d = doc, d.shuffle else { return }
        doc = try decode(try await call("POST", "\(Self.path)/\(d.id)/reshuffle", body: nil))
    }

    private func refill(_ d: QueueDoc, before: Int = 10, after: Int = 30) async -> QueueDoc? {
        guard let data = try? await call("GET", "\(Self.path)/\(d.id)?around=\(d.currentPos)&before=\(before)&after=\(after)", body: nil),
              let w = try? decode(data) else { return nil }
        doc = w
        return w
    }
}

private struct StaleAnswer: Decodable {
    let queue: QueueDoc
}
