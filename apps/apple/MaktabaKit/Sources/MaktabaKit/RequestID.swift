import Foundation

/// The request id every call to the home carries (Epic 53 story 53.08): `X-Request-Id`, a
/// UUIDv7 the app makes; calls within a second of each other (one screen's loads) share one,
/// so the home's lines for them can be followed through the API, the pipeline and streaming.
/// The last 20 are kept, by route, for a problem report.
public enum RequestID {
    public static let header = "X-Request-Id"
    /// How long calls count as one action.
    public static let window: TimeInterval = 1

    nonisolated(unsafe) private static var current: (id: String, at: Date)?
    nonisolated(unsafe) private static var kept: [String] = []
    private static let lock = NSLock()

    /// The current action's id: the one of a call within the last second, else a new one.
    public static func next(now: Date = Date()) -> String {
        lock.lock()
        defer { lock.unlock() }
        if let c = current, now.timeIntervalSince(c.at) <= window {
            current = (c.id, now)
            return c.id
        }
        let id = v7(now)
        current = (id, now)
        kept.append(id)
        if kept.count > 20 { kept.removeFirst(kept.count - 20) }
        return id
    }

    /// The last actions' ids, oldest first.
    public static func recent() -> [String] {
        lock.lock()
        defer { lock.unlock() }
        return kept
    }

    /// The short reference an error screen shows: the id's first 8 characters.
    public static func short(_ id: String) -> String { String(id.prefix(8)) }

    /// A UUIDv7: 48 bits of milliseconds, random bits, version 7, variant 10.
    public static func v7(_ now: Date = Date()) -> String {
        var b = [UInt8](repeating: 0, count: 16)
        for i in 0..<16 { b[i] = UInt8.random(in: 0...255) }
        var ms = UInt64(max(0, now.timeIntervalSince1970 * 1000))
        for i in stride(from: 5, through: 0, by: -1) {
            b[i] = UInt8(ms & 0xff)
            ms >>= 8
        }
        b[6] = (b[6] & 0x0f) | 0x70
        b[8] = (b[8] & 0x3f) | 0x80
        let h = b.map { String(format: "%02x", $0) }.joined()
        let c = Array(h)
        return String(c[0..<8]) + "-" + String(c[8..<12]) + "-" + String(c[12..<16]) + "-" + String(c[16..<20]) + "-" + String(c[20..<32])
    }
}
