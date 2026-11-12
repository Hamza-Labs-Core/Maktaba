package com.hamzalabs.maktaba.desktop

import com.hamzalabs.maktaba.core.playback.QualityOption
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.Instant
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
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
    private val main = TestMain(home)
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
    private val playback = Playback(home.client, main.scope, main.background, signedOut = {}, players = players, nowMs = main.nowMs)

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
        main.run { playback.close() }
        main.close()
        home.close()
    }

    private fun <T> onMain(block: () -> T): T = main.run(block)

    private fun until(what: String, ok: () -> Boolean) = main.until(what, ok)

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
        main.pass(60_000)
        assertEquals(1, opens.get())
        assertEquals(posted, posts())
        assertNotNull("the request stays for Check again", onMain { playback.request })
    }

    private val lapsed = """{"type":"https://maktaba.tv/problems/parental-time","title":"watching time is over","status":403,""" +
        """"code":"parental_time","reason":"no_heartbeat"}"""

    @Test
    fun aLapsedLeaseReopensTheStreamOnceWhereItIs() {
        // a long pause: the stream's lease lapsed. That is not the time being over
        home.answer("POST /api/stream/sessions/s1/progress", lapsed, 403)
        home.answer("POST /api/stream/sessions/s2/progress", """{"position_sec":754}""")
        playingAt(754_000)
        until("opened again") { opens.get() == 2 && home.asked("DELETE /api/stream/sessions/s1").isNotEmpty() }
        assertNull("no time's-up screen: the home gave the stream again", onMain { playback.timeUp })
        assertNotNull(onMain { playback.player })
        assertTrue("where it was", home.asked("POST /api/stream/sessions").last().body!!.utf8().contains("754"))
        main.pass(60_000)
        assertEquals("once", 2, opens.get())
    }

    /** Every open after the first is answered only once the latch opens ([opens] counts them as asked). */
    private fun slowReopens(): CountDownLatch {
        val gate = CountDownLatch(1)
        home.answer("POST /api/stream/sessions") {
            val n = opens.incrementAndGet()
            if (n > 1) gate.await(STALL_MS, TimeUnit.MILLISECONDS)
            TestHome.json("""{"session_id":"s$n","mode":"direct","direct_url":"/stream/s$n.mp4"}""")
        }
        return gate
    }

    private fun resume() {
        val e = events.get()!!
        onMain { e.playing(false) }
        onMain { e.playing(true) }
    }

    /**
     * A resume's post and the next ones are all refused for one lapse: the reopen on its way
     * answers them all, and a refusal of the session before it, read after the new one plays,
     * says nothing about the new one.
     */
    @Test
    fun lapsesHeardWhileTheStreamIsOpenedAgainAndOnesOfTheSessionBeforeOpenNothingMore() {
        // s1's lease lapsed: every post of it is refused. Its third post is answered late
        val late = CountDownLatch(1)
        val ofS1 = AtomicInteger()
        home.answer("POST /api/stream/sessions/s1/progress") {
            if (ofS1.incrementAndGet() == 3) late.await(STALL_MS, TimeUnit.MILLISECONDS)
            TestHome.json(lapsed, 403)
        }
        home.answer("POST /api/stream/sessions/s2/progress", """{"position_sec":754}""")
        val reopen = slowReopens()
        playingAt(754_000)
        until("the reopen is asked for") { opens.get() == 2 }
        // resumed while the reopen is on its way: that post of s1 is refused too, and read
        resume()
        until("the second post") { posts() == 2 }
        main.settle(held = 1)
        assertEquals("the reopen on its way answers it", 2, opens.get())
        assertNull(onMain { playback.timeUp })
        // this post of s1 is answered only after s2 plays
        resume()
        until("the third post") { posts() == 3 }
        reopen.countDown()
        until("opened again") { home.asked("DELETE /api/stream/sessions/s1").isNotEmpty() && playback.player != null }
        main.settle(held = 1)
        late.countDown()
        main.settle()
        assertEquals("opened again once", 2, opens.get())
        assertNull("no time's-up screen: a refusal of s1 says nothing about s2", onMain { playback.timeUp })
        assertNotNull(onMain { playback.player })
        main.pass(60_000)
        assertEquals(2, opens.get())
        assertNull(onMain { playback.timeUp })
        assertTrue("the new session's own posts went out", home.asked("POST /api/stream/sessions/s2/progress").isNotEmpty())
    }

    /**
     * The lapse stopped the stream itself too (the player failed), and the watch ticks while
     * the stream is being opened again: that open is the one open. The watch opened it a second
     * time for the failure, which left a session nobody closed.
     */
    @Test
    fun aFailedPlayerWhileTheStreamIsOpenedAgainForALapseOpensNothingMore() {
        playingAt(754_000)
        until("the first post") { posts() == 1 }
        // past the half minute after opening, in which a failed player shows why instead
        main.pass(30_000)
        home.answer("POST /api/stream/sessions/s1/progress", lapsed, 403)
        home.answer("POST /api/stream/sessions/s2/progress", """{"position_sec":754}""")
        val reopen = slowReopens()
        resume()
        until("the reopen is asked for") { opens.get() == 2 }
        // the streaming service refused the stream itself as well: the player failed
        val e = events.get()!!
        onMain { e.failed("The stream was refused.") }
        val posted = posts()
        main.advance(10_000)
        until("the watch's post is answered") { posts() == posted + 1 && home.running == 1 }
        onMain { }
        assertEquals("only the reopen is on its way", 1, home.running)
        assertEquals(2, opens.get())
        reopen.countDown()
        until("opened again") { home.asked("DELETE /api/stream/sessions/s1").isNotEmpty() && playback.player != null }
        main.settle()
        assertEquals("the lapse's reopen was the one open", 2, opens.get())
        assertNull(onMain { playback.timeUp })
        assertNull(onMain { playback.problem })
        main.pass(60_000)
        assertEquals(2, opens.get())
        assertTrue("no session is left open behind it", home.asked("DELETE /api/stream/sessions/s2").isEmpty())
    }

    /**
     * "Lapsed again right after its reopen" is about the session that reopen gave. A session
     * opened since (another quality) that lapses is opened again like any other.
     */
    @Test
    fun aLapseOfASessionOpenedSinceTheReopenIsOpenedAgain() {
        home.answer("POST /api/stream/sessions/s1/progress", lapsed, 403)
        home.answer("POST /api/stream/sessions/s2/progress", """{"position_sec":754}""")
        home.answer("POST /api/stream/sessions/s4/progress", """{"position_sec":754}""")
        playingAt(754_000)
        until("opened again") { opens.get() == 2 && playback.player != null && home.asked("DELETE /api/stream/sessions/s1").isNotEmpty() }
        main.settle()
        // another quality: s3 replaces s2, the session the reopen gave
        home.answer("POST /api/stream/sessions/s3/progress", lapsed, 403)
        onMain { playback.chooseQuality(QualityOption(720, 3_000)) }
        until("the quality's session") { opens.get() == 3 && home.asked("DELETE /api/stream/sessions/s2").isNotEmpty() }
        main.settle()
        resume()
        until("s3 opened again") { opens.get() == 4 && home.asked("DELETE /api/stream/sessions/s3").isNotEmpty() }
        main.settle()
        assertNull("not the time's-up screen: s3 was not the reopened session", onMain { playback.timeUp })
        assertNotNull(onMain { playback.player })
        assertEquals(4, opens.get())
    }

    @Test
    fun aLapsedLeaseWhoseReopenIsRefusedShowsTimesUp() {
        home.answer("POST /api/stream/sessions/s1/progress", lapsed, 403)
        // the time ran out meanwhile: the home refuses the reopen
        home.answer("POST /api/stream/sessions") {
            if (opens.incrementAndGet() == 1) TestHome.json("""{"session_id":"s1","mode":"direct","direct_url":"/stream/s1.mp4"}""") else TestHome.json(timeOver, 403)
        }
        playingAt(754_000)
        until("the time's-up state") { playback.timeUp != null }
        assertEquals("limit_reached", onMain { playback.timeUp }!!.reason)
        assertNull(onMain { playback.player })
        main.pass(60_000)
        assertEquals("the first open and the one reopen", 2, opens.get())
    }

    @Test
    fun aLeaseThatLapsesAgainRightAfterItsReopenStops() {
        home.answer("POST /api/stream/sessions/s1/progress", lapsed, 403)
        home.answer("POST /api/stream/sessions/s2/progress", lapsed, 403)
        playingAt(754_000)
        until("opened again") { opens.get() == 2 && playback.player != null && home.asked("DELETE /api/stream/sessions/s1").isNotEmpty() }
        val e = events.get()!!
        onMain { e.playing(false) }
        onMain { e.playing(true) }
        until("the time's-up state") { playback.timeUp != null }
        assertEquals("no_heartbeat", onMain { playback.timeUp }!!.reason)
        main.pass(60_000)
        assertEquals("no loop of opens", 2, opens.get())
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
        main.pass(60_000)
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
