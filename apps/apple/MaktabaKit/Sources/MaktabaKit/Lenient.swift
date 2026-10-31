import Foundation

// Reading what a home reports about its running work (its updates, its processes, its
// running jobs) tolerantly: an older home leaves fields out, a newer one adds states and
// shapes. A field of the wrong shape reads as absent, a list element that cannot be read is
// left out, and a state is kept as the home's own string (shown raw when the app has no
// words for it), so one odd value never hides the whole screen.

extension KeyedDecodingContainer {
    /// The value at `key`, nil when it is absent, null or of another shape.
    func lenient<T: Decodable>(_ key: Key) -> T? {
        (try? decodeIfPresent(T.self, forKey: key)) ?? nil
    }

    /// The list at `key` without the elements that cannot be read (empty when absent).
    func lenientList<T: Decodable>(_ key: Key) -> [T] {
        guard var c = try? nestedUnkeyedContainer(forKey: key) else { return [] }
        var out: [T] = []
        while !c.isAtEnd {
            if let v = try? c.decode(T.self) {
                out.append(v)
            } else if (try? c.decode(Skipped.self)) == nil {
                break
            }
        }
        return out
    }

    /// The string fields of the object at `key` (numbers and booleans as text; nested
    /// objects and lists left out): an error or a detail whose shape varies by its code.
    func lenientFields(_ key: Key) -> [String: String] {
        ((try? decodeIfPresent(LooseFields.self, forKey: key)) ?? nil)?.fields ?? [:]
    }
}

/// Anything, read and dropped: a list element that cannot be read as the type asked.
private struct Skipped: Decodable {
    init(from decoder: Decoder) throws {}
}

/// An object's scalar fields as text.
struct LooseFields: Decodable {
    var fields: [String: String] = [:]

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        for key in c.allKeys {
            if let s = try? c.decode(String.self, forKey: key) {
                fields[key.stringValue] = s
            } else if let i = try? c.decode(Int.self, forKey: key) {
                fields[key.stringValue] = String(i)
            } else if let d = try? c.decode(Double.self, forKey: key) {
                fields[key.stringValue] = String(d)
            } else if let b = try? c.decode(Bool.self, forKey: key) {
                fields[key.stringValue] = String(b)
            }
        }
    }
}

struct AnyKey: CodingKey {
    var stringValue: String
    var intValue: Int?
    init(stringValue: String) { self.stringValue = stringValue }
    init?(intValue: Int) {
        self.stringValue = String(intValue)
        self.intValue = intValue
    }
}

/// A word for a value the home sends (a state, a step, a reason): the app's own when it
/// has one, else the value itself with its underscores as spaces (a newer home's value),
/// as the web's `useWords` does.
func words(_ table: [String: String], _ value: String) -> String {
    table[value] ?? value.replacingOccurrences(of: "_", with: " ")
}
