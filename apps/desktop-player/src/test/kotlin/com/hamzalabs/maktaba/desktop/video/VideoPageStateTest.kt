package com.hamzalabs.maktaba.desktop.video

import com.hamzalabs.maktaba.desktop.TestHome
import kotlinx.coroutines.async
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.util.concurrent.CountDownLatch

/** Story 55.06 T01–T03. */
class VideoPageStateTest {
    private val detail = """{"id":"v1","title":"Fiqh 7","duration_sec":3000}"""
    private fun mine(pos: Double, favorite: Boolean = false, completed: Boolean = false) =
        """{"video_id":"v1","position_sec":$pos,"completed":$completed,"favorite":$favorite,"series":{"id":"s1","name":"Fiqh"}}"""

    private fun fake(pos: Double = 724.0) = TestHome().apply {
        answer("GET /api/videos/v1", detail)
        answer("GET /api/me/videos/v1", mine(pos))
        answer("GET /api/videos/v1/chapters", """{"items":[{"seq":1,"start_sec":0,"title":"Intro"},{"seq":2,"start_sec":600,"title":"Rules"}]}""")
        answer("GET /api/series/s1/episodes", """{"seasons":[{"season":1,"episodes":[{"video_id":"v0"},{"video_id":"v1","season":1,"episode":7},{"video_id":"v2","title":"Fiqh 8"}]}]}""")
        answer("POST /api/me/favorites", """{"updated":1}""")
    }

    @Test
    fun resumeIsOfferedAtTheSavedSecond() = fake().use { f ->
        runBlocking {
            val s = VideoPageState(f.client, "v1", {})
            s.load()
            assertEquals(724, s.resumeSec)
            assertEquals(listOf(1, 2), s.chapters.map { it.seq })
            assertEquals(listOf("v2"), s.nextEpisodes.map { it.videoId })
            assertEquals(7, s.episode?.episode)
        }
    }

    @Test
    fun nearTheEndThereIsOnlyPlayFromStartAndChaptersOffAreNotAskedFor() = fake(pos = 2_950.0).use { f ->
        runBlocking {
            val s = VideoPageState(f.client, "v1", {}, chaptersOn = { false })
            s.load()
            assertNull(s.resumeSec)
            assertTrue(s.chapters.isEmpty())
            assertTrue(f.asked("GET /api/videos/v1/chapters").isEmpty())
        }
    }

    @Test
    fun favoriteStaysOnWhenAnOlderReadAnswersOff() = fake().use { f ->
        runBlocking {
            val s = VideoPageState(f.client, "v1", {})
            s.load()
            val gate = CountDownLatch(1)
            f.held("GET /api/me/videos/v1", { mine(724.0, favorite = false) }, gate)
            val read = async { s.load() }
            while (f.asked("GET /api/me/videos/v1").size < 2) delay(10)
            s.toggleFavorite()
            gate.countDown()
            read.await()
            assertTrue(s.favorite)
        }
    }

    @Test
    fun aFailedMarkWatchedIsUndone() = fake().use { f ->
        f.answer("POST /api/me/watched", "{}", 500)
        runBlocking {
            val s = VideoPageState(f.client, "v1", {})
            s.load()
            s.toggleWatched()
            assertFalse(s.watched)
            assertTrue(s.saveFailed)
        }
    }

    @Test
    fun aVideoThatIsGoneIsNotAvailable() = TestHome().use { f ->
        runBlocking {
            val s = VideoPageState(f.client, "gone", {})
            s.load()
            assertTrue(s.notAvailable)
        }
    }
}
