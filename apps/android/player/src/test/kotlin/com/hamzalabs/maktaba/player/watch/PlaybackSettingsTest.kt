package com.hamzalabs.maktaba.player.watch

import com.hamzalabs.maktaba.core.MapStore
import com.hamzalabs.maktaba.player.profile.PlaybackSettings
import com.hamzalabs.maktaba.player.profile.StreamQuality
import com.hamzalabs.maktaba.player.profile.SubtitleLanguage
import com.hamzalabs.maktaba.player.profile.SubtitleSize
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/**
 * The one playback settings store (48.33): Profile's Settings (48.31) and the player (48.32)
 * each kept one on the same `playback.*` keys; they are one now, and what either wrote reads.
 */
class PlaybackSettingsTest {
    @Test
    fun whatThePlayersOwnStoreWroteStillReads() {
        // 48.32's PlaybackPrefs: booleans and a language tag
        val store = MapStore()
        store.put("playback.autoplay_next", "false")
        store.put("playback.subtitles_on", "true")
        store.put("playback.subtitle_language", "ar")
        val v = PlaybackSettings(store).current
        assertEquals(false, v.autoplayNext)
        assertEquals(true, v.subtitlesOn)
        assertEquals(SubtitleLanguage.Arabic, v.subtitleLanguage)
    }

    @Test
    fun whatProfileWroteReadsAndIsWrittenAsATag() {
        // 48.31 wrote the language's name
        val store = MapStore()
        store.put("playback.subtitle_language", "english")
        store.put("playback.quality_wifi", "1080p")
        store.put("playback.subtitle_size", "l")
        val settings = PlaybackSettings(store)
        assertEquals(SubtitleLanguage.English, settings.current.subtitleLanguage)
        assertEquals(StreamQuality.P1080, settings.current.qualityWifi)
        assertEquals(SubtitleSize.Large, settings.current.subtitleSize)

        settings.update { it.copy(autoplayNext = false) }
        assertEquals("en", store.get("playback.subtitle_language"))
        settings.update { it.copy(subtitleLanguage = SubtitleLanguage.Default) }
        assertNull("the video's own default: nothing stored", store.get("playback.subtitle_language"))
        assertEquals(SubtitleLanguage.Default, PlaybackSettings(store).current.subtitleLanguage)
        assertEquals(SubtitleLanguage.Default, SubtitleLanguage.of("klingon"))
    }

    @Test
    fun aVideoStartsAtWiFisOrMobileDatasQuality() {
        val settings = PlaybackSettings(MapStore())
        // defaults: the original on Wi-Fi, 720p on mobile data
        assertNull(settings.current.startKbps(metered = false))
        assertEquals(4_000, settings.current.startKbps(metered = true))
        settings.update { it.copy(qualityWifi = StreamQuality.P1080, qualityMobile = StreamQuality.P480) }
        assertEquals(8_000, settings.current.startKbps(metered = false))
        assertEquals(1_500, settings.current.startKbps(metered = true))
    }
}
