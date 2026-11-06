import Foundation

// Word review (Epic 52, story 52.13): the suspect words of every transcript, for a home admin
// in the Remote. Every route is the home admins' only (a library role grants nothing): anyone
// else is answered 403, which the screens show as "Only home admins can review words". Read
// tolerantly: a reason or status this build does not know is `.unknown`, a candidate that
// cannot be read is left out.

public enum ReviewReason: String, Sendable, CaseIterable {
    case lowConfidence = "low_confidence", notInDictionary = "not_in_dictionary"
    case listenersDisagree = "listeners_disagree", quranMismatch = "quran_mismatch"
    case namedEntity = "named_entity", nearVocabulary = "near_vocabulary", manual
    case previousFix = "previous_fix", overBudget = "over_budget", unknown

    public init(code: String) { self = ReviewReason(rawValue: code) ?? .unknown }
}

public enum ReviewStatus: String, Sendable {
    case open, accepted, corrected, name, dictionary, skipped, stale, unknown

    public init(code: String) { self = ReviewStatus(rawValue: code) ?? .unknown }
}

public enum ReviewAction: String, Sendable {
    case accept, correct, name, dictionary, skip
}

public struct ReviewCandidate: Decodable, Hashable, Sendable {
    public let text: String
    public let source: String
    public let score: Double

    enum CodingKeys: String, CodingKey { case text, source, score }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        text = try c.decode(String.self, forKey: .text)
        source = c.lenient(.source) ?? ""
        score = c.lenient(.score) ?? 0
    }
}

public struct QuranRef: Decodable, Hashable, Sendable {
    public let surah: Int?
    public let aya: Int?
    public let labelAr: String?
    public let labelEn: String?
    public let expected: String?
    public let missing: String?
    public let kind: String?

    enum CodingKeys: String, CodingKey {
        case surah, aya, expected, missing, kind
        case labelAr = "label_ar", labelEn = "label_en"
    }
}

public struct ReviewFlag: Decodable, Identifiable, Hashable, Sendable {
    public let id: String
    public let kind: String
    public let videoID: String
    public let videoTitle: String
    public let charStart: Int
    public let charLen: Int
    public let original: String
    public let context: String
    public let language: String
    public let direction: String
    public let reasonCodes: [String]
    public let candidates: [ReviewCandidate]
    public let status: ReviewStatus
    public let lockedBy: String?
    public let lockedByName: String?
    public let lockedUntil: String?
    public let before: String
    public let after: String
    public let sameKeyOpen: Int
    public let verse: QuranRef?
    public let reviewersNote: String

    enum CodingKeys: String, CodingKey {
        case id, kind, original, context, language, direction, reasons, candidates, status, before, after, verse
        case videoID = "video_id", videoTitle = "video_title", charStart = "char_start", charLen = "char_len"
        case lockedBy = "locked_by", lockedByName = "locked_by_name", lockedUntil = "locked_until"
        case sameKeyOpen = "same_key_open", reviewersNote = "reviewers_note"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        kind = c.lenient(.kind) ?? "word"
        videoID = c.lenient(.videoID) ?? ""
        videoTitle = c.lenient(.videoTitle) ?? ""
        charStart = c.lenient(.charStart) ?? 0
        charLen = c.lenient(.charLen) ?? 0
        original = c.lenient(.original) ?? ""
        context = c.lenient(.context) ?? ""
        language = c.lenient(.language) ?? ""
        direction = c.lenient(.direction) ?? "ltr"
        reasonCodes = c.lenientList(.reasons)
        candidates = c.lenientList(.candidates)
        status = ReviewStatus(code: c.lenient(.status) ?? "open")
        lockedBy = c.lenient(.lockedBy)
        lockedByName = c.lenient(.lockedByName)
        lockedUntil = c.lenient(.lockedUntil)
        before = c.lenient(.before) ?? ""
        after = c.lenient(.after) ?? ""
        sameKeyOpen = c.lenient(.sameKeyOpen) ?? 0
        verse = c.lenient(.verse)
        reviewersNote = c.lenient(.reviewersNote) ?? ""
    }

    public var reasons: [ReviewReason] { reasonCodes.map(ReviewReason.init(code:)) }
    public var rightToLeft: Bool { direction == "rtl" }
    public var isQuran: Bool { reasons.contains(.quranMismatch) }

    /// The sentence around the word, cut in Unicode code points as the API counts
    /// (Swift's Character is a grapheme, so the scalars are counted).
    public var parts: (before: String, word: String, after: String) {
        let scalars = Array(context.unicodeScalars)
        let a = min(max(0, charStart), scalars.count)
        let b = min(max(a, charStart + charLen), scalars.count)
        func str(_ s: ArraySlice<Unicode.Scalar>) -> String { String(String.UnicodeScalarView(s)) }
        return (str(scalars[0..<a]), str(scalars[a..<b]), str(scalars[b...]))
    }
}

public struct ReviewPage: Decodable, Sendable {
    public let items: [ReviewFlag]
    public let nextCursor: String?

    enum CodingKeys: String, CodingKey { case items, nextCursor = "next_cursor" }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        items = c.lenientList(.items)
        nextCursor = c.lenient(.nextCursor)
    }
}

public struct ReviewClip: Decodable, Sendable {
    public let url: String
    public let start: Double
    public let end: Double
}

public struct ReviewResult: Decodable, Sendable {
    public let status: String
    public let changed: Int
    public let editID: String?
    public let batchID: String?

    enum CodingKeys: String, CodingKey {
        case status, changed
        case editID = "edit_id", batchID = "batch_id"
    }
}

struct ReviewSummaryDoc: Decodable {
    let open: Int
}

public extension APIClient {
    /// `GET /api/review/flags`: the open words by priority.
    func reviewFlags(library: String? = nil, cursor: String? = nil) async throws -> ReviewPage {
        var q = ["limit": "50"]
        if let library { q["library"] = library }
        if let cursor { q["cursor"] = cursor }
        return try await get("/api/review/flags", query: q)
    }

    /// `GET /api/review/flags/{id}`: the word with its context, and a 5-minute lock.
    func reviewFlag(_ id: String) async throws -> ReviewFlag {
        try await get("/api/review/flags/\(Self.segment(id))")
    }

    /// `GET /api/review/flags/{id}/clip`: the audio around the word, as a URL on this home.
    func reviewClip(_ id: String) async throws -> URL? {
        let c: ReviewClip = try await get("/api/review/flags/\(Self.segment(id))/clip")
        return Self.clipURL(c.url, home: home)
    }

    /// `POST /api/review/flags/{id}/resolve`.
    func resolveFlag(_ id: String, action: ReviewAction, text: String? = nil, applyToSameKey: Bool = false,
                     force: Bool = false) async throws -> ReviewResult {
        try await send(request("POST", "/api/review/flags/\(Self.segment(id))/resolve",
                               body: Self.resolveBody(action, text: text, applyToSameKey: applyToSameKey, force: force)))
    }

    /// Undoes an action: its batch, else its edit.
    func undoReview(_ r: ReviewResult) async throws {
        if let b = r.batchID {
            _ = try await sendRaw(request("POST", "/api/review/batches/\(Self.segment(b))/undo"))
        } else if let e = r.editID {
            _ = try await sendRaw(request("POST", "/api/review/edits/\(Self.segment(e))/undo"))
        }
    }

    /// The open words (Word review's badge).
    func reviewOpenCount() async throws -> Int {
        let s: ReviewSummaryDoc = try await get("/api/review/summary")
        return s.open
    }

    /// The clip's address on `home` (the API answers a path; a home under a base path keeps it).
    nonisolated static func clipURL(_ path: String, home: URL) -> URL? {
        let base = home.path().hasSuffix("/") ? String(home.path().dropLast()) : home.path()
        let full = (!base.isEmpty && path.hasPrefix("/") && !path.hasPrefix("//") && !path.hasPrefix(base + "/"))
            ? base + path : path
        var c = URLComponents(url: home, resolvingAgainstBaseURL: false)
        let parts = full.split(separator: "?", maxSplits: 1).map(String.init)
        c?.percentEncodedPath = parts[0]
        c?.percentEncodedQuery = parts.count > 1 ? parts[1] : nil
        return c?.url
    }

    nonisolated static func resolveBody(_ action: ReviewAction, text: String?, applyToSameKey: Bool,
                                        force: Bool) -> [String: Any] {
        var b: [String: Any] = ["action": action.rawValue]
        if let text { b["text"] = text }
        if applyToSameKey { b["apply_to_same_key"] = true }
        if force { b["force"] = true }
        return b
    }
}

public extension APIError {
    /// A 403 from a review route: not (or no longer) a home admin.
    var isReviewForbidden: Bool {
        if case .status(403, _, _) = self { return true }
        return false
    }

    /// A 409's code (locked, stale, resolved, changed_since), or nil.
    var reviewConflict: String? {
        guard case let .status(409, body, _) = self,
              let data = body.data(using: .utf8),
              let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else { return nil }
        return obj["code"] as? String ?? "conflict"
    }
}
