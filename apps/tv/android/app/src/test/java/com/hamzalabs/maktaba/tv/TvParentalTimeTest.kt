package com.hamzalabs.maktaba.tv

import android.content.Context
import android.os.Looper
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.core.MemoryTokens
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ErrorKind
import com.hamzalabs.maktaba.core.api.TimeReason
import com.hamzalabs.maktaba.core.jwt
import com.hamzalabs.maktaba.tv.playback.TvPlayback
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Shadows.shadowOf
import java.time.Duration
import java.time.Instant
import java.util.concurrent.CopyOnWriteArrayList

/**
 * Epic 57 story 57.15 (T03, the TV root): the TV's player when the home refuses for time. A
 * session open or a progress post that answers 403 `parental-time` stops the video and keeps
 * the refusal for the time's-up screen, and nothing asks the home again by itself; a 404 on a
 * progress post is the stream's end; a 503 is "cannot check right now". Against a MockWebServer
 * home; the main looper's clock is moved by hand.
 */
@RunWith(AndroidJUnit4::class)
class TvParentalTimeTest {
    private val context: Context = ApplicationProvider.getApplicationContext()
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    private val background = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private lateinit var server: MockWebServer
    private val seen = CopyOnWriteArrayList<String>()
    @Volatile private var open: () -> MockResponse = { opened() }
    @Volatile private var progress: () -> MockResponse = { MockResponse.Builder().code(200).body("{}").build() }
    private var playback: TvPlayback? = null

    private fun opened() = MockResponse.Builder().code(201).setHeader("Content-Type", "application/json")
        .body("""{"session_id":"s1","mode":"direct","direct_url":"/stream/s1/v1.mp4?sig=x","expires_at":"2099-01-01T00:00:00Z"}""").build()

    private fun timeOver(reason: String, next: String? = "2026-11-12T04:00:00Z") = MockResponse.Builder().code(403)
        .setHeader("Content-Type", "application/problem+json")
        .body(
            """{"type":"https://maktaba.tv/problems/parental-time","title":"over","status":403,"code":"parental_time","reason":"$reason"""" +
                (if (next != null) ""","next_allowed_at":"$next"""" else "") + "}",
        ).build()

    @Before
    fun start() {
        server = MockWebServer()
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                val p = request.url.encodedPath
                seen += "${request.method} $p"
                return when {
                    p == "/api/stream/sessions" && request.method == "POST" -> open()
                    p.endsWith("/progress") -> progress()
                    else -> MockResponse.Builder().code(204).build()
                }
            }
        }
        server.start()
    }

    @After
    fun stop() {
        playback?.stop()
        scope.cancel()
        background.cancel()
        server.close()
    }

    private fun playback(startMs: Long = 0): TvPlayback {
        val client = ApiClient(server.url("/"), MemoryTokens(jwt(System.currentTimeMillis() / 1000 + 86_400), "r1"))
        return TvPlayback(context, client, "v1", startMs, scope, background, signedOut = {}).also { playback = it }
    }

    private fun runUntil(timeoutMs: Long = 10_000, condition: () -> Boolean) {
        val until = System.currentTimeMillis() + timeoutMs
        while (!condition()) {
            check(System.currentTimeMillis() < until) { "timed out waiting; the home was asked $seen" }
            shadowOf(Looper.getMainLooper()).idle()
            Thread.sleep(10)
        }
    }

    private fun idleFor(ms: Long) {
        shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(ms))
        Thread.sleep(200)
        shadowOf(Looper.getMainLooper()).idle()
    }

    private fun opens() = seen.count { it == "POST /api/stream/sessions" }
    private fun posts() = seen.count { it.endsWith("/progress") }

    @Test
    fun anOpenRefusedForTimeShowsWhyAndIsNotAskedAgain() {
        open = { timeOver(TimeReason.OUTSIDE_SLOT, "2026-11-12T07:00:00Z") }
        val pb = playback(startMs = 42_000)
        pb.start()
        runUntil { pb.timeUp != null }
        assertEquals(TimeReason.OUTSIDE_SLOT, pb.timeUp!!.refusal.reason)
        assertEquals(Instant.parse("2026-11-12T07:00:00Z"), pb.timeUp!!.refusal.nextAllowedAt)
        assertEquals(42_000L, pb.timeUp!!.positionMs)
        assertNull("the time's-up screen, not the error screen", pb.problem)
        assertNull(pb.waiting)
        assertNull(pb.player)
        assertFalse(pb.opening)
        idleFor(60_000)
        assertEquals("the home is not asked again by itself", 1, opens())
    }

    @Test
    fun aProgressPostRefusedForTimeStopsTheVideo() {
        val pb = playback()
        pb.start()
        runUntil { pb.player != null }
        progress = { timeOver(TimeReason.LIMIT_REACHED) }
        // the player is paused where it is: the position goes out at once and the home refuses
        pb.player!!.seekTo(30_000)
        pb.player!!.playWhenReady = false
        runUntil { pb.timeUp != null }
        assertEquals(TimeReason.LIMIT_REACHED, pb.timeUp!!.refusal.reason)
        assertEquals(Instant.parse("2026-11-12T04:00:00Z"), pb.timeUp!!.refusal.nextAllowedAt)
        assertNull("playback stopped", pb.player)
        runUntil { "DELETE /api/stream/sessions/s1" in seen }
        val posted = posts()
        idleFor(60_000)
        assertEquals("nothing is opened again", 1, opens())
        assertEquals("and nothing more is posted", posted, posts())
    }

    @Test
    fun aLapsedLeaseReopensTheStreamOnceWhereItIs() {
        val pb = playback()
        pb.start()
        runUntil { pb.player != null }
        // a long pause: the stream's lease lapsed. That is not the time being over
        progress = { if (opens() == 1) timeOver(TimeReason.NO_HEARTBEAT, next = null) else MockResponse.Builder().code(200).body("{}").build() }
        pb.player!!.seekTo(30_000)
        pb.player!!.playWhenReady = false
        runUntil { opens() == 2 }
        idleFor(1_000)
        assertNull("no time's-up screen: the home gave the stream again", pb.timeUp)
        assertNull(pb.problem)
        assertNotNull(pb.player)
        assertFalse("it was paused, and still is", pb.player!!.playWhenReady)
        idleFor(60_000)
        assertEquals("once", 2, opens())
    }

    @Test
    fun aLapsedLeaseWhoseReopenIsRefusedShowsTimesUp() {
        val pb = playback()
        pb.start()
        runUntil { pb.player != null }
        progress = { timeOver(TimeReason.NO_HEARTBEAT, next = null) }
        // the time ran out meanwhile: the home refuses the reopen
        open = { timeOver(TimeReason.LIMIT_REACHED) }
        pb.player!!.seekTo(30_000)
        pb.player!!.playWhenReady = false
        runUntil { pb.timeUp != null }
        assertEquals(TimeReason.LIMIT_REACHED, pb.timeUp!!.refusal.reason)
        assertEquals(30_000L, pb.timeUp!!.positionMs)
        assertNull(pb.player)
        idleFor(60_000)
        assertEquals("the first open and the one reopen", 2, opens())
    }

    @Test
    fun aLeaseThatLapsesAgainRightAfterItsReopenStops() {
        val pb = playback()
        pb.start()
        runUntil { pb.player != null }
        progress = { timeOver(TimeReason.NO_HEARTBEAT, next = null) }
        pb.player!!.seekTo(30_000)
        pb.player!!.playWhenReady = false
        runUntil { opens() == 2 }
        // the reopened stream's own post (it is paused again) is refused the same way: no loop of opens
        runUntil { pb.timeUp != null }
        assertEquals(TimeReason.NO_HEARTBEAT, pb.timeUp!!.refusal.reason)
        idleFor(60_000)
        assertEquals(2, opens())
    }

    @Test
    fun theStreamItselfRefusedForNoHeartbeatIsRead() {
        // the streaming service answered a segment with 403 parental-time, reason no_heartbeat
        val body = """{"type":"https://maktaba.tv/problems/parental-time","status":403,"code":"parental_time","reason":"no_heartbeat"}"""
        val http = androidx.media3.datasource.HttpDataSource.InvalidResponseCodeException(
            403, null, null, emptyMap(), androidx.media3.datasource.DataSpec(android.net.Uri.parse("http://home/stream/s1/seg0.ts")), body.toByteArray(),
        )
        val e = androidx.media3.common.PlaybackException("refused", http, androidx.media3.common.PlaybackException.ERROR_CODE_IO_BAD_HTTP_STATUS)
        assertEquals(TimeReason.NO_HEARTBEAT, com.hamzalabs.maktaba.tv.playback.tvPlaybackTimeRefusal(e)!!.reason)
        val other = androidx.media3.common.PlaybackException("x", java.io.IOException("y"), androidx.media3.common.PlaybackException.ERROR_CODE_IO_UNSPECIFIED)
        assertNull(com.hamzalabs.maktaba.tv.playback.tvPlaybackTimeRefusal(other))
    }

    @Test
    fun checkAgainAsksForTheSameVideoWhereItStopped() {
        open = { timeOver(TimeReason.LIMIT_REACHED, next = null) }
        val pb = playback(startMs = 12_000)
        pb.start()
        runUntil { pb.timeUp != null }
        assertNull(pb.timeUp!!.refusal.nextAllowedAt)
        // a parent gave more time; the person presses Check again
        open = { opened() }
        pb.checkAgain()
        runUntil { pb.player != null }
        assertNull(pb.timeUp)
        assertEquals(2, opens())
    }

    @Test
    fun aMissingSessionOnAProgressPostIsTheStreamsEnd() {
        val pb = playback()
        pb.start()
        runUntil { pb.player != null }
        progress = { MockResponse.Builder().code(404).setHeader("Content-Type", "application/problem+json").body("""{"type":"https://maktaba.tv/problems/not-found","title":"Not found","status":404}""").build() }
        pb.player!!.seekTo(30_000)
        pb.player!!.playWhenReady = false
        runUntil { pb.endedByHome }
        assertNull(pb.player)
        assertNull(pb.timeUp)
        idleFor(30_000)
        assertEquals(1, opens())
    }

    @Test
    fun aHomeThatCannotCheckTheTimeDoesNotStopTheVideo() {
        val pb = playback()
        pb.start()
        runUntil { pb.player != null }
        progress = { MockResponse.Builder().code(503).setHeader("Retry-After", "5").body("""{"status":503}""").build() }
        pb.player!!.seekTo(30_000)
        pb.player!!.playWhenReady = false
        runUntil { pb.cannotCheck }
        assertNotNull("the video is still there", pb.player)
        assertNull(pb.timeUp)
        progress = { MockResponse.Builder().code(200).body("{}").build() }
        pb.player!!.playWhenReady = true
        runUntil { !pb.cannotCheck }
    }

    @Test
    fun a503OnOpenIsShownWithTryAgainAndIsNotAskedAgain() {
        open = { MockResponse.Builder().code(503).setHeader("Retry-After", "5").setHeader("Content-Type", "application/problem+json").body("""{"status":503}""").build() }
        val pb = playback()
        pb.start()
        runUntil { pb.problem != null }
        assertEquals(ErrorKind.Busy, pb.problem!!.kind)
        assertEquals(5L, pb.problem!!.retryAfterSec)
        assertTrue("an older home's bare 503 to a restricted account: can't check the time, as before", pb.problem!!.cannotCheckTime(restricted = true))
        idleFor(30_000)
        assertEquals(1, opens())
    }

    @Test
    fun aRateLimitOnOpenIsNotCannotCheckTheTimeAndTheHomesOwnCodeIs() {
        open = { MockResponse.Builder().code(429).setHeader("Retry-After", "30").setHeader("Content-Type", "application/problem+json").body("""{"type":"https://maktaba.tv/problems/rate-limited","title":"Too many","status":429,"code":"rate_limited"}""").build() }
        val limited = playback()
        limited.start()
        runUntil { limited.problem != null }
        assertEquals(ErrorKind.Busy, limited.problem!!.kind)
        assertFalse("a rate limit keeps its own words, for a restricted account too", limited.problem!!.cannotCheckTime(restricted = true))
        limited.stop()

        open = { MockResponse.Builder().code(503).setHeader("Retry-After", "5").setHeader("Content-Type", "application/problem+json").body("""{"type":"https://maktaba.tv/problems/unavailable","title":"service unavailable","status":503,"code":"parental_time_unknown"}""").build() }
        val unknown = playback()
        unknown.start()
        runUntil { unknown.problem != null }
        assertTrue(unknown.problem!!.cannotCheckTime(restricted = true))
    }

    @Test
    fun anOpenThatAnswersAfterTheTimeRanOutPlaysNothingUnderTheTimesUpScreen() {
        val pb = playback()
        pb.start()
        runUntil { pb.player != null }
        // the lease lapsed: the stream is opened again, and the home is slow to answer
        val gate = java.util.concurrent.CountDownLatch(1)
        val asked = java.util.concurrent.CountDownLatch(1)
        open = {
            asked.countDown()
            gate.await(30, java.util.concurrent.TimeUnit.SECONDS)
            MockResponse.Builder().code(201).setHeader("Content-Type", "application/json")
                .body("""{"session_id":"s2","mode":"direct","direct_url":"/stream/s2/v1.mp4?sig=x","expires_at":"2099-01-01T00:00:00Z"}""").build()
        }
        progress = { timeOver(TimeReason.NO_HEARTBEAT, next = null) }
        pb.player!!.seekTo(30_000)
        pb.player!!.playWhenReady = false
        runUntil { asked.count == 0L }
        // meanwhile another progress post says the time is over
        progress = { timeOver(TimeReason.LIMIT_REACHED) }
        pb.player!!.playWhenReady = true
        runUntil { pb.timeUp != null }
        assertEquals(TimeReason.LIMIT_REACHED, pb.timeUp!!.refusal.reason)
        // the open succeeds after all: nothing starts playing behind the screen
        gate.countDown()
        runUntil { "DELETE /api/stream/sessions/s2" in seen }
        idleFor(1_000)
        assertNull("no player under the time's-up screen", pb.player)
        assertNotNull(pb.timeUp)
        assertNull(pb.problem)
    }

    @Test
    fun pausingAndResumingEachPostTheProgressAtOnce() {
        val pb = playback()
        pb.start()
        runUntil { pb.player != null }
        pb.player!!.seekTo(30_000)
        pb.player!!.playWhenReady = false
        runUntil { posts() == 1 }
        pb.player!!.playWhenReady = true
        runUntil { posts() == 2 }
        assertTrue(seen.toString(), seen.contains("POST /api/stream/sessions/s1/progress"))
    }
}
