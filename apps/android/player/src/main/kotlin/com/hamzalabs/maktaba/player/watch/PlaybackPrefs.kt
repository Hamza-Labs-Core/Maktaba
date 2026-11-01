package com.hamzalabs.maktaba.player.watch

import android.content.Context
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.platform.LocalContext
import com.hamzalabs.maktaba.core.session.KeyValueStore
import com.hamzalabs.maktaba.core.session.PreferencesStore
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

/**
 * The player's settings, kept on this device (FR-31): play the next episode automatically (on),
 * subtitles on by default (off) and their language. Profile and Settings (48.31) shows them;
 * the player reads them. Kept in the app's "settings" preferences, beside the theme.
 */
class PlaybackPrefs(private val store: KeyValueStore) {
    data class Values(
        val autoplayNext: Boolean = true,
        val subtitlesOn: Boolean = false,
        /** A language tag ("en", "ar"); null: the system's. */
        val subtitleLanguage: String? = null,
    )

    private val _values = MutableStateFlow(read())
    val values: StateFlow<Values> = _values.asStateFlow()
    val current: Values get() = _values.value

    fun setAutoplayNext(on: Boolean) = update { it.copy(autoplayNext = on) }
    fun setSubtitlesOn(on: Boolean) = update { it.copy(subtitlesOn = on) }
    fun setSubtitleLanguage(tag: String?) = update { it.copy(subtitleLanguage = tag?.takeIf(String::isNotBlank)) }

    private fun update(change: (Values) -> Values) {
        val v = change(_values.value)
        _values.value = v
        store.put(AUTOPLAY_NEXT, v.autoplayNext.toString())
        store.put(SUBTITLES_ON, v.subtitlesOn.toString())
        store.put(SUBTITLE_LANGUAGE, v.subtitleLanguage)
    }

    private fun read() = Values(
        autoplayNext = store.get(AUTOPLAY_NEXT)?.toBooleanStrictOrNull() ?: true,
        subtitlesOn = store.get(SUBTITLES_ON)?.toBooleanStrictOrNull() ?: false,
        subtitleLanguage = store.get(SUBTITLE_LANGUAGE)?.takeIf(String::isNotBlank),
    )

    companion object {
        const val AUTOPLAY_NEXT = "playback.autoplay_next"
        const val SUBTITLES_ON = "playback.subtitles_on"
        const val SUBTITLE_LANGUAGE = "playback.subtitle_language"

        /** The app's: the "settings" preferences the theme is kept in. */
        fun of(context: Context) = PlaybackPrefs(PreferencesStore(context.applicationContext, "settings"))
    }
}

/** The player's settings; a test provides its own. Null: the app's ([PlaybackPrefs.of]). */
val LocalPlaybackPrefs = staticCompositionLocalOf<PlaybackPrefs?> { null }

@Composable
fun playbackPrefs(): PlaybackPrefs {
    val given = LocalPlaybackPrefs.current
    val context = LocalContext.current
    return given ?: remember(context) { PlaybackPrefs.of(context) }
}
