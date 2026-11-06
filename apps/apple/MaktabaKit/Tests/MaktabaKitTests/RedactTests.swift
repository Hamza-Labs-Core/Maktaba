import Foundation
import Testing
@testable import MaktabaKit

/// Epic 53 story 53.01: the Apple apps' redaction passes the shared cases (T01), and the on-device
/// ring keeps the newest lines in under 1 MB (T06), each in the shared schema's shape (T02).
@Suite struct RedactTests {
    static let repo = URL(fileURLWithPath: #filePath)
        .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()

    static func cases() throws -> [String: Any] {
        let data = try Data(contentsOf: repo.appendingPathComponent("shared/redact/cases.json"))
        return try #require(try JSONSerialization.jsonObject(with: data) as? [String: Any])
    }

    @Test func theCopyIsTheSharedFile() throws {
        let shared = try String(contentsOf: Self.repo.appendingPathComponent("shared/redact/rules.json"), encoding: .utf8)
        #expect(redactRulesJSON == shared)
    }

    @Test func scrubsEverySharedCase() throws {
        let scrub = try #require(try Self.cases()["scrub"] as? [[String: String]])
        for c in scrub {
            #expect(Redact.scrub(c["in"] ?? "") == c["out"], "\(c["in"] ?? "")")
        }
    }

    @Test func masksTheSharedKeys() throws {
        let keys = try #require(try Self.cases()["keys"] as? [[String: Any]])
        for c in keys {
            let key = c["key"] as? String ?? ""
            #expect(Redact.keySensitive(key, isString: c["string"] as? Bool ?? false) == (c["masked"] as? Bool ?? false), "\(key)")
        }
    }

    @Test func theRingKeepsTheNewestLinesUnderOneMegabyte() throws {
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent("logring-\(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: dir) }
        let ring = LogRing(directory: dir, base: ["service": "apple.player", "app_version": "1.0", "platform": "ios"])
        for i in 0..<3000 {
            ring.write(level: "info", msg: "tick", attrs: ["i": i, "pad": String(repeating: "x", count: 300)])
        }
        ring.write(level: "error", msg: "screen_failed", attrs: ["url": "/a?token=abc", "who": "a@b.co"])
        let total = [ring.current, ring.previous].compactMap { try? FileManager.default.attributesOfItem(atPath: $0.path)[.size] as? Int }.reduce(0, +)
        #expect(total <= 1024 * 1024)
        let lines = ring.lines()
        #expect(lines.count > 1000 && lines.count < 3001)
        let lastLine = try #require(lines.last)
        let last = try #require(try JSONSerialization.jsonObject(with: Data(lastLine.utf8)) as? [String: Any])
        #expect(last["msg"] as? String == "screen_failed")
        #expect(last["level"] as? String == "error")
        #expect(last["service"] as? String == "apple.player")
        #expect((last["ts"] as? String)?.hasSuffix("Z") == true)
        let attrs = try #require(last["attrs"] as? [String: Any])
        #expect(attrs["url"] as? String == "/a?token=[REDACTED]")
        #expect(attrs["who"] as? String == "[EMAIL]")
        // the oldest went first: the first kept line is not line 0
        let firstLine = try #require(lines.first)
        let first = try #require(try JSONSerialization.jsonObject(with: Data(firstLine.utf8)) as? [String: Any])
        #expect(((first["attrs"] as? [String: Any])?["i"] as? Int ?? 0) > 0)
    }
}
