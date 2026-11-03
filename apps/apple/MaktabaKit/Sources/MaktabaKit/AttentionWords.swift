import Foundation

// What a "Needs attention" group says. Its cause is a grouping key, not words: a cause the
// engine could not name is "kind:normalised message", the message's numbers made <n> and its
// paths <path> (pipeline engine/causes.py). The title is the cause's words, by its code or its
// kind, in the app's language: the Android app's words (AttentionWords.kt), under the web's
// keys (`queue.issues.cause.<code>` in en.json, ar.json and MaktabaKit's catalog). The home's
// label (api queues/causes.go) only for a code the app has no words for.

public enum AttentionWords {
    /// The cause codes' English (and the kinds' the stages raise that are no code of their
    /// own); the Arabic is in the catalog under `queue.issues.cause.<code>`.
    public static let causes: [String: String] = [
        "nas_timeout": "NAS read timed out",
        "nas_invalid_argument": "NAS refused the read (Invalid argument)",
        "nas_partial_read": "NAS returned a partial read",
        "file_missing": "File is missing on disk",
        "ffmpeg_decode": "Could not decode the media file",
        "youtube_bot_check": "YouTube asked for a bot check",
        "youtube_rate_limited": "YouTube is limiting requests",
        "youtube_unavailable": "YouTube video or channel unavailable",
        "plex_unreachable": "Plex is unreachable",
        "plex_auth": "Plex rejected the token",
        "dizquetv_unreachable": "dizqueTV is unreachable",
        "readback_mismatch": "Write did not read back correctly",
        "no_handler": "No worker handles this stage",
        "graph_violation": "Stage requested work its graph does not allow",
        "db_error": "Database error",
        "shm_full": "Postgres ran out of shared memory",
        "disk_full": "No space left on the disk",
        "permission_denied": "No permission to read the file",
        "ffmpeg_rc": "ffmpeg failed on the file",
        "awaiting_review": "Waiting for your review",
        "stale_graph": "No worker has synced this stage recently",
        "model_unavailable": "The model did not answer",
        "thumbnail_unreadable": "The thumbnail could not be read",
        "video_unhashed": "The file has no content hash yet",
        "unknown": "Unknown failure",
    ]

    /// Codes that say what another code says (a chapter's frames fail as ffmpeg does).
    static let sameWords = ["chapter_frames_failed": "ffmpeg_rc"]

    /// A code's words in `locale`'s language, nil when the app has none.
    static func words(_ code: String, _ locale: Locale) -> String? {
        let c = sameWords[code] ?? code
        guard let en = causes[c] else { return nil }
        return kitWord("queue.issues.cause." + c, locale) ?? en
    }

    /// A generated cause's kind ("nas_timeout:<path>: no answer" is nas_timeout's), else the cause.
    public static func kind(_ cause: String) -> String {
        guard let i = cause.firstIndex(of: ":"), i != cause.startIndex else { return cause }
        return String(cause[..<i])
    }

    /// True when `s` is a grouping key, never words to show: a code's "kind:..." or a placeholder.
    public static func looksLikeKey(_ s: String) -> Bool {
        s.range(of: #"^[a-z][a-z0-9_]*:"#, options: .regularExpression) != nil
            || s.range(of: #"<(n|path|id|hex)>"#, options: .regularExpression) != nil
    }

    /// A group's title: its cause's words, else its kind's, else the home's label, else the kind in words.
    public static func title(cause: String, label: String, locale: Locale = AppLanguage.locale) -> String {
        if let w = words(cause, locale) ?? words(kind(cause), locale) { return w }
        if !label.trimmingCharacters(in: .whitespaces).isEmpty, !looksLikeKey(label) { return label }
        let spaced = kind(cause).replacingOccurrences(of: "_", with: " ").trimmingCharacters(in: .whitespaces)
        return spaced.prefix(1).uppercased() + spaced.dropFirst()
    }

    /// A job's message as it should read: a repr's escapes as real line breaks (never a
    /// backslash-n), blank lines dropped, and each part in its own direction (`isolateRTL`).
    public static func readableMessage(_ message: String) -> String {
        message
            .replacingOccurrences(of: #"\r\n"#, with: "\n")
            .replacingOccurrences(of: #"\n"#, with: "\n")
            .replacingOccurrences(of: #"\r"#, with: "\n")
            .replacingOccurrences(of: #"\t"#, with: " ")
            .components(separatedBy: .newlines)
            .map { $0.replacingOccurrences(of: #"\s+$"#, with: "", options: .regularExpression) }
            .filter { !$0.trimmingCharacters(in: .whitespaces).isEmpty }
            .map(isolateRTL)
            .joined(separator: "\n")
            .trimmingCharacters(in: .whitespacesAndNewlines)
    }

    private static let fsi = "\u{2068}", pdi = "\u{2069}"

    /// Right-to-left letters: Hebrew, Arabic, Syriac, Thaana, N'Ko ... and their presentation forms.
    static func isRTL(_ s: Substring) -> Bool {
        s.unicodeScalars.contains {
            switch $0.value {
            case 0x0590...0x08FF, 0xFB1D...0xFDFF, 0xFE70...0xFEFF, 0x10800...0x10FFF, 0x1E800...0x1EFFF: true
            default: false
            }
        }
    }

    /// Each part of `line` between ": " separators that has right-to-left letters, set apart in
    /// its own direction (Unicode's first-strong isolate): "<file>: no answer" with an Arabic
    /// file name reads as the name, then ": no answer", in an English line or an Arabic one.
    public static func isolateRTL(_ line: String) -> String {
        guard isRTL(Substring(line)) else { return line }
        return line.components(separatedBy: ": ")
            .map { isRTL(Substring($0)) ? fsi + $0 + pdi : $0 }
            .joined(separator: ": ")
    }
}

extension FailureGroup {
    /// The group's title in the app's language (or `locale`'s): `AttentionWords.title`.
    public func title(locale: Locale = AppLanguage.locale) -> String {
        AttentionWords.title(cause: cause, label: label, locale: locale)
    }

    /// The group's sample message as it should read, nil when it has none.
    public var readableSample: String? {
        guard let m = sampleMessage else { return nil }
        let r = AttentionWords.readableMessage(m)
        return r.isEmpty ? nil : r
    }
}
