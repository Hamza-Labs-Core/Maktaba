import Foundation

/// The playback settings the player reads (FR-31), as Settings (story 48.22) will write them.
/// 48.22 was not merged when the player was built, so these are the player's minimal reads:
/// three keys of the standard defaults, the same names as the Android Player's
/// (`playback.autoplay_next`, `playback.subtitles_on`, `playback.subtitle_language`). A UI
/// test sets them with launch arguments (`-playback.autoplay_next NO`).
struct PlaybackSettings: Equatable {
    static let autoplayNextKey = "playback.autoplay_next"
    static let subtitlesOnKey = "playback.subtitles_on"
    static let subtitleLanguageKey = "playback.subtitle_language"

    /// Up next counts down and plays the series' next episode (on by default).
    var autoplayNext = true
    /// Each video starts with subtitles on, in `subtitleLanguage` (off by default).
    var subtitlesOn = false
    /// The subtitles' language ("ar", "en"); nil: the app's language.
    var subtitleLanguage: String?

    static func read(_ defaults: UserDefaults = .standard) -> PlaybackSettings {
        PlaybackSettings(autoplayNext: defaults.object(forKey: autoplayNextKey) == nil || defaults.bool(forKey: autoplayNextKey),
                         subtitlesOn: defaults.bool(forKey: subtitlesOnKey),
                         subtitleLanguage: defaults.string(forKey: subtitleLanguageKey).flatMap { $0.isEmpty ? nil : $0 })
    }

    /// The language subtitles start in: the setting, else the app's language.
    var language: String {
        subtitleLanguage ?? Bundle.main.preferredLocalizations.first.map { String($0.prefix(2)) } ?? "en"
    }
}
