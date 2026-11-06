package com.hamzalabs.maktaba.core.playback

import com.hamzalabs.maktaba.core.api.Episode
import com.hamzalabs.maktaba.core.api.Rendition
import com.hamzalabs.maktaba.core.api.Season
import com.hamzalabs.maktaba.core.api.SeriesEpisodes
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** The track, quality and Up next rules both Players follow (story 55.08 T02). */
class TracksTest {
    @Test
    fun aNewVideoFollowsTheLastChoiceElseTheSettings() {
        val prefs = PlaybackValues(subtitlesOn = true, subtitleLanguage = SubtitleLanguage.Arabic, audioLanguage = SubtitleLanguage.English)
        // no choice yet: the settings
        assertEquals(TrackPlan(audioLanguage = "en", textOn = true, textLanguage = "ar"), TrackChoice().plan(prefs))
        // off stays off
        assertEquals(TrackPlan("en", textOn = false, textLanguage = null), TrackChoice(textOff = true).plan(prefs))
        // a language carries over
        assertEquals(TrackPlan("ar", true, "en"), TrackChoice(audioLanguage = "ar", textOff = false, textLanguage = "en").plan(prefs))
        // the defaults: subtitles off, the video's own audio
        assertEquals(TrackPlan(null, false, null), TrackChoice().plan(PlaybackValues()))
    }

    @Test
    fun aTrackIsPickedByLanguageElseTheFirst() {
        assertEquals(1, pickTrack(listOf("ar", "en"), "en"))
        assertEquals(1, pickTrack(listOf("ara", "eng"), "en-GB"))
        assertEquals(0, pickTrack(listOf("ar", "en"), "fr"))
        assertEquals(0, pickTrack(listOf("ar", null), null))
        assertNull(pickTrack(emptyList(), "en"))
        assertTrue(sameLanguage("ar", "ara"))
        assertFalse(sameLanguage("ar", "en"))
    }

    @Test
    fun qualityIsAutoThenTheLadderFromTheHighest() {
        val q = QualityOption.of(listOf(Rendition(480, 854, 1200), Rendition(1080, 1920, 6000), Rendition(1080, 1920, 5000)))
        assertEquals(listOf(QualityOption.Auto, QualityOption(1080, 6000), QualityOption(480, 1200)), q)
        // none named (direct play): the service's own ladder
        assertEquals(4, QualityOption.of(emptyList()).size)
    }

    @Test
    fun upNextIsTheFollowingEpisodeAndIsDueAtTheEnd() {
        val eps = SeriesEpisodes(seasons = listOf(Season(1, listOf(Episode("a"), Episode("b"))), Season(2, listOf(Episode("c")))))
        assertEquals("b", nextEpisode(eps, "a")?.videoId)
        assertEquals("c", nextEpisode(eps, "b")?.videoId)
        assertNull(nextEpisode(eps, "c"))
        assertNull(nextEpisode(eps, "x"))
        assertTrue(upNextDue(95_000, 100_000, ended = false))
        assertFalse(upNextDue(80_000, 100_000, ended = false))
        assertTrue(upNextDue(80_000, 100_000, ended = false, leadSec = 20))
        assertTrue(upNextDue(0, 0, ended = true))
    }
}
