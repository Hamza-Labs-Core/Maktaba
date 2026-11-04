import Foundation

/// The Remote's words for a stage its component holds back (Epic 51, stories 51.14 and 51.11):
/// the web's queue card, `queue.health.componentOff` / `componentHeld`, with the component's
/// title (`components.<id>.title`). The Arabic is the web's, in MaktabaKit's catalog under
/// the same keys.
public enum QueueWords {
    /// The titles of the catalog components that own stages (en.json `components.<id>.title`).
    public static let componentTitles: [String: String] = [
        "media.thumbnails": "Thumbnails",
        "library.convert": "Convert to MP4",
        "library.dedup": "Duplicates",
        "library.repair": "Repair",
        "transcription": "Transcription",
        "subtitles": "Subtitles",
        "search.semantic": "Smart search",
        "classify": "Organising",
        "enrich": "Details lookup",
        "youtube": "YouTube",
        "ext.plex": "Plex",
        "ext.dizquetv": "dizqueTV",
        "livetv": "Live TV",
    ]

    /// `queue.health.*`: the line of a stage its component holds back.
    public static let lines: [String: String] = [
        "componentOff": "Waiting: the {component} component is off · {n} queued",
        "componentHeld": "Waiting: the {component} component is stopped until you turn it on · {n} queued",
    ]

    /// A component's title in `locale`'s language; its id when this app has none.
    public static func componentTitle(_ id: String, _ locale: Locale = AppLanguage.locale) -> String {
        guard let en = componentTitles[id] else { return id }
        return kitWord("components.\(id).title", locale) ?? en
    }

    /// "Waiting: the Transcription component is off · 1,840 queued"
    public static func componentLine(_ hold: ComponentHold, _ locale: Locale = AppLanguage.locale) -> String {
        filled(lines, "queue.health.", hold.stopped ? "componentHeld" : "componentOff",
               ["component": componentTitle(hold.component, locale),
                "n": hold.queued.formatted(.number.locale(locale))], locale)
    }
}
