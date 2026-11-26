import Foundation

// What the queue says about a stage and a running job, in the app's language. The home sends
// codes: a stage's id with its label as the pipeline graph names it (engine/graph.py, in
// English), a running job's state and its progress step (the step a stage reports, often the
// stage's own id). The words are the web's (`queue.stage.<id>.title` in en.json and ar.json,
// as web/src/lib/stageWords.ts reads them) under the same keys in MaktabaKit's catalog; the
// steps' and the states' are under `queue.running.step.<code>` and `queue.running.state.<state>`.
// A code this app has no words for (a newer home's, a plugin's) keeps what the home sent.

public enum StageWords {
    /// Every stage's English title (en.json `queue.stage.<id>.title`, the graph's label); the
    /// Arabic is in the catalog under the same key.
    public static let stages: [String: String] = [
        "youtube_sync": "YouTube listing",
        "exact_dates": "Exact dates",
        "content_hash": "File identity",
        "probe": "Length",
        "thumb_hash": "Thumbnail",
        "link": "Link",
        "fingerprint": "Fingerprint",
        "style_sample": "Style sample",
        "thumb_small": "Small thumbnail",
        "detect": "Place episodes",
        "fp_gate": "Samples to candidates",
        "tag": "Tag topics",
        "check_tags": "Check series tags",
        "check_clips": "Check clip tags",
        "display_title": "Display titles",
        "books": "Books",
        "topic_collections": "Topic collections",
        "promo_links": "Promo links",
        "clip_groups": "Clip groups",
        "rate_style": "Rate style",
        "review_pages": "Review pages",
        "plan_channels": "Plan channels",
        "write_channels": "Write channels",
        "plex_livetv": "Plex Live TV",
        "tv_stage": "Stage TV change",
        "tv_swap": "Swap TV change",
        "tv_gc": "TV staging clean-up",
        "tv_rollback": "Roll back TV change",
        "scan": "Scan folders",
        "extract": "Extract audio",
        "transcribe": "Transcribe",
        "second_listen": "Second listen",
        "subtitle_gen": "Subtitle files",
        "index": "Search index",
        "chapter_infer": "Chapters",
        "chapter_thumbs": "Chapter frames",
        "topic_assign": "Topics",
        "topic_recluster": "Library topics",
        "thumbnail": "Poster and sprites",
        "repair_broken": "Repair broken files",
        "storage_canonicalize": "Canonical share paths",
        "component_catchup": "Catch up",
        "access_check": "Access check",
        "categorize": "Categorize",
        "enrich": "Find metadata",
        "convert_mp4": "Convert to MP4",
        "dedup_scan": "Find duplicates",
        "dedup_apply": "Merge duplicates",
        "trash_purge": "Empty file trash",
        "file_restore": "Restore from trash",
        "youtube_fetch": "YouTube downloads",
        "youtube_backfill": "YouTube back catalogue",
        "youtube_tartube": "Tartube import",
        "youtube_collections": "Collections from YouTube",
        "word_check": "Word review",
        "content_screen": "Content screen",
        "keyword_index": "Keyword index",
    ]

    /// The progress steps the stages report (`ctx.progress("<step>", ...)` in the pipeline and
    /// the first-party plugins, and the steps they list) that are not a stage's id.
    public static let steps: [String: String] = [
        "ask": "Ask the model",
        "catch_up": "Catch up",
        "cluster": "Group topics",
        "convert": "Convert",
        "decode": "Decode",
        "download": "Download",
        "embed": "Index for search",
        "fan_out": "Share out the work",
        "frames": "Take frames",
        "full_hash": "Compare whole files",
        "listen": "Listen again",
        "listing": "Read channel lists",
        "matching": "Match episodes",
        "plan": "Plan",
        "plugin": "Plugin at work",
        "purge": "Trash purge",
        "read_vectors": "Read the search index",
        "render": "Draw the pages",
        "repair": "Repair",
        "restore": "Restore",
        "sources": "Read sources",
        "store": "Save",
        "swap": "Swap in the new file",
        "verify": "Check the result",
        "waiting": "Waiting",
    ]

    /// A running job's states (`GET /api/queues/running`): claimed, running, resuming.
    public static let states: [String: String] = [
        "claimed": "starting",
        "running": "running",
        "resuming": "resuming",
    ]

    /// A stage's title in `locale`'s language: its words, else the label the home sent, else
    /// its id with spaces.
    public static func title(_ stage: String, label: String? = nil, locale: Locale = AppLanguage.locale) -> String {
        if let en = stages[stage] { return kitWord("queue.stage.\(stage).title", locale) ?? en }
        if let label, !label.trimmingCharacters(in: .whitespaces).isEmpty { return label }
        return stage.replacingOccurrences(of: "_", with: " ")
    }

    /// A running job's step in words, nil when it says nothing the row does not (none, or the
    /// job's own stage: the transcription stages report their own id). A step that is another
    /// stage's id is that stage's title; one with no words shows as sent, its underscores as
    /// spaces.
    public static func step(_ step: String?, stage: String, locale: Locale = AppLanguage.locale) -> String? {
        guard let step, !step.trimmingCharacters(in: .whitespaces).isEmpty, step != stage else { return nil }
        // a step named with spaces ("read vectors") is keyed with underscores
        let code = step.replacingOccurrences(of: " ", with: "_")
        guard code != stage else { return nil }
        if stages[code] != nil { return title(code, locale: locale) }
        return words(steps, "queue.running.step.", code, locale)
    }

    /// A running job's state in words ("starting" for a claimed job); one with no words as
    /// sent, its underscores as spaces.
    public static func state(_ state: String, locale: Locale = AppLanguage.locale) -> String {
        words(states, "queue.running.state.", state, locale)
    }
}

extension StageInfo {
    /// The stage's title in the app's language (or `locale`'s): `StageWords.title`.
    public func title(locale: Locale = AppLanguage.locale) -> String {
        StageWords.title(stage, label: label, locale: locale)
    }
}

extension FailureGroup {
    /// The group's stage in the app's language (or `locale`'s).
    public func stageTitle(locale: Locale = AppLanguage.locale) -> String {
        StageWords.title(stage, locale: locale)
    }
}

extension RunningJob {
    /// The job's step in the app's language, nil when it adds nothing: `StageWords.step`.
    public func stepWords(locale: Locale = AppLanguage.locale) -> String? {
        StageWords.step(progressStep, stage: stage, locale: locale)
    }

    /// The job's state in the app's language.
    public func stateWords(locale: Locale = AppLanguage.locale) -> String {
        StageWords.state(state, locale: locale)
    }
}
