package com.hamzalabs.maktaba.player.watch

import com.hamzalabs.maktaba.player.profile.SubtitleLanguage
import com.hamzalabs.maktaba.player.profile.PlaybackSettings
import com.hamzalabs.maktaba.core.MapStore
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.Episode
import com.hamzalabs.maktaba.core.api.PlaybackState
import com.hamzalabs.maktaba.core.api.Rendition
import com.hamzalabs.maktaba.core.api.Season
import com.hamzalabs.maktaba.core.api.SeriesEpisodes
import com.hamzalabs.maktaba.core.api.VideoDetail
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.util.Locale

/** The player's decisions, without a player: next, Up next's moment, seeking, quality, Settings, resume. */
class WatchLogicTest {
    private fun ep(id: String) = Episode(videoId = id)

    private val series = SeriesEpisodes(
        seasons = listOf(
            Season(1, listOf(ep("a1"), ep("a2"))),
            Season(2, listOf(ep("b1"))),
        ),
    )

    @Test
    fun theNextEpisodeFollowsTheSeasonsOrder() {
        assertEquals("a2", nextEpisode(series, "a1")?.videoId)
        // across a season
        assertEquals("b1", nextEpisode(series, "a2")?.videoId)
        // the last has none, nor a video not in the series
        assertNull(nextEpisode(series, "b1"))
        assertNull(nextEpisode(series, "zz"))
    }

    @Test
    fun theSeriesNextIsReadFromTheHomeAndNothingWhenItFails() = kotlinx.coroutines.runBlocking {
        val server = mockwebserver3.MockWebServer()
        server.dispatcher = object : mockwebserver3.Dispatcher() {
            override fun dispatch(request: mockwebserver3.RecordedRequest): mockwebserver3.MockResponse {
                val body = when (request.url.encodedPath) {
                    "/api/me/videos/a1" -> """{"video_id":"a1","series":{"id":"s1","name":"S"}}"""
                    "/api/series/s1/episodes" -> """{"seasons":[{"season":1,"episodes":[{"video_id":"a1"},{"video_id":"a2","title":"Two","duration_sec":60}]}]}"""
                    else -> return mockwebserver3.MockResponse.Builder().code(404).build()
                }
                return mockwebserver3.MockResponse.Builder().code(200).setHeader("Content-Type", "application/json").body(body).build()
            }
        }
        server.start()
        try {
            val tokens = com.hamzalabs.maktaba.core.MemoryTokens(com.hamzalabs.maktaba.core.jwt(System.currentTimeMillis() / 1000 + 3600), "r")
            val client = ApiClient(server.url("/"), tokens)
            // the video's own page is missing (404): the episode's title stands in
            val next = client.nextVideo("a1")
            assertEquals("a2", next?.id)
            assertEquals("Two", next?.displayTitle)
            // not in a series (404 on /api/me/videos): no next, no failure
            assertNull(client.nextVideo("lonely"))
        } finally {
            server.close()
        }
    }

    @Test
    fun upNextIsDueInTheLastTenSecondsOrAtTheEnd() {
        assertFalse(upNextDue(positionMs = 0, durationMs = 600_000, ended = false))
        assertFalse(upNextDue(positionMs = 589_000, durationMs = 600_000, ended = false))
        assertTrue(upNextDue(positionMs = 590_000, durationMs = 600_000, ended = false))
        assertTrue(upNextDue(positionMs = 0, durationMs = 0, ended = true))
        // a length not known yet: not due
        assertFalse(upNextDue(positionMs = 5_000, durationMs = 0, ended = false))
    }

    @Test
    fun aDoubleTapSeeksByItsSideAndMirrorsInArabic() {
        assertEquals(-SEEK_MS, doubleTapSeekMs(100f, 1000f, rtl = false))
        assertEquals(SEEK_MS, doubleTapSeekMs(900f, 1000f, rtl = false))
        // right to left: the right side is where the video starts
        assertEquals(SEEK_MS, doubleTapSeekMs(100f, 1000f, rtl = true))
        assertEquals(-SEEK_MS, doubleTapSeekMs(900f, 1000f, rtl = true))
    }

    @Test
    fun qualityIsAutoThenTheLadderFromTheHighest() {
        val ladder = listOf(Rendition(480, 854, 1200), Rendition(1080, 1920, 6000), Rendition(720, 1280, 3000), Rendition(720, 1280, 2500))
        assertEquals(
            listOf(QualityOption.Auto, QualityOption(1080, 6000), QualityOption(720, 3000), QualityOption(480, 1200)),
            QualityOption.of(ladder),
        )
        // direct play names no ladder: the streaming service's own rungs
        assertEquals(listOf(null, 1080, 720, 480), QualityOption.of(emptyList()).map { it.height })
    }

    @Test
    fun settingsStartWithAutoplayOnAndSubtitlesOffAndAreKept() {
        val store = MapStore()
        val prefs = PlaybackSettings(store)
        assertEquals(true, prefs.current.autoplayNext)
        assertEquals(false, prefs.current.subtitlesOn)
        assertEquals(SubtitleLanguage.Default, prefs.current.subtitleLanguage)
        prefs.update { it.copy(autoplayNext = false, subtitlesOn = true, subtitleLanguage = SubtitleLanguage.Arabic) }
        val kept = PlaybackSettings(store).current
        assertEquals(listOf(false, true), listOf(kept.autoplayNext, kept.subtitlesOn))
        assertEquals(SubtitleLanguage.Arabic, kept.subtitleLanguage)
    }

    @Test
    fun aVideoOpensAtItsResumePointUnlessFinished() {
        fun detail(state: PlaybackState?) = VideoDetail(id = "v", playbackState = state)
        assertEquals(95, resumeSec(detail(PlaybackState(positionSec = 95.7))))
        assertEquals(0, resumeSec(detail(PlaybackState(positionSec = 95.7, completed = true))))
        assertEquals(0, resumeSec(detail(null)))
        assertEquals(0, resumeSec(null))
    }

    @Test
    fun aTrackIsNamedByItsLabelElseItsLanguage() {
        val t = TrackOption(type = 3, group = 0, track = 0, label = null, language = "ar", selected = false)
        assertEquals("Arabic", t.name(Locale.ENGLISH))
        assertEquals("العربية", t.name(Locale.forLanguageTag("ar")))
        assertEquals("English", t.copy(label = "English", language = "en").name(Locale.forLanguageTag("ar")))
        assertNull(t.copy(language = null).name(Locale.ENGLISH))
        assertNull(t.copy(language = "und").name(Locale.ENGLISH))
    }
}
