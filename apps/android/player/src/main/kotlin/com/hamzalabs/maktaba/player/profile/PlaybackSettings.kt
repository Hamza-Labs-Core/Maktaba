package com.hamzalabs.maktaba.player.profile

import android.content.Context
import androidx.annotation.StringRes
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.platform.LocalContext
import com.hamzalabs.maktaba.core.playback.PlaybackSettings
import com.hamzalabs.maktaba.core.playback.StreamQuality
import com.hamzalabs.maktaba.core.playback.SubtitleLanguage
import com.hamzalabs.maktaba.core.playback.SubtitleSize
import com.hamzalabs.maktaba.core.session.PreferencesStore
import com.hamzalabs.maktaba.player.R

// The playback settings' values, keys and store are :client's (core.playback, shared with the
// desktop Player); this file names them in the app's words and keeps the app's one store.

/** A subtitle size's words. */
@get:StringRes
val SubtitleSize.label: Int
    get() = when (this) {
        SubtitleSize.Small -> R.string.profile_subtitle_small
        SubtitleSize.Medium -> R.string.profile_subtitle_medium
        SubtitleSize.Large -> R.string.profile_subtitle_large
    }

/** A streaming quality's words. */
@get:StringRes
val StreamQuality.label: Int
    get() = when (this) {
        StreamQuality.Original -> R.string.profile_quality_original
        StreamQuality.P1080 -> R.string.profile_quality_1080
        StreamQuality.P720 -> R.string.profile_quality_720
        StreamQuality.P480 -> R.string.profile_quality_480
    }

/** A subtitle language's words. */
@get:StringRes
val SubtitleLanguage.label: Int
    get() = when (this) {
        SubtitleLanguage.Default -> R.string.profile_subtitle_language_default
        SubtitleLanguage.English -> R.string.profile_language_english
        SubtitleLanguage.Arabic -> R.string.profile_language_arabic
    }

@Volatile private var shared: PlaybackSettings? = null

/** The app's one instance, on the device's settings ("settings", beside the theme). */
fun PlaybackSettings.Companion.of(context: Context): PlaybackSettings = shared ?: synchronized(PlaybackSettings) {
    shared ?: PlaybackSettings(PreferencesStore(context.applicationContext, "settings")).also { shared = it }
}

/** The playback settings a screen reads; a test provides its own. */
val LocalPlaybackSettings = staticCompositionLocalOf<PlaybackSettings?> { null }

@Composable
fun playbackSettings(): PlaybackSettings {
    LocalPlaybackSettings.current?.let { return it }
    val context = LocalContext.current
    return remember(context) { PlaybackSettings.of(context) }
}
