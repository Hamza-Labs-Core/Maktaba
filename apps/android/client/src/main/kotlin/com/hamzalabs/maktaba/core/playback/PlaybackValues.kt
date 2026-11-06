package com.hamzalabs.maktaba.core.playback

import com.hamzalabs.maktaba.core.session.KeyValueStore
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

// The Players' playback settings (FR-31), shared by the Android and desktop Players (Epic 55,
// story 55.08): their values, keys and defaults. Each Player names them in its own words.

/** A subtitle size (FR-31): small, medium, large, as a scale of the player's default. */
enum class SubtitleSize(val wire: String, val scale: Float) {
    Small("s", 0.8f),
    Medium("m", 1f),
    Large("l", 1.3f),
}

/** A streaming quality (FR-31), sent as `max_bitrate_kbps` (null: the original, no cap). */
enum class StreamQuality(val wire: String, val maxBitrateKbps: Int?, val height: Int?) {
    Original("original", null, null),
    P1080("1080p", 8_000, 1080),
    P720("720p", 4_000, 720),
    P480("480p", 1_500, 480),
}

/** A language to prefer: the video's own default, English or Arabic. */
enum class SubtitleLanguage(val tag: String?) {
    Default(null),
    English("en"),
    Arabic("ar"),
    ;

    companion object {
        /**
         * The stored value: a language tag ("en", "ar"; what is written), or the name 48.31
         * wrote before the stores were one ("english", "arabic", "default").
         */
        fun of(stored: String?): SubtitleLanguage {
            val v = stored?.trim()?.takeIf(String::isNotEmpty) ?: return Default
            return entries.firstOrNull { it.tag.equals(v, ignoreCase = true) || it.name.equals(v, ignoreCase = true) } ?: Default
        }
    }
}

/**
 * FR-31's playback settings, kept on this device: Profile's Settings writes them and the player
 * reads them (autoplay next, subtitles on and their language and size, the audio language, the
 * quality to start at on Wi-Fi and on mobile data, resume without asking). A desktop has no
 * mobile data: it starts at [qualityWifi].
 */
data class PlaybackValues(
    val autoplayNext: Boolean = true,
    val subtitlesOn: Boolean = false,
    val subtitleLanguage: SubtitleLanguage = SubtitleLanguage.Default,
    val subtitleSize: SubtitleSize = SubtitleSize.Medium,
    val qualityWifi: StreamQuality = StreamQuality.Original,
    val qualityMobile: StreamQuality = StreamQuality.P720,
    val resumeWithoutAsking: Boolean = true,
    /** The audio language to prefer (the video's own default when [SubtitleLanguage.Default]). */
    val audioLanguage: SubtitleLanguage = SubtitleLanguage.Default,
) {
    /** The quality a video starts at (`max_bitrate_kbps`; null: the original): mobile data's on a metered network. */
    fun startKbps(metered: Boolean): Int? = (if (metered) qualityMobile else qualityWifi).maxBitrateKbps
}

/**
 * The one store of the playback settings (`playback.*` keys in the app's settings), which
 * Profile's Settings and the player both read and write. 48.32's player kept its own on the same
 * keys; the subtitle language was the one it wrote differently (a tag, where Profile wrote a
 * name): both are read, a tag is written.
 */
class PlaybackSettings(private val store: KeyValueStore) {
    private val _value = MutableStateFlow(read())
    val value: StateFlow<PlaybackValues> = _value.asStateFlow()
    val current: PlaybackValues get() = _value.value

    fun update(change: (PlaybackValues) -> PlaybackValues) {
        val next = change(_value.value)
        _value.value = next
        store.put(AUTOPLAY, next.autoplayNext.toString())
        store.put(SUBTITLES, next.subtitlesOn.toString())
        store.put(SUBTITLE_LANGUAGE, next.subtitleLanguage.tag)
        store.put(SUBTITLE_SIZE, next.subtitleSize.wire)
        store.put(QUALITY_WIFI, next.qualityWifi.wire)
        store.put(QUALITY_MOBILE, next.qualityMobile.wire)
        store.put(RESUME, next.resumeWithoutAsking.toString())
        store.put(AUDIO_LANGUAGE, next.audioLanguage.tag)
    }

    private fun read(): PlaybackValues {
        val d = PlaybackValues()
        fun bool(key: String, default: Boolean) = store.get(key)?.toBooleanStrictOrNull() ?: default
        return PlaybackValues(
            autoplayNext = bool(AUTOPLAY, d.autoplayNext),
            subtitlesOn = bool(SUBTITLES, d.subtitlesOn),
            subtitleLanguage = SubtitleLanguage.of(store.get(SUBTITLE_LANGUAGE)),
            subtitleSize = SubtitleSize.entries.firstOrNull { it.wire == store.get(SUBTITLE_SIZE) } ?: d.subtitleSize,
            qualityWifi = StreamQuality.entries.firstOrNull { it.wire == store.get(QUALITY_WIFI) } ?: d.qualityWifi,
            qualityMobile = StreamQuality.entries.firstOrNull { it.wire == store.get(QUALITY_MOBILE) } ?: d.qualityMobile,
            resumeWithoutAsking = bool(RESUME, d.resumeWithoutAsking),
            audioLanguage = SubtitleLanguage.of(store.get(AUDIO_LANGUAGE)),
        )
    }

    companion object {
        const val AUTOPLAY = "playback.autoplay_next"
        const val SUBTITLES = "playback.subtitles_on"
        const val SUBTITLE_LANGUAGE = "playback.subtitle_language"
        const val SUBTITLE_SIZE = "playback.subtitle_size"
        const val QUALITY_WIFI = "playback.quality_wifi"
        const val QUALITY_MOBILE = "playback.quality_mobile"
        const val RESUME = "playback.resume_without_asking"
        const val AUDIO_LANGUAGE = "playback.audio_language"
    }
}
