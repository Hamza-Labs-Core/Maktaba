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
import com.hamzalabs.maktaba.core.api.Video
import com.hamzalabs.maktaba.player.PHONE_PROFILE
import com.hamzalabs.maktaba.player.Playback
import com.hamzalabs.maktaba.player.harness.FakeHome
import com.hamzalabs.maktaba.player.harness.NoStall
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.boolean
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonPrimitive
import mockwebserver3.MockResponse
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

/**
 * How soon a video starts and how long it says it is.
 *
 * - The Player opens its streams with its own profile and what the device plays (`android-native`
 *   and `device_caps`): without them the home used its `generic` profile (H.264 and AAC only, in
 *   MP4, 720p and 4 Mbit/s at most) and transcoded nearly every file, so the first frame waited
 *   for a transcode's first 6-second segment of every rung.
 * - A file played as it is starts without any playlist to wait for, within [FIRST_FRAME_BUDGET_MS].
 * - A transcode's playlist grows while it is written (EVENT, no `#EXT-X-ENDLIST`): the player's
 *   duration is only what is written so far, so the length shown is the catalogue's from the
 *   start, and a seek past what is written opens the stream there.
 * - A file that does not play as it is after all is opened again once, as a transcode.
 */
@RunWith(AndroidJUnit4::class)
class StartupAndLengthTest {
    @get:Rule(order = NoStall.FIRST) val noStall = NoStall()
    @get:Rule val home = FakeHome()
    @get:Rule val codecs: ShadowMediaCodecConfig = ShadowMediaCodecConfig.withAllDefaultSupportedCodecs()

    private val context: Context = ApplicationProvider.getApplicationContext()
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    private val background = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private lateinit var streams: FakeStreams
    private lateinit var playback: Playback

    @Before
    fun setUp() {
        streams = FakeStreams(home)
        streams.install()
        playback = Playback(
            context, home.home.client, scope, background, signedOut = {},
            build = testPlayer(context), nowMs = looperTime,
        )
    }

    @After
    fun tearDown() {
        playback.stop()
        scope.cancel()
        background.cancel()
    }

    /** A ten-minute film; a growing playlist lists a few seconds of it so far. */
    private val film = Video("v1", title = "The Message", durationSec = 600.0)

    private fun playing(): ExoPlayer {
        runUntil(what = "the player ready") { playback.player?.playbackState == Player.STATE_READY }
        return playback.player!!
    }

    /**
     * The growing playlist read: the player's window known (dynamic) with what is written so far.
     * (The JVM's stand-in renderers never call a live window ready, so these wait for the timeline.)
     */
    private fun written(): ExoPlayer {
        runUntil(what = "the growing playlist read") {
            val p = playback.player
            p != null && p.isCurrentMediaItemDynamic && p.duration != androidx.media3.common.C.TIME_UNSET && p.duration > 0
        }
        return playback.player!!
    }

    @Test
    fun opensWithThePhonesProfileAndWhatTheDevicePlays() {
        playback.play(film)
        playing()
        val open = streams.opens().single()
        assertEquals(PHONE_PROFILE, open["client_profile"]?.jsonPrimitive?.content)
        val caps = open["device_caps"] as JsonObject
        assertTrue("the device's video decoders are reported: $caps", caps["video"]!!.jsonArray.isNotEmpty())
        assertTrue("its audio decoders too: $caps", caps["audio"]!!.jsonArray.isNotEmpty())
        assertNull("a transcode is not asked for", open["force_transcode"])
    }

    @Test
    fun aFilePlayedAsItIsStartsWithinTheBudgetWithNoPlaylistToWaitFor() {
        streams.direct = true
        val start = System.nanoTime()
        playback.play(film)
        // playing: ready, its video decoded and its clock moving (the JVM has no surface to draw on)
        runUntil(what = "the video playing") {
            val p = playback.player
            p != null && p.isPlaying && p.currentPosition > 0 && (p.videoDecoderCounters?.queuedInputBufferCount ?: 0) > 0
        }
        val ms = (System.nanoTime() - start) / 1_000_000
        println("time to first frame, direct play against the fake home: $ms ms")
        assertTrue("the video played after $ms ms, over the $FIRST_FRAME_BUDGET_MS ms budget", ms <= FIRST_FRAME_BUDGET_MS)
        assertFalse("no playlist is asked for: ${home.paths}", home.paths.any { it.contains(".m3u8") })
        // the file's own length, from the start
        assertEquals(TestStreams.LENGTH_MS.toDouble(), playback.lengthMs(playback.player!!).toDouble(), 100.0)
    }

    @Test
    fun aTranscodeStillBeingWrittenShowsTheVideosWholeLengthFromTheStart() {
        streams.growing = true
        streams.durationSec = 600.0
        // the session's length counts, even where the video's is not known here
        playback.play(Video("v1", title = "The Message"))
        val p = written()
        assertTrue("the player's own duration is what is written so far: ${p.duration}", p.duration in 1..60_000)
        assertEquals(600_000L, playback.lengthMs(p))
        // as live, the player would speed up or slow down to keep a live offset: pinned to 1
        val live = p.currentMediaItem!!.liveConfiguration
        assertEquals(1f, live.minPlaybackSpeed)
        assertEquals(1f, live.maxPlaybackSpeed)
    }

    @Test
    fun aSeekWithinWhatIsWrittenStaysInTheStream() {
        streams.growing = true
        streams.durationSec = 600.0
        playback.play(film)
        val p = written()
        playback.seekTo(500)
        runUntil(what = "the seek") { p.currentPosition in 500..1_900 }
        runDue()
        assertEquals("no second open", 1, streams.opens().size)
        assertTrue(p === playback.player)
    }

    @Test
    fun aShortSkipPastWhatIsWrittenGoesAsFarAsItIs() {
        streams.growing = true
        streams.durationSec = 600.0
        playback.play(film)
        val p = written()
        val end = p.duration
        // +10 s past the written end: the transcode runs ahead; opening it again would wait anew
        playback.seekTo(end + 10_000)
        runDue()
        assertEquals("no second open", 1, streams.opens().size)
        assertTrue("at the written edge: ${p.currentPosition} of $end", p.currentPosition in (end - 2_000)..end)
    }

    @Test
    fun aSeekPastWhatIsWrittenOpensTheStreamThere() {
        streams.growing = true
        streams.durationSec = 600.0
        playback.play(film)
        written()
        playback.seekTo(300_000)
        runUntil(what = "the stream opened again") { streams.opens().size == 2 }
        assertEquals(300, streams.opens()[1].int("start_sec"))
        runUntil(what = "the new session to load") { playback.player?.currentMediaItem?.localConfiguration?.uri?.path?.startsWith("/stream/s2/") == true }
        runUntil(what = "the first session closed") { "s1" in streams.closed() }
        assertNull(playback.problem)
        // the length stays the video's
        assertEquals(600_000L, playback.lengthMs(playback.player!!))
    }

    @Test
    fun theVideoStoppedWhileASeekReopensPlaysNothing() {
        streams.growing = true
        streams.durationSec = 600.0
        // the new transcode's first playlist takes a while
        home.answer("/stream/s2/master.m3u8") {
            MockResponse.Builder().code(200).setHeader("Content-Type", "application/vnd.apple.mpegurl")
                .body(okio.Buffer().write(TestStreams.bytes("hls/master.m3u8"))).headersDelay(1, java.util.concurrent.TimeUnit.SECONDS).build()
        }
        playback.play(film)
        written()
        playback.seekTo(300_000)
        runUntil(what = "the reopen asked for") { streams.opens().size == 2 }
        // the person leaves while it waits
        playback.stop()
        runUntil(what = "the reopen's session closed") { "s2" in streams.closed() }
        runDue()
        assertNull("nothing plays after the stop", playback.player)
        assertNull(playback.control)
    }

    @Test
    fun aFileThatDoesNotPlayAsItIsOpensOnceAsATranscode() {
        streams.direct = true
        streams.directOpens = 1
        // the first session's file is not one this device reads; the second session's is
        home.answer("/stream/s1/two-audio.mp4") {
            MockResponse.Builder().code(200).setHeader("Content-Type", "video/mp4").body("not a video file at all").build()
        }
        playback.play(film)
        runUntil(what = "the stream opened again") { streams.opens().size == 2 }
        val again = streams.opens()[1]
        assertTrue("a transcode is asked for: $again", again["force_transcode"]!!.jsonPrimitive.boolean)
        assertNull("no device report with it", again["device_caps"])
        playing()
        assertNull(playback.problem)
        assertTrue("the transcode plays", playback.player!!.currentMediaItem!!.localConfiguration!!.uri.path!!.endsWith("/s2/master.m3u8"))
        runUntil(what = "the video playing") { playback.player!!.currentPosition > 500 }
        // past the 10-second watch: it does not take the first file's error for the stream's
        idleFor(10_000)
        assertNull(playback.problem)
        assertTrue(playback.player != null)
        assertEquals("only once", 2, streams.opens().size)
    }

    companion object {
        /**
         * Play to the first frame of a file played as it is, against the fake home on this machine
         * (a CI runner's JVM included): measured at about 0.3–0.6 s here, so this catches a wait
         * or a round trip added before the first frame, not the runner's speed.
         */
        const val FIRST_FRAME_BUDGET_MS = 5_000L
    }
}
