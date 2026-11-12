@file:OptIn(UnstableApi::class)

package com.hamzalabs.maktaba.player.watch

import android.content.Context
import androidx.annotation.OptIn
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.test.utils.robolectric.ShadowMediaCodecConfig
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.core.api.ErrorKind
import com.hamzalabs.maktaba.core.api.TimeReason
import com.hamzalabs.maktaba.core.api.Video
import com.hamzalabs.maktaba.player.Playback
import com.hamzalabs.maktaba.player.harness.FakeHome
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
 * session open, a renewal or a progress post that answers 403 `parental-time` the video stops,
 * the refusal is kept for the time's-up screen, and nothing asks the home again by itself; a
 * 404 on a progress post is the stream's end; a 503 is "cannot check right now".
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
        // and the same video asked for again by the screen's own recomposition is not a retry
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
    fun aRefusalWithNoNextTimeStillStops() {
        playback.play(v1, startSec = 1)
        val p = playing()
        runUntil { p.currentPosition > 1_200 }
        home.answer("/api/stream/sessions/s1/progress") { timeOver(TimeReason.NO_HEARTBEAT, next = null) }
        p.pause()
        runUntil { playback.timeUp != null }
        assertEquals(TimeReason.NO_HEARTBEAT, playback.timeUp!!.refusal.reason)
        assertNull(playback.timeUp!!.refusal.nextAllowedAt)
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
        idleFor(30_000)
        assertEquals("only the person's Try again asks", 1, streams.opens().size)
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
