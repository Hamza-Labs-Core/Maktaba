import Foundation

// Story 49.11 (Epic 49 §5.7): what the home's search help (story 49.8's API) gives the Apple
// apps, the Player and the TV alike: suggestions with their kind, the did-you-mean, the facet
// counts with the filter each bucket stands for, and the order. The web's search page (story
// 49.10, `web/src/pages/searchShared.tsx` and `searchHelp.tsx`) reads the same answers the
// same way. Every field is optional: a home before 49.8 answers without them, and the apps
// then search as they did.

// MARK: Suggestions

/// What a suggestion is: an earlier search of this person's, a word from the transcripts, a
/// video's title (which opens that video) or a named speaker.
public enum SuggestionKind: String, Sendable, CaseIterable, Hashable {
    case history, term, title, speaker
}

/// One suggestion as you type (`GET /api/search/suggest`).
public struct SearchSuggestion: Hashable, Sendable, Identifiable {
    public let text: String
    /// nil from a home before story 49.8 (plain texts).
    public let kind: SuggestionKind?
    /// A title's video.
    public let videoID: String?

    public var id: String { "\(kind?.rawValue ?? "")\n\(text)\n\(videoID ?? "")" }

    public init(text: String, kind: SuggestionKind? = nil, videoID: String? = nil) {
        self.text = text
        self.kind = kind
        self.videoID = videoID
    }

    /// The suggestions of an answer: its `items`, else its plain `suggestions`. A blank text
    /// is left out, a kind this app does not know reads as none, a title without its video is
    /// left out (it could not open), and the same text and kind come once.
    static func list(items: [SuggestItem], texts: [String]) -> [SearchSuggestion] {
        var out: [SearchSuggestion] = []
        if !items.isEmpty {
            for it in items {
                guard let text = it.text, !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { continue }
                let video = it.videoID.flatMap { $0.isEmpty ? nil : $0 }
                out.append(SearchSuggestion(text: text, kind: it.kind.flatMap(SuggestionKind.init(rawValue:)), videoID: video))
            }
        } else {
            out = texts.filter { !$0.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }.map { SearchSuggestion(text: $0) }
        }
        var seen = Set<String>()
        return out.filter { s in
            if s.kind == .title, s.videoID == nil { return false }
            return seen.insert(s.id).inserted
        }
    }
}

/// An item of `GET /api/search/suggest`, read as it comes.
struct SuggestItem: Decodable {
    let text: String?
    let kind: String?
    let videoID: String?

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        text = c.lenient(AnyKey(stringValue: "text"))
        kind = c.lenient(AnyKey(stringValue: "kind"))
        videoID = c.lenient(AnyKey(stringValue: "video_id"))
    }
}

/// `GET /api/search/suggest`: `items` with their kinds (story 49.8), and `suggestions`, the
/// texts alone, which every home sends.
struct SuggestAnswer: Decodable {
    let suggestions: [SearchSuggestion]

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        suggestions = SearchSuggestion.list(items: c.lenientList(AnyKey(stringValue: "items")),
                                            texts: c.lenientList(AnyKey(stringValue: "suggestions")))
    }
}

// MARK: Sort

/// The order of a search's results: relevance (the home's default), newest (the date a video
/// shows) or most matching lines first.
public enum SearchSort: String, Sendable, CaseIterable, Hashable {
    case relevance, newest, matches
}

// MARK: Facets

/// A facet of the videos found.
public enum SearchFacet: String, Sendable, CaseIterable, Hashable {
    case library, language, speaker, year, duration

    /// A year and a duration band are ranges: one of each at a time.
    public var isRange: Bool { self == .year || self == .duration }
}

/// The request filter a year or duration bucket stands for, as the home gives it: a date
/// range (RFC 3339 times) or a seconds range, each bound `gte`, `lte` or `lt`.
public struct SearchBucketFilter: Hashable, Sendable {
    public var date: [String: String]
    public var durationSec: [String: Double]

    public init(date: [String: String] = [:], durationSec: [String: Double] = [:]) {
        self.date = date
        self.durationSec = durationSec
    }

    static let bounds: Set<String> = ["gte", "lte", "lt"]
}

/// One bucket of a facet: its value, a library's name, and how many of the videos found it
/// holds.
public struct SearchBucket: Decodable, Hashable, Sendable {
    public let value: String
    /// A library's name (the library facet).
    public let label: String?
    public let count: Int
    /// A year's or a duration band's request filter.
    public let filter: SearchBucketFilter?

    public init(value: String, label: String? = nil, count: Int, filter: SearchBucketFilter? = nil) {
        self.value = value
        self.label = label
        self.count = count
        self.filter = filter
    }

    private struct NoValue: Error {}

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        func k(_ s: String) -> AnyKey { AnyKey(stringValue: s) }
        let v: String? = c.lenient(k("value")) ?? (c.lenient(k("value")) as Int?).map(String.init)
        // a bucket without a value cannot be chosen: the list leaves it out
        guard let v, !v.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { throw NoValue() }
        value = v
        label = (c.lenient(k("label")) as String?).flatMap { $0.trimmingCharacters(in: .whitespaces).isEmpty ? nil : $0 }
        count = max(c.lenient(k("count")) ?? 0, 0)
        var f = SearchBucketFilter()
        if let fc = try? c.nestedContainer(keyedBy: AnyKey.self, forKey: k("filter")) {
            if let d = try? fc.nestedContainer(keyedBy: AnyKey.self, forKey: k("date")) {
                for b in SearchBucketFilter.bounds { if let s: String = d.lenient(k(b)) { f.date[b] = s } }
            }
            if let d = try? fc.nestedContainer(keyedBy: AnyKey.self, forKey: k("duration_sec")) {
                for b in SearchBucketFilter.bounds { if let s: Double = d.lenient(k(b)) { f.durationSec[b] = s } }
            }
        }
        filter = f.date.isEmpty && f.durationSec.isEmpty ? nil : f
    }
}

/// The counts of a search's videos (the first page's answer): each facet's buckets, most
/// first (years newest first, duration bands shortest first).
public struct SearchFacets: Decodable, Hashable, Sendable {
    public let library: [SearchBucket]
    public let language: [SearchBucket]
    public let speaker: [SearchBucket]
    public let year: [SearchBucket]
    public let duration: [SearchBucket]
    /// How many videos were counted.
    public let videos: Int?

    public init(library: [SearchBucket] = [], language: [SearchBucket] = [], speaker: [SearchBucket] = [],
                year: [SearchBucket] = [], duration: [SearchBucket] = [], videos: Int? = nil) {
        self.library = library
        self.language = language
        self.speaker = speaker
        self.year = year
        self.duration = duration
        self.videos = videos
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        func k(_ s: String) -> AnyKey { AnyKey(stringValue: s) }
        library = c.lenientList(k("library"))
        language = c.lenientList(k("language"))
        speaker = c.lenientList(k("speaker"))
        year = c.lenientList(k("year"))
        duration = c.lenientList(k("duration"))
        videos = c.lenient(k("videos"))
    }

    public func buckets(_ f: SearchFacet) -> [SearchBucket] {
        switch f {
        case .library: library
        case .language: language
        case .speaker: speaker
        case .year: year
        case .duration: duration
        }
    }

    public var isEmpty: Bool { SearchFacet.allCases.allSatisfy { buckets($0).isEmpty } }
}

/// A bucket as the apps show it: its count is nil for a chosen bucket the narrowed answer no
/// longer lists, kept so it can be taken off where it was put on.
public struct ShownBucket: Hashable, Sendable, Identifiable {
    public let facet: SearchFacet
    public let value: String
    public let label: String?
    public let count: Int?
    public let chosen: Bool
    public var id: String { "\(facet.rawValue):\(value)" }
}

/// A facet with its buckets to show.
public struct ShownFacet: Hashable, Sendable, Identifiable {
    public let facet: SearchFacet
    public let buckets: [ShownBucket]
    public var id: String { facet.rawValue }
}

// MARK: Filters

/// The buckets chosen: libraries, languages and speakers are lists (any of them), a year and
/// a duration band one each (the request has one date and one duration range). Filters of
/// different facets combine.
public struct SearchFilterChoice: Hashable, Sendable {
    public var library: [String] = []
    public var language: [String] = []
    public var speaker: [String] = []
    public var year: String?
    public var duration: String?

    public init(library: [String] = [], language: [String] = [], speaker: [String] = [],
                year: String? = nil, duration: String? = nil) {
        self.library = library
        self.language = language
        self.speaker = speaker
        self.year = year
        self.duration = duration
    }

    /// The duration bands' seconds, as the home makes them (`api/internal/handlers/search/facets.go`).
    public static let durationBands: [String: [String: Double]] = [
        "under_5m": ["gte": 0, "lt": 300],
        "5m_20m": ["gte": 300, "lt": 1200],
        "20m_60m": ["gte": 1200, "lt": 3600],
        "over_60m": ["gte": 3600],
    ]
    /// The bands in their order, shortest first.
    public static let bandOrder = ["under_5m", "5m_20m", "20m_60m", "over_60m"]

    /// A year's date range, as the home's year bucket gives it (UTC).
    public static func yearRange(_ year: String) -> [String: String] {
        let y = Int(year) ?? 0
        func iso(_ y: Int) -> String { String(format: "%04d-01-01T00:00:00.000Z", y) }
        return ["gte": iso(y), "lt": iso(y + 1)]
    }

    public var isEmpty: Bool { count == 0 }

    /// How many filters are in use.
    public var count: Int { library.count + language.count + speaker.count + (year == nil ? 0 : 1) + (duration == nil ? 0 : 1) }

    /// The values chosen of `facet`.
    public func chosen(_ facet: SearchFacet) -> [String] {
        switch facet {
        case .library: library
        case .language: language
        case .speaker: speaker
        case .year: year.map { [$0] } ?? []
        case .duration: duration.map { [$0] } ?? []
        }
    }

    public func has(_ facet: SearchFacet, _ value: String) -> Bool { chosen(facet).contains(value) }

    /// `value` taken on or off: on a list it joins or leaves; a year or a band replaces the
    /// one chosen before.
    public func toggled(_ facet: SearchFacet, _ value: String) -> SearchFilterChoice {
        var next = self
        func flip(_ list: [String]) -> [String] { list.contains(value) ? list.filter { $0 != value } : list + [value] }
        switch facet {
        case .library: next.library = flip(library)
        case .language: next.language = flip(language)
        case .speaker: next.speaker = flip(speaker)
        case .year: next.year = year == value ? nil : value
        case .duration: next.duration = duration == value ? nil : value
        }
        return next
    }

    /// `value` taken off (nothing when it is not on).
    public func removing(_ facet: SearchFacet, _ value: String) -> SearchFilterChoice {
        has(facet, value) ? toggled(facet, value) : self
    }

    /// Every filter in use, facet by facet in the facets' order: what "Filters in use" lists.
    public var inUse: [(facet: SearchFacet, value: String)] {
        SearchFacet.allCases.flatMap { f in chosen(f).map { (f, $0) } }
    }

    /// The `filters` of `POST /api/search` (nil when none is in use). A year or a band takes
    /// its bucket's own filter from `facets` when they list it, else the range the home gives
    /// that bucket.
    public func requestBody(facets: SearchFacets?) -> [String: Any]? {
        guard !isEmpty else { return nil }
        var f: [String: Any] = [:]
        if !library.isEmpty { f["library_id"] = library }
        if !language.isEmpty { f["language"] = language }
        if !speaker.isEmpty { f["speaker"] = speaker }
        if let year {
            let own = facets?.year.first { $0.value == year }?.filter?.date
            f["date"] = own.flatMap { $0.isEmpty ? nil : $0 } ?? Self.yearRange(year)
        }
        if let duration {
            let own = facets?.duration.first { $0.value == duration }?.filter?.durationSec
            // a band this app does not know, and the answer no longer lists, sends no range (as
            // the web's page): the chip stays, to be taken off
            if let range = own.flatMap({ $0.isEmpty ? nil : $0 }) ?? Self.durationBands[duration] {
                // whole seconds go as integers
                f["duration_sec"] = range.mapValues { $0.rounded() == $0 && abs($0) < 1e15 ? Int($0) as Any : $0 as Any }
            }
        }
        return f
    }

    /// The facets to show for `facets` with this choice: each facet's buckets, a chosen bucket
    /// the answer no longer lists first and without a count; a facet with nothing to show is
    /// left out. `labels` names a library the answer no longer lists ("library:<id>").
    public func shown(_ facets: SearchFacets?, labels: [String: String] = [:]) -> [ShownFacet] {
        SearchFacet.allCases.compactMap { f in
            let listed = facets?.buckets(f) ?? []
            var out = chosen(f).filter { v in !listed.contains { $0.value == v } }
                .map { ShownBucket(facet: f, value: $0, label: labels["\(f.rawValue):\($0)"], count: nil, chosen: true) }
            out += listed.map { ShownBucket(facet: f, value: $0.value, label: $0.label ?? labels["\(f.rawValue):\($0.value)"],
                                            count: $0.count, chosen: has(f, $0.value)) }
            return out.isEmpty ? nil : ShownFacet(facet: f, buckets: out)
        }
    }
}

// MARK: Words

/// The search help's words, English and Arabic (MaktabaKit's catalog), the web's (49.10):
/// both apps say the same.
public enum SearchWords {
    /// A suggestion's kind ("Recent", "Word", "Video", "Speaker"). Keyed (`search.kind.<kind>`):
    /// the speaker kind and the speaker facet share their English, not their Arabic.
    public static func kind(_ k: SuggestionKind, _ locale: Locale = AppLanguage.locale) -> String {
        if let w = kitWord("search.kind.\(k.rawValue)", locale) { return w }
        switch k {
        case .history: return "Recent"
        case .term: return "Word"
        case .title: return "Video"
        case .speaker: return "Speaker"
        }
    }

    public static func facet(_ f: SearchFacet, _ locale: Locale = AppLanguage.locale) -> String {
        switch f {
        case .library: kit("Library", locale)
        case .language: kit("Language", locale)
        case .speaker: kit("Speaker", locale)
        case .year: kit("Year", locale)
        case .duration: kit("Length", locale)
        }
    }

    public static func sort(_ s: SearchSort, _ locale: Locale = AppLanguage.locale) -> String {
        switch s {
        case .relevance: kit("Relevance", locale)
        case .newest: kit("Newest", locale)
        case .matches: kit("Most matches", locale)
        }
    }

    public static func sortBy(_ locale: Locale = AppLanguage.locale) -> String { kit("Sort by", locale) }

    /// "Sort by: Newest".
    public static func sortedBy(_ s: SearchSort, _ locale: Locale = AppLanguage.locale) -> String {
        kit("Sort by: \(sort(s, locale))", locale)
    }

    /// "Did you mean المدرسة?": the corrected words where the sentence puts them.
    public static func didYouMean(_ q: String, _ locale: Locale = AppLanguage.locale) -> String {
        kit("Did you mean \(q)?", locale)
    }

    /// The did-you-mean sentence around the corrected words: what comes before and after them,
    /// so the words can be marked where each language puts them.
    public static func didYouMeanParts(_ locale: Locale = AppLanguage.locale) -> (before: String, after: String) {
        let marker = "\u{FFFC}"
        let whole = didYouMean(marker, locale)
        guard let r = whole.range(of: marker) else { return ("", "") }
        // the words' own bidi isolates (U+2068, U+2069) go with them: the caller isolates them
        let isolates = CharacterSet(charactersIn: "\u{2068}\u{2069}")
        func trim(_ s: Substring) -> String { String(String.UnicodeScalarView(s.unicodeScalars.filter { !isolates.contains($0) })) }
        return (trim(whole[..<r.lowerBound]), trim(whole[r.upperBound...]))
    }

    public static func narrow(_ locale: Locale = AppLanguage.locale) -> String { kit("Narrow the results", locale) }
    public static func filters(_ locale: Locale = AppLanguage.locale) -> String { kit("Filters", locale) }
    public static func filtersInUse(_ locale: Locale = AppLanguage.locale) -> String { kit("Filters in use", locale) }
    public static func clearFilters(_ locale: Locale = AppLanguage.locale) -> String { kit("Clear all filters", locale) }
    public static func done(_ locale: Locale = AppLanguage.locale) -> String { kit("Done", locale) }

    /// "Remove Library: Tafsir".
    public static func remove(_ name: String, _ locale: Locale = AppLanguage.locale) -> String {
        kit("Remove \(name)", locale)
    }

    /// "Search for “seerah”" (the TV's button that runs what was typed).
    public static func searchFor(_ q: String, _ locale: Locale = AppLanguage.locale) -> String {
        kit("Search for “\(q)”", locale)
    }

    /// "3 videos": a bucket's count as VoiceOver reads it.
    public static func videos(_ n: Int, _ locale: Locale = AppLanguage.locale) -> String {
        kit("\(n) videos", locale)
    }

    /// A number in the app's language (Western digits 0–9 in Arabic too).
    public static func number(_ n: Int, _ locale: Locale = AppLanguage.locale) -> String {
        n.formatted(.number.locale(AppLanguage.westernDigits(locale)))
    }

    /// "Library: Tafsir Lessons": a filter in use.
    public static func inUse(_ facet: SearchFacet, _ value: String, label: String?, _ locale: Locale = AppLanguage.locale) -> String {
        // the name isolated, as the catalog's sentences isolate theirs: a Latin name keeps its
        // place in an Arabic line
        "\(self.facet(facet, locale)): \u{2068}\(bucket(facet, value, label: label, locale))\u{2069}"
    }

    /// A bucket's name: a library by its name, a language by its name in the app's language, a
    /// year in the app's digits, a duration band by its words; a speaker as the home names them.
    public static func bucket(_ facet: SearchFacet, _ value: String, label: String?, _ locale: Locale = AppLanguage.locale) -> String {
        switch facet {
        case .library:
            return label.flatMap { $0.trimmingCharacters(in: .whitespaces).isEmpty ? nil : $0 } ?? value
        case .language:
            let name = locale.localizedString(forLanguageCode: value)
            return name.flatMap { $0.isEmpty || $0 == value ? nil : $0 } ?? value
        case .year:
            guard let y = Int(value) else { return value }
            return y.formatted(.number.grouping(.never).locale(AppLanguage.westernDigits(locale)))
        case .duration:
            return band(value, locale)
        case .speaker:
            return value
        }
    }

    /// A duration band's words ("Under 5 min", "5 to 20 min", "20 to 60 min", "Over an hour").
    public static func band(_ value: String, _ locale: Locale = AppLanguage.locale) -> String {
        let n = { (v: Int) in number(v, locale) }
        switch value {
        case "under_5m": return kit("Under \(n(5)) min", locale)
        case "5m_20m": return kit("\(n(5)) to \(n(20)) min", locale)
        case "20m_60m": return kit("\(n(20)) to \(n(60)) min", locale)
        case "over_60m": return kit("Over an hour", locale)
        default: return value
        }
    }
}
