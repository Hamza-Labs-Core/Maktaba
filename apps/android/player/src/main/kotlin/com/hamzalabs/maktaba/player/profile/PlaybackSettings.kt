package com.hamzalabs.maktaba.player.profile

import android.content.Context
import androidx.annotation.StringRes
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.platform.LocalContext
import com.hamzalabs.maktaba.core.session.KeyValueStore
import com.hamzalabs.maktaba.core.session.PreferencesStore
import com.hamzalabs.maktaba.player.R
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

/** A subtitle size (FR-31): small, medium, large, as a scale of the player's default. */
enum class SubtitleSize(val wire: String, val scale: Float, @param:StringRes val label: Int) {
    Small("s", 0.8f, R.string.profile_subtitle_small),
    Medium("m", 1f, R.string.profile_subtitle_medium),
    Large("l", 1.3f, R.string.profile_subtitle_large),
}

/** A streaming quality (FR-31), sent as `max_bitrate_kbps` (null: the original, no cap). */
enum class StreamQuality(val wire: String, val maxBitrateKbps: Int?, @param:StringRes val label: Int) {
    Original("original", null, R.string.profile_quality_original),
    P1080("1080p", 8_000, R.string.profile_quality_1080),
    P720("720p", 4_000, R.string.profile_quality_720),
    P480("480p", 1_500, R.string.profile_quality_480),
}

/** A subtitle language to prefer: the video's own default, English or Arabic. */
enum class SubtitleLanguage(val tag: String?, @param:StringRes val label: Int) {
    Default(null, R.string.profile_subtitle_language_default),
    English("en", R.string.profile_language_english),
    Arabic("ar", R.string.profile_language_arabic),
}

/** FR-31's playback settings, kept on this device; the player (48.32) reads them. */
data class Playback(
    val autoplayNext: Boolean = true,
    val subtitlesOn: Boolean = false,
    val subtitleLanguage: SubtitleLanguage = SubtitleLanguage.Default,
    val subtitleSize: SubtitleSize = SubtitleSize.Medium,
    val qualityWifi: StreamQuality = StreamQuality.Original,
    val qualityMobile: StreamQuality = StreamQuality.P720,
    val resumeWithoutAsking: Boolean = true,
)

/** The playback settings on a device's store (`playback.*` keys, beside the theme). */
class PlaybackSettings(private val store: KeyValueStore) {
    private val _value = MutableStateFlow(read())
    val value: StateFlow<Playback> = _value.asStateFlow()

    fun update(change: (Playback) -> Playback) {
        val next = change(_value.value)
        _value.value = next
        store.put(AUTOPLAY, next.autoplayNext.toString())
        store.put(SUBTITLES, next.subtitlesOn.toString())
        store.put(SUBTITLE_LANGUAGE, next.subtitleLanguage.name.lowercase())
        store.put(SUBTITLE_SIZE, next.subtitleSize.wire)
        store.put(QUALITY_WIFI, next.qualityWifi.wire)
        store.put(QUALITY_MOBILE, next.qualityMobile.wire)
        store.put(RESUME, next.resumeWithoutAsking.toString())
    }

    private fun read(): Playback {
        val d = Playback()
        fun bool(key: String, default: Boolean) = store.get(key)?.toBooleanStrictOrNull() ?: default
        return Playback(
            autoplayNext = bool(AUTOPLAY, d.autoplayNext),
            subtitlesOn = bool(SUBTITLES, d.subtitlesOn),
            subtitleLanguage = SubtitleLanguage.entries.firstOrNull { it.name.equals(store.get(SUBTITLE_LANGUAGE), true) } ?: d.subtitleLanguage,
            subtitleSize = SubtitleSize.entries.firstOrNull { it.wire == store.get(SUBTITLE_SIZE) } ?: d.subtitleSize,
            qualityWifi = StreamQuality.entries.firstOrNull { it.wire == store.get(QUALITY_WIFI) } ?: d.qualityWifi,
            qualityMobile = StreamQuality.entries.firstOrNull { it.wire == store.get(QUALITY_MOBILE) } ?: d.qualityMobile,
            resumeWithoutAsking = bool(RESUME, d.resumeWithoutAsking),
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

        @Volatile private var shared: PlaybackSettings? = null

        /** The app's one instance, on the device's settings. */
        fun of(context: Context): PlaybackSettings = shared ?: synchronized(this) {
            shared ?: PlaybackSettings(PreferencesStore(context.applicationContext, "settings")).also { shared = it }
        }
    }
}

/** The playback settings a screen reads; a test provides its own. */
val LocalPlaybackSettings = staticCompositionLocalOf<PlaybackSettings?> { null }

@Composable
fun playbackSettings(): PlaybackSettings {
    LocalPlaybackSettings.current?.let { return it }
    val context = LocalContext.current
    return remember(context) { PlaybackSettings.of(context) }
}
