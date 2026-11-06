package com.hamzalabs.maktaba.desktop.libraries

import com.hamzalabs.maktaba.desktop.TestHome
import kotlinx.coroutines.async
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.util.concurrent.CountDownLatch

/** Story 55.06 T04–T05. */
class SeriesStateTest {
    private val episodes = """{"seasons":[
        {"season":1,"episodes":[{"video_id":"e1","episode":1,"title":"One","watched":true},{"video_id":"e2","episode":2,"title":"Two","progress_pct":40}]},
        {"season":2,"episodes":[{"video_id":"e3","episode":1,"title":"Three"}]}]}"""

    private fun fake() = TestHome().apply {
        answer("GET /api/series/s1", """{"id":"s1","name":"Seerah","episode_count":3,"watched_count":1}""")
        answer("GET /api/series/s1/episodes", episodes)
        answer("POST /api/me/watched", """{"updated":2}""")
    }

    @Test
    fun continueWatchingIsTheFirstUnwatchedEpisode() = fake().use { f ->
        runBlocking {
            val s = SeriesState(f.client, "s1") {}
            s.load()
            assertEquals("e2", s.continueEpisode()?.videoId)
            assertEquals(1, s.season)
            s.mark(s.seasonIds(1), true)
            // the season is watched: Continue moves to the next season
            assertEquals("e3", s.continueEpisode()?.videoId)
        }
    }

    @Test
    fun aMarkedSeasonStaysWatchedWhenAnOlderReadAnswers() = fake().use { f ->
        runBlocking {
            val s = SeriesState(f.client, "s1") {}
            s.load()
            val gate = CountDownLatch(1)
            f.held("GET /api/series/s1/episodes", { episodes }, gate)
            val read = async { s.load() }
            while (f.asked("GET /api/series/s1/episodes").size < 2) delay(10)
            s.mark(s.seasonIds(1), true)
            gate.countDown()
            read.await()
            assertTrue(s.episodes!!.seasons[0].episodes.all { it.watched })
            assertTrue(f.asked("POST /api/me/watched").single().body!!.utf8().contains("\"watched\":true"))
        }
    }

    @Test
    fun aFailedMarkIsUndone() = TestHome().use { f ->
        f.answer("GET /api/series/s1", """{"id":"s1","name":"Seerah"}""")
        f.answer("GET /api/series/s1/episodes", episodes)
        f.answer("POST /api/me/watched", "{}", 500)
        runBlocking {
            val s = SeriesState(f.client, "s1") {}
            s.load()
            s.mark(listOf("e2"), true)
            assertEquals(false, s.episodes!!.seasons[0].episodes[1].watched)
            assertTrue(s.saveFailed)
        }
    }
}
