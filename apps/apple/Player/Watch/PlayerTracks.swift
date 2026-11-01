import AVFoundation

/// A subtitle or audio choice of the playing item: one option of its legible or audible
/// media selection group (the home's subtitle renditions since 48.4 are the legible ones).
struct TrackChoice: Identifiable, Hashable {
    /// Its place in its group ("0", "1" …), stable while the item plays.
    let id: String
    /// What the person reads: the option's language in the app's language ("English",
    /// "Arabic"; in Arabic "الإنجليزية", "العربية").
    let name: String
    /// Its language ("en", "ar"), when the stream says.
    let language: String?
}

/// The playing item's subtitles and audio, read from AVFoundation and chosen there; and the
/// sound made sure of (FR-39a): whenever an item has an audio stream, it plays with one
/// selected, enabled and audible. No SwiftUI and no app types, so the UI-test targets compile
/// this file and test it on a real stream (see project.yml).
@MainActor enum PlayerTracks {
    /// The item's subtitle and audio groups (nil when it has none). A stream whose sound is in
    /// its video segments (the home's HLS) still has an audio group: one option, no language,
    /// and a group that says it allows an empty selection (`ensureAudio` selects it then).
    static func groups(of item: AVPlayerItem) async -> (legible: AVMediaSelectionGroup?, audible: AVMediaSelectionGroup?) {
        let asset = item.asset
        let legible = try? await asset.loadMediaSelectionGroup(for: .legible)
        let audible = try? await asset.loadMediaSelectionGroup(for: .audible)
        return (legible, audible)
    }

    /// The group's options as choices, in the stream's order (the legible group's forced-only
    /// and closed-caption duplicates are left out: they are not a choice of their own).
    static func choices(in group: AVMediaSelectionGroup?) -> [TrackChoice] {
        guard let group else { return [] }
        return options(in: group).enumerated().map { i, o in
            TrackChoice(id: String(i), name: o.displayName, language: o.extendedLanguageTag ?? o.locale?.language.languageCode?.identifier)
        }
    }

    static func options(in group: AVMediaSelectionGroup) -> [AVMediaSelectionOption] {
        group.options.filter { !$0.hasMediaCharacteristic(.containsOnlyForcedSubtitles) }
    }

    /// The choice selected in `group` now (nil: none, subtitles off).
    static func selected(in group: AVMediaSelectionGroup?, of item: AVPlayerItem) -> TrackChoice? {
        guard let group, let on = item.currentMediaSelection.selectedMediaOption(in: group),
              let i = options(in: group).firstIndex(of: on) else { return nil }
        return choices(in: group)[i]
    }

    /// Selects `choice` in `group` (nil: none, which only subtitles allow).
    static func select(_ choice: TrackChoice?, in group: AVMediaSelectionGroup?, of item: AVPlayerItem) {
        guard let group else { return }
        let all = options(in: group)
        if let choice, let i = Int(choice.id), all.indices.contains(i) {
            item.select(all[i], in: group)
        } else if group.allowsEmptySelection {
            item.select(nil, in: group)
        }
    }

    /// The choice of `group` in `language` (its first, when several), nil when none is.
    static func choice(in group: AVMediaSelectionGroup?, language: String) -> TrackChoice? {
        choices(in: group).first { ($0.language ?? "").lowercased().hasPrefix(language.lowercased()) }
    }

    /// Makes sure what has sound plays with sound: the player not muted and its volume up;
    /// an audio group with nothing selected gets its default (or first) option; an audio
    /// track turned off is turned back on. Returns whether anything had to change. Called when
    /// the item is ready and on every change of its tracks or selection.
    @discardableResult
    static func ensureAudio(player: AVPlayer, item: AVPlayerItem, audible: AVMediaSelectionGroup?) -> Bool {
        var changed = false
        if player.isMuted { player.isMuted = false; changed = true }
        if player.volume <= 0 { player.volume = 1; changed = true }
        if let audible, item.currentMediaSelection.selectedMediaOption(in: audible) == nil,
           let option = audible.defaultOption ?? options(in: audible).first {
            item.select(option, in: audible)
            changed = true
        }
        for t in item.tracks where t.assetTrack?.mediaType == .audio && !t.isEnabled {
            t.isEnabled = true
            changed = true
        }
        return changed
    }

    /// What sound the item plays now, for the UI tests' state and the tests: the codec of its
    /// enabled audio track ("aac"), nil when no audio track is enabled; and whether the
    /// player is audible (not muted, volume up).
    static func sound(player: AVPlayer, item: AVPlayerItem) -> (codec: String?, audible: Bool) {
        let enabled = item.tracks.filter { $0.isEnabled && $0.assetTrack?.mediaType == .audio }
        let codec = enabled.lazy.compactMap { t -> String? in
            guard let desc = t.assetTrack?.formatDescriptions.first else { return "audio" }
            // swiftlint:disable:next force_cast
            return Self.codecName(CMFormatDescriptionGetMediaSubType(desc as! CMFormatDescription))
        }.first
        return (enabled.isEmpty ? nil : codec ?? "audio", !player.isMuted && player.volume > 0)
    }

    static func codecName(_ subtype: FourCharCode) -> String {
        switch subtype {
        case kAudioFormatMPEG4AAC, kAudioFormatMPEG4AAC_HE, kAudioFormatMPEG4AAC_HE_V2, kAudioFormatMPEG4AAC_LD: "aac"
        case kAudioFormatAC3: "ac3"
        case kAudioFormatEnhancedAC3: "eac3"
        case kAudioFormatMPEGLayer3: "mp3"
        case kAudioFormatOpus: "opus"
        case kAudioFormatFLAC: "flac"
        default: "audio"
        }
    }
}
