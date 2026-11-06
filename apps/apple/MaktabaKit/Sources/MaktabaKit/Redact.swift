import Foundation

/// The shared redaction list (`shared/redact/rules.json`, Epic 53 story 53.01): the rules every
/// Maktaba logger uses, and the cases they all pass (`shared/redact/cases.json`). `RedactRules.swift`
/// holds the copy `tools/log-schema/sync.py` writes. A replacement names its groups as `$1`, which
/// NSRegularExpression's templates read as is.
public enum Redact {
    struct Rule {
        let regex: NSRegularExpression
        let template: String
        let needs: [String]
        let ignoreCase: Bool
    }

    struct Rules {
        let keyMask: String
        let keys: Set<String>
        let suffixes: [String]
        let parts: [String]
        let values: [Rule]
        let cards: NSRegularExpression
        let cardFirst: String
        let cardMin: Int
        let cardMax: Int
        let cardReplace: String
        let ipv4: NSRegularExpression
        let ipv4Keep: Set<String>
        let ipv6: NSRegularExpression
    }

    static let rules: Rules = {
        let data = Data(redactRulesJSON.utf8)
        guard let raw = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any] else {
            fatalError("RedactRules.swift is not JSON: run tools/log-schema/sync.py")
        }
        func re(_ p: Any?, _ ic: Bool = false) -> NSRegularExpression {
            // the shared patterns compile everywhere; a broken copy fails its tests
            try! NSRegularExpression(pattern: p as? String ?? "", options: ic ? [.caseInsensitive] : [])
        }
        let values = (raw["values"] as? [[String: Any]] ?? []).map { v -> Rule in
            let ic = v["ignore_case"] as? Bool ?? false
            let needs = (v["needs"] as? [String] ?? []).map { ic ? $0.lowercased() : $0 }
            return Rule(regex: re(v["pattern"], ic), template: v["replace"] as? String ?? "", needs: needs, ignoreCase: ic)
        }
        let cards = raw["cards"] as? [String: Any] ?? [:]
        let ipv4 = raw["ipv4"] as? [String: Any] ?? [:]
        let ipv6 = raw["ipv6"] as? [String: Any] ?? [:]
        return Rules(
            keyMask: raw["key_mask"] as? String ?? "***REDACTED***",
            keys: Set((raw["keys"] as? [String] ?? []).map { $0.lowercased() }),
            suffixes: raw["key_suffixes"] as? [String] ?? [],
            parts: raw["key_parts_string_only"] as? [String] ?? [],
            values: values,
            cards: re(cards["pattern"]),
            cardFirst: cards["first_digits"] as? String ?? "",
            cardMin: cards["min_digits"] as? Int ?? 15,
            cardMax: cards["max_digits"] as? Int ?? 16,
            cardReplace: cards["replace"] as? String ?? "[CARD]",
            ipv4: re(ipv4["pattern"]),
            ipv4Keep: Set(ipv4["keep_first_octet"] as? [String] ?? []),
            ipv6: re(ipv6["pattern"])
        )
    }()

    /// The mask a whole sensitive field gets.
    public static var keyMask: String { rules.keyMask }

    /// Whether a field named `key` is masked whole; `isString` says whether its value is a string.
    public static func keySensitive(_ key: String, isString: Bool) -> Bool {
        let low = key.lowercased()
        if rules.keys.contains(low) || rules.suffixes.contains(where: { low.hasSuffix($0) }) { return true }
        return isString && rules.parts.contains(where: { low.contains($0) })
    }

    /// `text` with the shared value rules applied: credentials, emails, home folders, cards, IPs.
    public static func scrub(_ text: String) -> String {
        if text.isEmpty { return text }
        var out = text
        var low = out.lowercased()
        for r in rules.values {
            let hay = r.ignoreCase ? low : out
            if !r.needs.isEmpty && !r.needs.contains(where: { hay.contains($0) }) { continue }
            let ns = out as NSString
            let next = r.regex.stringByReplacingMatches(in: out, range: NSRange(location: 0, length: ns.length), withTemplate: r.template)
            if next != out {
                out = next
                low = out.lowercased()
            }
        }
        var digits = 0, dots = 0, colons = 0
        for c in out.unicodeScalars {
            if c >= "0" && c <= "9" { digits += 1 } else if c == "." { dots += 1 } else if c == ":" { colons += 1 }
        }
        if digits >= rules.cardMin { out = replaceChecked(out, rules.cards, card) }
        if dots >= 3 && digits >= 4 { out = replaceChecked(out, rules.ipv4, v4) }
        if colons >= 3 { out = replaceChecked(out, rules.ipv6, v6) }
        return out
    }

    /// A flat attribute map with masked keys and scrubbed strings.
    public static func attrs(_ a: [String: Any]) -> [String: Any] {
        var out: [String: Any] = [:]
        for (k, v) in a {
            if keySensitive(k, isString: v is String) { out[k] = keyMask } else if let s = v as? String { out[k] = scrub(s) } else { out[k] = v }
        }
        return out
    }

    private static func replaceChecked(_ text: String, _ re: NSRegularExpression, _ fn: (String, Character?, Character?) -> String) -> String {
        let ns = text as NSString
        let matches = re.matches(in: text, range: NSRange(location: 0, length: ns.length))
        if matches.isEmpty { return text }
        var out = ""
        var last = 0
        for m in matches {
            let r = m.range
            out += ns.substring(with: NSRange(location: last, length: r.location - last))
            let before: Character? = r.location > 0 ? Character(ns.substring(with: NSRange(location: r.location - 1, length: 1))) : nil
            let end = r.location + r.length
            let after: Character? = end < ns.length ? Character(ns.substring(with: NSRange(location: end, length: 1))) : nil
            out += fn(ns.substring(with: r), before, after)
            last = end
        }
        out += ns.substring(from: last)
        return out
    }

    private static func isDigit(_ c: Character?) -> Bool { c.map { $0 >= "0" && $0 <= "9" } ?? false }
    private static func isHex(_ c: Character?) -> Bool { c.map { $0.isHexDigit && $0.isASCII } ?? false }

    private static func card(_ m: String, _ before: Character?, _ after: Character?) -> String {
        if isDigit(before) || isDigit(after) { return m }
        let d = m.filter { $0 >= "0" && $0 <= "9" }.compactMap { $0.wholeNumberValue }
        guard d.count >= rules.cardMin, d.count <= rules.cardMax, let first = d.first,
              rules.cardFirst.contains(String(first)) else { return m }
        var sum = 0
        for (i, n) in d.reversed().enumerated() {
            var x = n
            if i % 2 == 1 { x *= 2; if x > 9 { x -= 9 } }
            sum += x
        }
        return sum % 10 == 0 ? rules.cardReplace : m
    }

    private static func v4(_ m: String, _ before: Character?, _ after: Character?) -> String {
        // part of a longer number (a version like 1.2.3.4.5 is cut from its start only)
        if isDigit(before) || before == "." || isDigit(after) { return m }
        let p = m.split(separator: ".").map(String.init)
        if p.count != 4 || p.contains(where: { (Int($0) ?? 256) > 255 }) || rules.ipv4Keep.contains(p[0]) { return m }
        return "\(p[0]).\(p[1]).\(p[2]).0"
    }

    static func ipv6Groups(_ m: String) -> [String]? {
        if m.contains(":::") || m.components(separatedBy: "::").count > 2 { return nil }
        var groups: [String]
        if let r = m.range(of: "::") {
            let l = String(m[..<r.lowerBound]), rt = String(m[r.upperBound...])
            let left = l.isEmpty ? [] : l.components(separatedBy: ":")
            let right = rt.isEmpty ? [] : rt.components(separatedBy: ":")
            if left.count + right.count > 7 { return nil }
            groups = left + Array(repeating: "0", count: 8 - left.count - right.count) + right
        } else {
            groups = m.components(separatedBy: ":")
            if groups.count != 8 { return nil }
        }
        return groups.contains("") ? nil : groups
    }

    private static func v6(_ m: String, _ before: Character?, _ after: Character?) -> String {
        if isHex(before) || before == ":" || isHex(after) || after == ":" { return m }
        guard let g = ipv6Groups(m) else { return m }
        if g.prefix(7).allSatisfy({ UInt16($0, radix: 16) == 0 }) && UInt16(g[7], radix: 16) == 1 { return m }
        return "\(g[0]):\(g[1]):\(g[2])::"
    }
}
