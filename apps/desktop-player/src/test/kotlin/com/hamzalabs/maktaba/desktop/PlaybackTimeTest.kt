package com.hamzalabs.maktaba.desktop

import com.hamzalabs.maktaba.desktop.watch.TimeUp
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.asCoroutineDispatcher
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withContext
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import java.time.Instant
import java.util.Collections
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicInteger

/**
 * Epic 57 story 57.15 (T05) with a fake player: the home answers `POST /api/stream/sessions`
 * with 403 `parental-time` while [over] is set. The Player then shows the time's-up state, plays
 * nothing and does not ask again by itself; Check again is the viewer's tap.
 */
class PlaybackTimeTest {
    private val home = TestHome()
    private val main = Executors.newSingleThreadExecutor { r -> Thread(r, "test-main").apply { isDaemon = true } }.asCoroutineDispatcher()
    private val scope = CoroutineScope(SupervisorJob() + main)
    private val background = CoroutineScope(SupervisorJob() + main)
    private val asked = AtomicInteger()
    private val over = AtomicBoolean(true)

    private class FakeVideo(val url: String) : Video {
        @Volatile var disposed = false
        override fun toggle() {}
        override fun pause() {}
        override fun seek(ms: Long) {}
        override fun sound(volume: Float, muted: Boolean) {}
        override fun dispose() {
            disposed = true
        }
    }

    private val made: MutableList<FakeVideo> = Collections.synchronizedList(mutableListOf())
    private val players = Players { url, _, _, _ -> FakeVideo(url).also { made += it } }
    private val playback = Playback(home.client, scope, background, signedOut = {}, players = players)

    init {
        home.answer("POST /api/stream/sessions") {
            val n = asked.incrementAndGet()
            if (over.get()) {
                TestHome.json(
                    """{"type":"https://maktaba.tv/problems/parental-time","title":"watching time is over","status":403,""" +
                        """"code":"parental_time","reason":"limit_reached","next_allowed_at":"2026-11-12T04:00:00Z"}""",
                    403,
                )
            } else {
                TestHome.json("""{"session_id":"s$n","mode":"direct","direct_url":"/stream/s$n.mp4"}""")
            }
        }
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
            if (System.currentTimeMillis() > end) throw AssertionError("never: $what")
            Thread.sleep(20)
        }
    }

    @Test
    fun aRefusedVideoShowsTimesUpAndIsNotAskedForAgain() {
        onMain { playback.play(PlayRequest("A", "A", startSec = 754)) }
        until("the time's-up state") { playback.timeUp != null }
        assertEquals(TimeUp("limit_reached", Instant.parse("2026-11-12T04:00:00Z"), positionMs = 754_000), onMain { playback.timeUp })
        assertNull("nothing plays", onMain { playback.player })
        assertNull("it is not the general failure", onMain { playback.problem })
        // the Player does not retry
        Thread.sleep(300)
        assertEquals(1, asked.get())
        // no player was ever made for it
        assertEquals(0, made.size)
    }

    @Test
    fun checkAgainOpensTheVideoWhereItStoppedOnceAParentGaveMoreTime() {
        onMain { playback.play(PlayRequest("A", "A", startSec = 754)) }
        until("the time's-up state") { playback.timeUp != null }
        over.set(false)
        onMain { playback.checkAgain() }
        until("the video plays") { playback.player != null }
        assertNull(onMain { playback.timeUp })
        assertEquals(2, asked.get())
        val body = home.asked("POST /api/stream/sessions").last().body?.utf8().orEmpty()
        assertEquals("it opens where it stopped: $body", true, body.contains("754"))
    }

    @Test
    fun checkAgainWhileTheTimeIsStillOverShowsTheScreenAgain() {
        onMain { playback.play(PlayRequest("A", "A", startSec = 0)) }
        until("the time's-up state") { playback.timeUp != null }
        onMain { playback.checkAgain() }
        until("asked once more") { asked.get() == 2 && playback.timeUp != null }
        assertNull(onMain { playback.player })
    }

    @Test
    fun closingTheVideoClearsTheState() {
        onMain { playback.play(PlayRequest("A", "A", startSec = 0)) }
        until("the time's-up state") { playback.timeUp != null }
        onMain { playback.stop() }
        assertNull(onMain { playback.timeUp })
    }
}
