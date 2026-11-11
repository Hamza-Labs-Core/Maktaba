import Foundation
import Testing

/// Epic 60 story 60.06 (T07): every problem type and code the Apple apps compare against is in
/// the registry (shared/api/problems.json), so each has its page on maktaba.tv and the API sends it.
///
/// Types: every `maktaba.tv/problems/<slug>` in the apps' sources and every literal compared with
/// a `slug`. Codes: every literal compared with `code` or `reason` in the problem classifier (`classifiers`)
/// and every `problem?.code == "…"` anywhere.
@Suite struct ProblemRegistry {
    /// The checkout's root: this file is apps/apple/MaktabaKit/Tests/MaktabaKitTests/….
    static let repo = URL(fileURLWithPath: #filePath)
        .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()

    /// The files that sort the home's problems into what a person reads.
    static let classifiers: Set<String> = ["apps/apple/MaktabaKit/Sources/MaktabaKit/HomeErrors.swift"]

    /// Codes compared there that are not the API's problem codes, each with why.
    static let notProblemCodes: [String: String] = [
        "relay_needs_plan": "Maktaba Cloud's relay names it in its own `error` field (story 50.19), not an API problem",
        "stream_limit": "Maktaba Cloud's relay names it in its own `error` field (story 50.19), not an API problem",
    ]

    struct Registry: Decodable {
        struct Entry: Decodable { let slug: String; let codes: [String] }
        let problems: [Entry]
    }

    static func matches(_ pattern: String, in text: String) -> [String] {
        let re = try! NSRegularExpression(pattern: pattern)
        return re.matches(in: text, range: NSRange(text.startIndex..., in: text)).compactMap {
            Range($0.range(at: 1), in: text).map { String(text[$0]) }
        }
    }

    /// The apps' Swift sources (apps/apple and the TV app), no test target.
    static func sources() -> [(path: String, text: String)] {
        var out: [(String, String)] = []
        for dir in ["apps/apple", "apps/tv/tvos"] {
            let root = repo.appendingPathComponent(dir)
            guard let walk = FileManager.default.enumerator(at: root, includingPropertiesForKeys: nil) else { continue }
            for case let url as URL in walk where url.pathExtension == "swift" {
                let rel = String(url.path.dropFirst(repo.path.count + 1))
                let parts = rel.split(separator: "/")
                if parts.contains(where: { $0.hasSuffix("Tests") || $0 == ".build" || $0 == "build" }) { continue }
                if let text = try? String(contentsOf: url, encoding: .utf8) { out.append((rel, text)) }
            }
        }
        return out
    }

    @Test func everyComparedTypeAndCodeIsRegistered() throws {
        let data = try Data(contentsOf: Self.repo.appendingPathComponent("shared/api/problems.json"))
        let registry = try JSONDecoder().decode(Registry.self, from: data)
        let slugs = Set(registry.problems.map(\.slug))
        let codes = Set(registry.problems.flatMap(\.codes))

        let files = Self.sources()
        #expect(files.count > 100, "found no sources under apps/apple: the scan is broken")
        var types: [String: String] = [:]
        var found: [String: String] = [:]
        for (path, text) in files {
            for s in Self.matches(#"maktaba\.tv/problems/([a-z0-9_-]+)"#, in: text)
                + Self.matches(#"\b[sS]lug\s*==\s*"([a-z0-9_-]+)""#, in: text) where types[s] == nil {
                types[s] = path
            }
            var cs = Self.matches(#"problem\??\.code\s*==\s*"([a-z0-9_]+)""#, in: text)
            if Self.classifiers.contains(path) { cs += Self.matches(#"\b(?:code|reason)\s*==\s*"([a-z0-9_]+)""#, in: text) }
            for c in cs where found[c] == nil { found[c] = path }
        }
        #expect(!types.isEmpty, "found no problem type in the apps: the scan is broken")
        #expect(!found.isEmpty, "found no problem code in the classifier: the scan is broken")

        let bad = types.filter { !slugs.contains($0.key) }.map { "type \($0.key) (\($0.value))" }
            + found.filter { !codes.contains($0.key) && Self.notProblemCodes[$0.key] == nil }.map { "code \($0.key) (\($0.value))" }
        #expect(bad.sorted() == [], "not in shared/api/problems.json (add the entry there, or, for a code that is no API problem's, to notProblemCodes with why)")
        let stale = Self.notProblemCodes.keys.filter { found[$0] == nil || codes.contains($0) }
        #expect(stale.sorted() == [], "notProblemCodes lists codes the apps no longer compare, or that are registered")
    }
}
