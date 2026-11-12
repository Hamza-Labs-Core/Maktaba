@file:OptIn(UnstableApi::class)

package com.hamzalabs.maktaba.player.watch

import android.content.Context
import android.net.Uri
import androidx.annotation.OptIn
import androidx.media3.common.PlaybackException
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.DataSpec
import androidx.media3.datasource.HttpDataSource
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.test.utils.robolectric.ShadowMediaCodecConfig
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.core.api.ErrorKind
import com.hamzalabs.maktaba.core.api.TimeReason
import com.hamzalabs.maktaba.core.api.Video
import com.hamzalabs.maktaba.player.Playback
import com.hamzalabs.maktaba.player.harness.FakeHome
import com.hamzalabs.maktaba.player.playbackTimeRefusal
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import mockwebserver3.MockResponse
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import java.time.Instant

/**
 * Epic 57 story 57.15 (T03, the phone root): the player when the home refuses for time. On a
 * session open, a renewal, a progress post or the stream itself answering 403 `parental-time`
 * the video stops, the refusal is kept for the time's-up screen, and nothing asks the home again
 * by itself; reason `no_heartbeat` (the lease lapsed in a long pause) reopens the stream once
 * first; a 404 on a progress post is the stream's end; a 503 is "cannot check right now".
 */
@RunWith(AndroidJUnit4::class)
class ParentalPlaybackTest {
    @get:Rule val home = FakeHome()
    @get:Rule val codecs: ShadowMediaCodecConfig = ShadowMediaCodecConfig.withAllDefaultSupportedCodecs()

    private val context: Context = ApplicationProvider.getApplicationContext()
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    private val background = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private lateinit var streams: FakeStreams
    private lateinit var playback: Playback

    private val v1 = Video("v1", title = "The Message", durationSec = 4.0)

    @Before
    fun setUp() {
        streams = FakeStreams(home)
        streams.install()
        playback = Playback(context, home.home.client, scope, background, signedOut = {}, build = testPlayer(context))
    }

    @After
    fun tearDown() {
        playback.stop()
        scope.cancel()
        background.cancel()
    }

    private fun timeOver(reason: String, next: String? = "2026-11-12T04:00:00Z") = MockResponse.Builder().code(403)
        .setHeader("Content-Type", "application/problem+json")
        .body(
            """{"type":"https://maktaba.tv/problems/parental-time","title":"this account's watching time is over for now","status":403,"code":"parental_time","reason":"$reason"""" +
                (if (next != null) ""","next_allowed_at":"$next"""" else "") + "}",
        ).build()

    /**
     * Lets the answers still on their way land: half a second of the looper's work, a task at a
     * time (idling it whole never returns while a released player's test clock keeps posting).
     */
    private fun settle() {
        val end = System.currentTimeMillis() + 500
        runUntil { System.currentTimeMillis() > end }
    }

    private fun playing(): ExoPlayer {
        runUntil { playback.player?.playbackState == Player.STATE_READY && playback.tracks.groups.isNotEmpty() }
        return playback.player!!
    }

    @Test
    fun anOpenRefusedForTimeShowsWhyAndIsNotAskedAgain() {
        home.answer("/api/stream/sessions") { timeOver(TimeReason.OUTSIDE_SLOT, "2026-11-12T07:00:00Z") }
        playback.play(v1, startSec = 3)
        runUntil { playback.timeUp != null }
        val up = playback.timeUp!!
        assertEquals(TimeReason.OUTSIDE_SLOT, up.refusal.reason)
        assertEquals(Instant.parse("2026-11-12T07:00:00Z"), up.refusal.nextAllowedAt)
        assertEquals("where it would have started", 3_000L, up.positionMs)
        assertNull("no error screen: this is the time's-up screen", playback.problem)
        assertNull(playback.waiting)
        assertNull(playback.player)

        // a minute passes: the home is not asked again by itself
        idleFor(60_000)
        assertEquals(1, streams.opens().size)
        assertNotNull(playback.timeUp)
    }

    @Test
    fun aProgressPostRefusedForTimeStopsTheVideoWhereItIs() {
        playback.play(v1, startSec = 1)
        val p = playing()
        runUntil { p.currentPosition > 1_200 }
        home.answer("/api/stream/sessions/s1/progress") { timeOver(TimeReason.LIMIT_REACHED) }
        // a pause posts the position at once; the home answers that the time is over
        p.pause()
        runUntil { playback.timeUp != null }
        val up = playback.timeUp!!
        assertEquals(TimeReason.LIMIT_REACHED, up.refusal.reason)
        assertEquals(Instant.parse("2026-11-12T04:00:00Z"), up.refusal.nextAllowedAt)
        assertNull("playback stopped", playback.player)
        assertNull(playback.control)
        assertFalse(playback.isPlaying)
        runUntil { "s1" in streams.closed() }

        val posts = streams.progress().size
        settle()
        assertEquals("nothing is opened again", 1, streams.opens().size)
        assertEquals("and nothing more is posted", posts, streams.progress().size)
    }

    @Test
    fun aLapsedLeaseOnAProgressPostReopensTheStreamOnceWhereItIs() {
        playback.play(v1, startSec = 1)
        val p = playing()
        runUntil { p.currentPosition > 1_200 }
        // a long pause: the stream's lease lapsed. That is not the time being over
        home.answer("/api/stream/sessions/s1/progress") { timeOver(TimeReason.NO_HEARTBEAT, next = null) }
        p.pause()
        runUntil { streams.opens().size == 2 && "s1" in streams.closed() }
        settle()
        assertNull("no time's-up screen: the home gave the stream again", playback.timeUp)
        assertNull(playback.problem)
        assertNotNull(playback.player)
        assertTrue("it opens where it was", streams.opens()[1].int("start_sec")!! >= 1)
        assertFalse("it was paused, and still is", playback.control!!.playWhenReady)
        assertEquals("once", 2, streams.opens().size)
    }

    @Test
    fun aLapsedLeaseWhoseReopenIsRefusedShowsTimesUp() {
        playback.play(v1, startSec = 1)
        val p = playing()
        runUntil { p.currentPosition > 1_200 }
        home.answer("/api/stream/sessions/s1/progress") { timeOver(TimeReason.NO_HEARTBEAT, next = null) }
        // the time ran out meanwhile: the home refuses the reopen
        home.answer("/api/stream/sessions") { timeOver(TimeReason.LIMIT_REACHED) }
        p.pause()
        runUntil { playback.timeUp != null }
        assertEquals(TimeReason.LIMIT_REACHED, playback.timeUp!!.refusal.reason)
        assertEquals(Instant.parse("2026-11-12T04:00:00Z"), playback.timeUp!!.refusal.nextAllowedAt)
        assertNull(playback.player)
        settle()
        assertEquals("the first open and the one reopen", 2, streams.opens().size)
    }

    @Test
    fun aLeaseThatLapsesAgainRightAfterItsReopenStops() {
        playback.play(v1, startSec = 1)
        val p = playing()
        runUntil { p.currentPosition > 1_200 }
        home.answer("/api/stream/sessions/s1/progress") { timeOver(TimeReason.NO_HEARTBEAT, next = null) }
        home.answer("/api/stream/sessions/s2/progress") { timeOver(TimeReason.NO_HEARTBEAT, next = null) }
        p.pause()
        runUntil { streams.opens().size == 2 && playback.control?.playbackState == Player.STATE_READY }
        // the reopened stream is refused the same way at once: no loop of opens
        playback.control!!.play()
        runUntil { playback.timeUp != null }
        assertEquals(TimeReason.NO_HEARTBEAT, playback.timeUp!!.refusal.reason)
        assertNull(playback.timeUp!!.refusal.nextAllowedAt)
        settle()
        assertEquals(2, streams.opens().size)
    }

    /** The player's failure when the streaming service answers a segment with 403 `parental-time`. */
    private fun streamRefused(reason: String): PlaybackException {
        val body = """{"type":"https://maktaba.tv/problems/parental-time","status":403,"code":"parental_time","reason":"$reason"}"""
        val http = HttpDataSource.InvalidResponseCodeException(
            403, null, null, emptyMap(), DataSpec(Uri.parse("http://home.test/stream/s1/seg0.ts")), body.toByteArray(),
        )
        return PlaybackException("refused", http, PlaybackException.ERROR_CODE_IO_BAD_HTTP_STATUS)
    }

    @Test
    fun theStreamItselfRefusedForTimeIsRead() {
        assertEquals(TimeReason.LIMIT_REACHED, playbackTimeRefusal(streamRefused(TimeReason.LIMIT_REACHED))!!.reason)
        assertEquals(TimeReason.NO_HEARTBEAT, playbackTimeRefusal(streamRefused(TimeReason.NO_HEARTBEAT))!!.reason)
        assertNull(playbackTimeRefusal(streamRefused(TimeReason.NO_HEARTBEAT))!!.nextAllowedAt)
        // any other failure of the player is not a time refusal
        assertNull(playbackTimeRefusal(PlaybackException("x", java.io.IOException("y"), PlaybackException.ERROR_CODE_IO_UNSPECIFIED)))
    }

    @Test
    fun aStreamTheServiceRefusesForTimeStopsWithTimesUp() {
        playback.play(v1, startSec = 1)
        val p = playing()
        // what the ten-second watch does with the player's failure: the account's time is over
        playback.refusedForTime(playbackTimeRefusal(streamRefused(TimeReason.LIMIT_REACHED))!!, p.currentPosition)
        assertEquals(TimeReason.LIMIT_REACHED, playback.timeUp!!.refusal.reason)
        assertNull("the time's-up screen, not the error screen", playback.problem)
        assertNull(playback.player)
        settle()
        assertEquals(1, streams.opens().size)
    }

    @Test
    fun aPausedStreamWhoseLeaseLapsedIsOpenedAgainWhenTheServiceRefusesIt() {
        playback.play(v1, startSec = 1)
        val p = playing()
        runUntil { p.currentPosition > 1_200 }
        p.pause()
        runUntil { streams.progress().isNotEmpty() }
        // paused for over a minute, then scrubbed: the segment answers 403 no_heartbeat
        playback.refusedForTime(playbackTimeRefusal(streamRefused(TimeReason.NO_HEARTBEAT))!!, p.currentPosition)
        runUntil { streams.opens().size == 2 && "s1" in streams.closed() }
        settle()
        assertNull("not the time's-up screen", playback.timeUp)
        assertNull(playback.problem)
        assertNotNull(playback.player)
        assertFalse("still paused", playback.control!!.playWhenReady)
        assertEquals("opened again once", 2, streams.opens().size)
    }

    @Test
    fun checkAgainOpensTheSameVideoWhereItStopped() {
        home.answer("/api/stream/sessions") { timeOver(TimeReason.LIMIT_REACHED) }
        playback.play(v1, startSec = 2)
        runUntil { playback.timeUp != null }
        // a parent gave more time: the person taps Check again
        streams.install()
        playback.checkAgain()
        playing()
        assertNull(playback.timeUp)
        assertEquals(listOf(2, 2), streams.opens().map { it.int("start_sec") })
    }

    @Test
    fun aMissingSessionOnAProgressPostIsTheStreamsEnd() {
        playback.play(v1, startSec = 1)
        val p = playing()
        runUntil { p.currentPosition > 1_200 }
        home.answer("/api/stream/sessions/s1/progress") {
            FakeHome.json("""{"type":"https://maktaba.tv/problems/not-found","title":"Not found","status":404}""", code = 404)
        }
        p.pause()
        runUntil { playback.endedByHome }
        assertNull(playback.player)
        assertNull("not the time's-up screen", playback.timeUp)
        settle()
        assertEquals("not opened again", 1, streams.opens().size)
    }

    @Test
    fun aHomeThatCannotCheckTheTimeDoesNotStopTheVideo() {
        playback.play(v1, startSec = 1)
        val p = playing()
        runUntil { p.currentPosition > 1_200 }
        home.answer("/api/stream/sessions/s1/progress") {
            MockResponse.Builder().code(503).setHeader("Retry-After", "5").setHeader("Content-Type", "application/problem+json")
                .body("""{"type":"https://maktaba.tv/problems/unavailable","title":"Unavailable","status":503}""").build()
        }
        p.pause()
        runUntil { playback.cannotCheck }
        assertNotNull("the video is still there", playback.player)
        assertNull(playback.timeUp)
        assertFalse(playback.endedByHome)
        // the next post the home takes clears it
        home.answer("/api/stream/sessions/s1/progress") { MockResponse.Builder().code(204).build() }
        p.play()
        runUntil { !playback.cannotCheck }
    }

    @Test
    fun a503OnOpenIsShownAsBusyWithItsRetryAfterAndIsNotAskedAgain() {
        home.answer("/api/stream/sessions") {
            MockResponse.Builder().code(503).setHeader("Retry-After", "5").setHeader("Content-Type", "application/problem+json")
                .body("""{"type":"https://maktaba.tv/problems/unavailable","title":"Unavailable","status":503}""").build()
        }
        playback.play(v1)
        runUntil { playback.problem != null }
        assertEquals(ErrorKind.Busy, playback.problem!!.kind)
        assertEquals(5L, playback.problem!!.retryAfterSec)
        assertTrue("an older home's bare 503 to a restricted account: can't check the time, as before", playback.problem!!.cannotCheckTime(restricted = true))
        idleFor(30_000)
        assertEquals("only the person's Try again asks", 1, streams.opens().size)
    }

    @Test
    fun aRateLimitOnOpenIsNotCannotCheckTheTime() {
        home.answer("/api/stream/sessions") {
            MockResponse.Builder().code(429).setHeader("Retry-After", "30").setHeader("Content-Type", "application/problem+json")
                .body("""{"type":"https://maktaba.tv/problems/rate-limited","title":"Too many","status":429,"code":"rate_limited"}""").build()
        }
        playback.play(v1)
        runUntil { playback.problem != null }
        assertEquals(ErrorKind.Busy, playback.problem!!.kind)
        assertFalse("a rate limit keeps its own words, for a restricted account too", playback.problem!!.cannotCheckTime(restricted = true))
    }

    @Test
    fun theHomesOwnCodeOnOpenIsCannotCheckTheTime() {
        home.answer("/api/stream/sessions") {
            MockResponse.Builder().code(503).setHeader("Retry-After", "5").setHeader("Content-Type", "application/problem+json")
                .body("""{"type":"https://maktaba.tv/problems/unavailable","title":"service unavailable","status":503,"code":"parental_time_unknown"}""").build()
        }
        playback.play(v1)
        runUntil { playback.problem != null }
        assertTrue(playback.problem!!.cannotCheckTime(restricted = true))
        assertEquals(5L, playback.problem!!.retryAfterSec)
    }

    @Test
    fun anOpenThatAnswersAfterTheTimeRanOutPlaysNothingUnderTheTimesUpScreen() {
        playback.play(v1, startSec = 1)
        val p = playing()
        runUntil { p.currentPosition > 1_200 }
        // a quality change reopens the stream; the home is slow to answer it
        val slow = home.hold("/api/stream/sessions") {
            FakeHome.json("""{"session_id":"s2","mode":"transcode","manifest_url":"/stream/s2/master.m3u8?sig=x","expires_at":"2099-01-01T00:00:00Z","ladder":[]}""", code = 201)
        }
        playback.setQuality(1_500)
        runUntil { slow.wasAsked }
        // meanwhile a progress post says the time is over
        home.answer("/api/stream/sessions/s1/progress") { timeOver(TimeReason.LIMIT_REACHED) }
        p.pause()
        runUntil { playback.timeUp != null }
        assertNull(playback.player)
        // the open succeeds after all (a grant just landed): nothing starts playing behind the screen
        slow.release()
        runUntil { "s2" in streams.closed() }
        settle()
        assertNull("no player under the time's-up screen", playback.player)
        assertNull(playback.control)
        assertFalse(playback.isPlaying)
        assertNotNull(playback.timeUp)
        assertNull(playback.problem)
    }

    @Test
    fun resumingPostsTheProgressAtOnce() {
        playback.play(v1, startSec = 1)
        val p = playing()
        runUntil { p.currentPosition > 1_200 }
        p.pause()
        runUntil { streams.progress().isNotEmpty() }
        val paused = streams.progress().size
        // resumed: the home hears of it at once, not at the next ten-second tick
        p.play()
        runUntil { streams.progress().size > paused }
        assertEquals("s1", streams.progress().last().first)
    }

    @Test
    fun aLateAnswerForAClosedSessionChangesNothing() {
        playback.play(v1, startSec = 1)
        val p = playing()
        runUntil { p.currentPosition > 1_200 }
        home.answer("/api/stream/sessions/s1/progress") { timeOver(TimeReason.LIMIT_REACHED) }
        // leaving posts the last position; its refusal arrives after the video was stopped
        playback.stop()
        runUntil { streams.progress().isNotEmpty() && "s1" in streams.closed() }
        settle()
        assertNull(playback.timeUp)
        assertNull(playback.video)
    }
}
