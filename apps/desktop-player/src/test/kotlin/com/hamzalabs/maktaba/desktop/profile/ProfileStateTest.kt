package com.hamzalabs.maktaba.desktop.profile

import com.hamzalabs.maktaba.core.MapStore
import com.hamzalabs.maktaba.core.api.PlaylistDoc
import com.hamzalabs.maktaba.core.api.PlaylistSummary
import com.hamzalabs.maktaba.core.api.VideoCard
import com.hamzalabs.maktaba.core.playback.PlaybackSettings
import com.hamzalabs.maktaba.core.playback.StreamQuality
import com.hamzalabs.maktaba.core.playback.SubtitleLanguage
import com.hamzalabs.maktaba.core.playback.TrackChoice
import com.hamzalabs.maktaba.core.playback.plan
import com.hamzalabs.maktaba.desktop.TestHome
import kotlinx.coroutines.async
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.util.concurrent.CountDownLatch

/** Story 55.09 T01–T06. */
class ProfileStateTest {
    private fun history(from: Int, n: Int) = (from until from + n).joinToString(",", """{"items":[""", "]}") { """{"video_id":"v$it","title":"Video $it"}""" }

    @Test
    fun eachPageLoadsAndLoadMoreAppends() = TestHome().use { f ->
        f.answer("GET /api/me/history") { r -> TestHome.json(if (r.url.queryParameter("offset") == "0") history(0, HISTORY_PAGE) else history(HISTORY_PAGE, 3)) }
        f.answer("GET /api/me/videos") { r -> TestHome.json(if (r.url.queryParameter("cursor") == null) """{"items":[{"id":"f1"}],"next":"c2"}""" else """{"items":[{"id":"f2"}]}""") }
        f.answer("GET /api/me/playlists", """{"items":[{"id":"p1","name":"Ramadan","videos":2}]}""")
        f.answer("GET /api/me/sessions", """{"items":[{"id":"s1","user_agent":"Mozilla/5.0 Firefox/128.0"},{"id":"s2"}]}""")
        runBlocking {
            val s = ProfileState(f.client)
            s.loadHistory()
            assertTrue(s.history.more)
            s.moreHistory()
            assertEquals(HISTORY_PAGE + 3, s.history.items.size)
            s.loadFavorites()
            s.moreFavorites()
            assertEquals(listOf("f1", "f2"), s.favorites.items.map { it.id })
            s.loadPlaylists()
            assertEquals("Ramadan", s.playlists.items.single().name)
            s.loadSessions()
            assertEquals("Firefox", browserName(s.sessions.items[0].userAgent))
        }
    }

    private fun latch(route: String, body: String, f: TestHome, change: suspend (ProfileState) -> Unit, load: suspend (ProfileState) -> Unit, rows: (ProfileState) -> List<String>) {
        runBlocking {
            val s = ProfileState(f.client)
            load(s)
            val gate = CountDownLatch(1)
            f.held(route, { body }, gate)
            val read = async { load(s) }
            while (f.asked(route).size < 2) delay(10)
            change(s)
            gate.countDown()
            read.await()
            assertEquals(listOf("b"), rows(s))
        }
    }

    @Test
    fun aStaleLoadMoreNeverSkipsAPage() = TestHome().use { f ->
        f.answer("GET /api/me/videos") { r -> TestHome.json(if (r.url.queryParameter("cursor") == null) """{"items":[{"id":"a"},{"id":"b"}],"next":"c2"}""" else """{"items":[{"id":"c"}],"next":"c3"}""") }
        f.answer("POST /api/me/favorites", """{"updated":1}""")
        runBlocking {
            val s = ProfileState(f.client)
            s.loadFavorites()
            val gate = CountDownLatch(1)
            f.held("GET /api/me/videos", { """{"items":[{"id":"c"}],"next":"c3"}""" }, gate)
            val read = async { s.moreFavorites() }
            while (f.asked("GET /api/me/videos").size < 2) delay(10)
            // a removal while page 2 is read: that answer is stale and dropped
            s.unfavorite(s.favorites.items.first())
            gate.countDown()
            read.await()
            assertEquals(listOf("b"), s.favorites.items.map { it.id })
            // the cursor did not move past the dropped page: Load more asks for it again
            assertTrue(s.favorites.more)
            s.moreFavorites()
            assertEquals("c2", f.asked("GET /api/me/videos").last().url.queryParameter("cursor"))
        }
    }

    @Test
    fun aForgottenVideoStaysGone() = TestHome().use { f ->
        val body = """{"items":[{"video_id":"a","title":"A"},{"video_id":"b","title":"B"}]}"""
        f.answer("GET /api/me/history", body)
        f.answer("DELETE /api/me/history/a") { TestHome.json("", 204) }
        latch("GET /api/me/history", body, f, { it.forget(it.history.items.first()) }, { it.loadHistory() }) { s -> s.history.items.map { it.videoId } }
    }

    @Test
    fun aRemovedFavoriteStaysGone() = TestHome().use { f ->
        val body = """{"items":[{"id":"a"},{"id":"b"}]}"""
        f.answer("GET /api/me/videos", body)
        f.answer("POST /api/me/favorites", """{"updated":1}""")
        latch("GET /api/me/videos", body, f, { it.unfavorite(it.favorites.items.first()) }, { it.loadFavorites() }) { s -> s.favorites.items.map { it.id } }
        assertTrue(f.asked("POST /api/me/favorites").single().body!!.utf8().contains("\"favorite\":false"))
    }

    @Test
    fun anEndedSessionStaysGone() = TestHome().use { f ->
        val body = """{"items":[{"id":"a"},{"id":"b"}]}"""
        f.answer("GET /api/me/sessions", body)
        f.answer("DELETE /api/me/sessions/a") { TestHome.json("", 204) }
        latch("GET /api/me/sessions", body, f, { it.endSession(it.sessions.items.first()) }, { it.loadSessions() }) { s -> s.sessions.items.map { it.id } }
    }

    @Test
    fun aFailedForgetPutsTheRowBackAndSaysSo() = TestHome().use { f ->
        f.answer("GET /api/me/history", """{"items":[{"video_id":"a","title":"A"},{"video_id":"b","title":"B"}]}""")
        f.answer("DELETE /api/me/history/a") { TestHome.json("{}", 500) }
        runBlocking {
            val s = ProfileState(f.client)
            s.loadHistory()
            s.forget(s.history.items.first())
            assertEquals(listOf("a", "b"), s.history.items.map { it.videoId })
            assertTrue(s.history.failed)
        }
    }

    @Test
    fun anOldHomeWithoutSessionsHidesTheGroup() = TestHome().use { f ->
        runBlocking {
            val s = ProfileState(f.client)
            s.loadSessions()
            assertTrue(s.sessions.missing)
        }
    }

    @Test
    fun thePlaybackSettingsAreWhatWatchingStartsWith() {
        val store = MapStore()
        val settings = PlaybackSettings(store)
        settings.update { it.copy(subtitlesOn = true, subtitleLanguage = SubtitleLanguage.Arabic, qualityWifi = StreamQuality.P480) }
        // a restart reads them back
        val again = PlaybackSettings(store).current
        val plan = TrackChoice().plan(again)
        assertTrue(plan.textOn)
        assertEquals("ar", plan.textLanguage)
        assertEquals(1_500, again.qualityWifi.maxBitrateKbps)
    }

    @Test
    fun aPlaylistPlaysInOrder() {
        val doc = PlaylistDoc(PlaylistSummary("p1", "Ramadan"), listOf(VideoCard("v1", "One"), VideoCard("v2", "Two"), VideoCard("v3", "Three")))
        val r = playlistRequest(doc)
        assertEquals("v1", r.videoId)
        assertEquals(0, r.startSec)
        assertEquals(listOf("v2", "v3"), r.then.map { it.videoId })
    }
}
