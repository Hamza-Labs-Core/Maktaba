package com.hamzalabs.maktaba.desktop

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.asCoroutineDispatcher
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withContext
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.Instant
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.atomic.AtomicReference

/**
 * Epic 57 stories 57.14 and 57.15 (T05) on the desktop Player, with a fake player: the progress
 * post is the stream's heartbeat, so it goes out at once when the video starts or resumes, and
 * its answer is read. 403 `parental-time` stops the video with the time's-up state, 404 is the
 * stream's end, 503 is "cannot check right now"; none of them is asked about again by itself.
 */
class PlaybackProgressTimeTest {
    private val home = TestHome()
    private val main = Executors.newSingleThreadExecutor { r -> Thread(r, "test-main").apply { isDaemon = true } }.asCoroutineDispatcher()
    private val scope = CoroutineScope(SupervisorJob() + main)
    private val background = CoroutineScope(SupervisorJob() + main)
    private val opens = AtomicInteger()
    private val events = AtomicReference<VideoEvents?>()
    private val disposed = AtomicInteger()

    private val players = Players { _, _, _, e ->
        events.set(e)
        object : Video {
            override fun toggle() {}
            override fun pause() {}
            override fun seek(ms: Long) {}
            override fun sound(volume: Float, muted: Boolean) {}
            override fun dispose() {
                disposed.incrementAndGet()
            }
        }
    }
    private val playback = Playback(home.client, scope, background, signedOut = {}, players = players)

    private val timeOver = """{"type":"https://maktaba.tv/problems/parental-time","title":"watching time is over","status":403,""" +
        """"code":"parental_time","reason":"limit_reached","next_allowed_at":"2026-11-12T04:00:00Z"}"""

    init {
        home.answer("POST /api/stream/sessions") {
            val n = opens.incrementAndGet()
            TestHome.json("""{"session_id":"s$n","mode":"direct","direct_url":"/stream/s$n.mp4"}""")
        }
        home.answer("POST /api/stream/sessions/s1/progress", """{"position_sec":1,"completed":false}""")
    }

    @After
    fun tearDown() {
        runBlocking(main) { playback.close() }
        home.close()
        main.close()
    }

    private fun <T> onMain(block: () -> T): T = runBlocking { withContext(main) { block() } }

    private fun until(what: String, ms: Long = 10_000, ok: () -> Boolean) {
        val end = System.currentTimeMillis() + ms
        while (!onMain(ok)) {
            if (System.currentTimeMillis() > end) throw AssertionError("never: $what; the home was asked ${home.seen.map { it.method + " " + it.url.encodedPath }}")
            Thread.sleep(20)
        }
    }

    private fun posts() = home.asked("POST /api/stream/sessions/s1/progress").size

    /** The video plays at [ms]: the fake player says so, as JavaFX would. */
    private fun playingAt(ms: Long) {
        onMain { playback.play(PlayRequest("A", "A", startSec = 0)) }
        until("the video opens") { playback.player != null }
        val e = events.get()!!
        onMain {
            e.position(ms)
            e.playing(true)
        }
    }

    @Test
    fun startingAndResumingPostTheProgressAtOnce() {
        playingAt(42_000)
        until("the first post") { posts() == 1 }
        assertTrue(home.asked("POST /api/stream/sessions/s1/progress").first().body!!.utf8().contains("42"))
        val e = events.get()!!
        onMain { e.playing(false) }
        onMain {
            e.position(43_000)
            e.playing(true)
        }
        until("resumed: posted at once") { posts() == 2 }
    }

    @Test
    fun aProgressPostRefusedForTimeStopsTheVideoWithTheTimesUpState() {
        home.answer("POST /api/stream/sessions/s1/progress", timeOver, 403)
        playingAt(754_000)
        until("the time's-up state") { playback.timeUp != null }
        val up = onMain { playback.timeUp }!!
        assertEquals("limit_reached", up.reason)
        assertEquals(Instant.parse("2026-11-12T04:00:00Z"), up.nextAllowedAt)
        assertEquals("where it stopped", 754_000L, up.positionMs)
        assertNull("nothing plays", onMain { playback.player })
        assertFalse(onMain { playback.playing })
        assertEquals("the player was let go", 1, disposed.get())
        until("the session is closed") { home.asked("DELETE /api/stream/sessions/s1").isNotEmpty() }
        // the Player neither reopens nor posts again
        val posted = posts()
        Thread.sleep(1_500)
        assertEquals(1, opens.get())
        assertEquals(posted, posts())
        assertNotNull("the request stays for Check again", onMain { playback.request })
    }

    @Test
    fun checkAgainAfterARefusedPostOpensWhereItStopped() {
        home.answer("POST /api/stream/sessions/s1/progress", timeOver, 403)
        playingAt(754_000)
        until("the time's-up state") { playback.timeUp != null }
        onMain { playback.checkAgain() }
        until("opened again") { opens.get() == 2 && playback.player != null }
        assertNull(onMain { playback.timeUp })
        assertTrue(home.asked("POST /api/stream/sessions").last().body!!.utf8().contains("754"))
    }

    @Test
    fun aMissingSessionIsTheStreamsEnd() {
        home.answer("POST /api/stream/sessions/s1/progress", """{"title":"not found","status":404}""", 404)
        playingAt(30_000)
        until("ended by the home") { playback.endedByHome }
        assertNull(onMain { playback.player })
        assertNull(onMain { playback.timeUp })
        Thread.sleep(1_200)
        assertEquals("not opened again", 1, opens.get())
    }

    @Test
    fun aHomeThatCannotCheckTheTimeDoesNotStopTheVideo() {
        home.answer("POST /api/stream/sessions/s1/progress") {
            mockwebserver3.MockResponse.Builder().code(503).setHeader("Retry-After", "5").body("""{"status":503}""").build()
        }
        playingAt(30_000)
        until("cannot check") { playback.cannotCheck }
        assertNotNull("the video is still there", onMain { playback.player })
        assertNull(onMain { playback.timeUp })
        // the next post the home takes clears it
        home.answer("POST /api/stream/sessions/s1/progress", """{"position_sec":31}""")
        val e = events.get()!!
        onMain { e.playing(false) }
        onMain { e.playing(true) }
        until("checked again") { !playback.cannotCheck }
    }
}
