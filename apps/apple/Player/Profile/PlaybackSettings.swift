import Foundation
import Observation

// FR-31's playback settings, kept on this device. The Profile's Playback page (story 48.22)
// changes them; the player (story 48.23) reads them through `PlaybackSettings.shared`:
//
//     let s = PlaybackSettings.shared
//     s.autoplayNext                       // Up next counts down (FR-33); off: stop at the end
//     s.resumeWithoutAsking                // open at playback_state.position without asking
//     s.subtitlesOn, s.subtitleLanguage.tag // pick a subtitle at start (nil tag: the video's own)
//     s.subtitleSize.scale                 // the VTT's / the renditions' text scale
//     s.maxBitrateKbps(expensive: path.isExpensive) // `max_bitrate_kbps` (nil: no cap)
//
// The keys are Android's (`PlaybackSettings.kt`, story 48.31), so both apps name them alike.

/// A subtitle size: small, medium, large, as a scale of the player's default.
enum SubtitleSize: String, CaseIterable, Identifiable, Sendable {
    case small = "s"
    case medium = "m"
    case large = "l"

    var id: String { rawValue }

    var scale: Double {
        switch self {
        case .small: 0.8
        case .medium: 1
        case .large: 1.3
        }
    }

    var label: LocalizedStringResource {
        switch self {
        case .small: LocalizedStringResource("Small", table: "Profile", comment: "A subtitle size")
        case .medium: LocalizedStringResource("Medium", table: "Profile", comment: "A subtitle size")
        case .large: LocalizedStringResource("Large", table: "Profile", comment: "A subtitle size")
        }
    }
}

/// A streaming quality, sent as `max_bitrate_kbps` (nil: the original, no cap).
enum StreamQuality: String, CaseIterable, Identifiable, Sendable {
    case original
    case p1080 = "1080p"
    case p720 = "720p"
    case p480 = "480p"

    var id: String { rawValue }

    /// The cap the player sends; the same numbers as Android's.
    var maxBitrateKbps: Int? {
        switch self {
        case .original: nil
        case .p1080: 8000
        case .p720: 4000
        case .p480: 1500
        }
    }

    var label: LocalizedStringResource {
        switch self {
        case .original: LocalizedStringResource("Original", table: "Profile", comment: "A streaming quality: the file as it is, no cap")
        case .p1080: LocalizedStringResource("1080p", table: "Profile", comment: "A streaming quality")
        case .p720: LocalizedStringResource("720p", table: "Profile", comment: "A streaming quality")
        case .p480: LocalizedStringResource("480p", table: "Profile", comment: "A streaming quality")
        }
    }
}

/// The subtitle language to prefer: the video's own default, English or Arabic.
enum SubtitleLanguage: String, CaseIterable, Identifiable, Sendable {
    case videoDefault = "default"
    case english = "en"
    case arabic = "ar"

    var id: String { rawValue }

    /// The language tag to match a subtitle track against (nil: the video's default track).
    var tag: String? { self == .videoDefault ? nil : rawValue }

    var label: LocalizedStringResource {
        switch self {
        case .videoDefault: LocalizedStringResource("The video's own", table: "Profile", comment: "Subtitle language: the video's default")
        case .english: LocalizedStringResource("English", table: "Profile", comment: "A language, named in English")
        case .arabic: LocalizedStringResource("Arabic", table: "Profile", comment: "A language, named in Arabic")
        }
    }
}

/// The playback settings on this device (`playback.*` keys in the app's defaults, beside the
/// theme). Each change is written at once. Observable: a view reading one redraws on change.
@MainActor @Observable
final class PlaybackSettings {
    /// The app's one store.
    static let shared = PlaybackSettings()

    enum Key {
        static let autoplayNext = "playback.autoplay_next"
        static let subtitlesOn = "playback.subtitles_on"
        static let subtitleLanguage = "playback.subtitle_language"
        static let subtitleSize = "playback.subtitle_size"
        static let qualityWifi = "playback.quality_wifi"
        static let qualityMobile = "playback.quality_mobile"
        static let resumeWithoutAsking = "playback.resume_without_asking"

        static let all = [autoplayNext, subtitlesOn, subtitleLanguage, subtitleSize, qualityWifi, qualityMobile, resumeWithoutAsking]
    }

    /// Play the next episode automatically (Up next, FR-33). On by default.
    var autoplayNext: Bool { didSet { defaults.set(autoplayNext, forKey: Key.autoplayNext) } }
    /// Start each video with subtitles. Off by default.
    var subtitlesOn: Bool { didSet { defaults.set(subtitlesOn, forKey: Key.subtitlesOn) } }
    /// Stored as the language tag ("en", "ar"), the key removed for the video's own: the
    /// player (48.23) reads the tag as it is.
    var subtitleLanguage: SubtitleLanguage {
        didSet {
            if let tag = subtitleLanguage.tag { defaults.set(tag, forKey: Key.subtitleLanguage) } else { defaults.removeObject(forKey: Key.subtitleLanguage) }
        }
    }
    var subtitleSize: SubtitleSize { didSet { defaults.set(subtitleSize.rawValue, forKey: Key.subtitleSize) } }
    /// On Wi-Fi or Ethernet. Original by default.
    var qualityWifi: StreamQuality { didSet { defaults.set(qualityWifi.rawValue, forKey: Key.qualityWifi) } }
    /// On mobile data (an expensive network path). 720p by default.
    var qualityMobile: StreamQuality { didSet { defaults.set(qualityMobile.rawValue, forKey: Key.qualityMobile) } }
    /// Open at the saved position without asking. On by default.
    var resumeWithoutAsking: Bool { didSet { defaults.set(resumeWithoutAsking, forKey: Key.resumeWithoutAsking) } }

    @ObservationIgnored private let defaults: UserDefaults

    init(defaults: UserDefaults = .standard) {
        self.defaults = defaults
        func bool(_ key: String, _ fallback: Bool) -> Bool { defaults.object(forKey: key) as? Bool ?? fallback }
        func value<T: RawRepresentable>(_ key: String, _ fallback: T) -> T where T.RawValue == String {
            defaults.string(forKey: key).flatMap(T.init(rawValue:)) ?? fallback
        }
        autoplayNext = bool(Key.autoplayNext, true)
        subtitlesOn = bool(Key.subtitlesOn, false)
        subtitleLanguage = value(Key.subtitleLanguage, SubtitleLanguage.videoDefault)
        subtitleSize = value(Key.subtitleSize, SubtitleSize.medium)
        qualityWifi = value(Key.qualityWifi, StreamQuality.original)
        qualityMobile = value(Key.qualityMobile, StreamQuality.p720)
        resumeWithoutAsking = bool(Key.resumeWithoutAsking, true)
    }

    /// The quality for the network in use: `expensive` is mobile data (or a hotspot).
    func quality(expensive: Bool) -> StreamQuality { expensive ? qualityMobile : qualityWifi }

    /// What the player sends as `max_bitrate_kbps` (nil: none).
    func maxBitrateKbps(expensive: Bool) -> Int? { quality(expensive: expensive).maxBitrateKbps }

    /// Back to the defaults (and the theme to dark), once per launch: a Debug launch with
    /// `-maktaba-settings-reset`, so a UI test starts as a fresh install. The Profile calls it
    /// as it appears.
    static func resetIfAsked(arguments: [String] = CommandLine.arguments) {
        #if DEBUG
        guard !didReset, arguments.contains("-maktaba-settings-reset") else { return }
        didReset = true
        for key in Key.all + ["theme"] { UserDefaults.standard.removeObject(forKey: key) }
        shared.reload()
        #endif
    }

    private static var didReset = false

    /// Reads every setting again from the defaults.
    func reload() {
        let now = PlaybackSettings(defaults: defaults)
        autoplayNext = now.autoplayNext
        subtitlesOn = now.subtitlesOn
        subtitleLanguage = now.subtitleLanguage
        subtitleSize = now.subtitleSize
        qualityWifi = now.qualityWifi
        qualityMobile = now.qualityMobile
        resumeWithoutAsking = now.resumeWithoutAsking
    }
}
