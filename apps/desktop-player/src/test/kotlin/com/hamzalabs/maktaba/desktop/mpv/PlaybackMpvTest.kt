package com.hamzalabs.maktaba.desktop.mpv

import com.hamzalabs.maktaba.desktop.AudioOption
import com.hamzalabs.maktaba.desktop.PlayRequest
import com.hamzalabs.maktaba.desktop.Playback
import com.hamzalabs.maktaba.desktop.Players
import com.hamzalabs.maktaba.desktop.TestHome
import com.hamzalabs.maktaba.desktop.Video
import com.hamzalabs.maktaba.desktop.VideoEvents
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.asCoroutineDispatcher
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withContext
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.util.Collections
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicInteger

/**
 * Story 13.18 with a fake libmpv: the profile asked for follows what the home knows (T04), a
 * direct file that does not play opens once more as a transcode (T05), and a direct file's
 * audio track is switched in the player.
 */
class PlaybackMpvTest {
    private val home = TestHome()
    private val thread = Executors.newSingleThreadExecutor { r -> Thread(r, "test-main").apply { isDaemon = true } }
    private val main = thread.asCoroutineDispatcher()
    private val scope = CoroutineScope(SupervisorJob() + main)
    private val background = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val opened = AtomicInteger()

    private class FakeMpv(val events: VideoEvents) : Video {
        val selected: MutableList<Int> = Collections.synchronizedList(mutableListOf())
        @Volatile override var started = false
        override fun toggle() {}
        override fun pause() {}
        override fun seek(ms: Long) {}
        override fun sound(volume: Float, muted: Boolean) {}
        override fun dispose() {}
        override fun selectAudio(index: Int): Boolean {
            selected += index
            return true
        }
    }

    private val made: MutableList<FakeMpv> = Collections.synchronizedList(mutableListOf())

    /** The probe's report (story 13.19): HEVC 10-bit in hardware, AV1 in software up to 1080p, 5.1 speakers. */
    private val report = DeviceReport().apply {
        probe = ProbeResult(
            "k",
            listOf(
                ClipResult("hevc", 10, decoded = true, hwdec = "vaapi-copy"),
                ClipResult("av1", 10, decoded = true, hwdec = "no", softwareFps = 20.0),
            ),
            900,
        )
        channels = 6
    }

    private val players = object : Players {
        override val profile = MPV_PROFILE
        override fun deviceCaps() = report.json()
        override suspend fun open(url: String, fromMs: Long, autoplay: Boolean, events: VideoEvents): Video =
            FakeMpv(events).also { made += it }
    }

    private val playback = Playback(home.client, scope, background, signedOut = {}, players = players)

    init {
        home.answer("POST /api/stream/sessions") {
            val n = opened.incrementAndGet()
            TestHome.json("""{"session_id":"s$n","mode":"direct","direct_url":"/stream/s$n.mkv"}""")
        }
    }

    @After
    fun tearDown() {
        runBlocking(main) { playback.close() }
        home.close()
        thread.shutdown()
    }

    private fun <T> onMain(block: () -> T): T = runBlocking { withContext(main) { block() } }

    private fun until(what: String, ok: () -> Boolean) {
        val end = System.currentTimeMillis() + 10_000
        while (!onMain(ok)) {
            if (System.currentTimeMillis() > end) throw AssertionError("never: $what")
            Thread.sleep(20)
        }
    }

    private fun bodies() = home.asked("POST /api/stream/sessions").map { it.body!!.utf8() }

    @Test
    fun aHomeThatKnowsDesktopMpvIsAskedForIt() {
        home.answer("GET /api/stream/capabilities", """{"client_profiles":["desktop-mpv","desktop-player","generic"]}""")
        onMain { playback.play(PlayRequest("v1", "v1", startSec = 0)) }
        until("playing") { playback.player != null }
        assertTrue(bodies().single(), bodies().single().contains("\"client_profile\":\"desktop-mpv\""))
    }

    @Test
    fun anOlderHomeIsAskedForDesktopPlayer() {
        // a home from before story 13.18: its capabilities list no client profiles
        home.answer("GET /api/stream/capabilities", """{"codecs":["h264"],"hwaccel":"none","max_bitrate_kbps":40000,"supported_containers":["mp4"]}""")
        onMain { playback.play(PlayRequest("v1", "v1", startSec = 0)) }
        until("playing") { playback.player != null }
        assertTrue(bodies().single(), bodies().single().contains("\"client_profile\":\"desktop-player\""))
    }

    @Test
    fun aDirectFileThatFailsOpensOnceAsATranscode() {
        home.answer("GET /api/stream/capabilities", """{"client_profiles":["desktop-mpv"]}""")
        onMain { playback.play(PlayRequest("v1", "v1", startSec = 0)) }
        until("playing") { playback.player != null }
        made.single().events.failed("no decoder for this stream")
        until("reopened") { opened.get() == 2 && (playback.player as? FakeMpv) === made.getOrNull(1) }
        val second = bodies()[1]
        assertTrue(second, second.contains("\"force_transcode\":true"))
        // a second failure is not reopened again at once: it is the watch loop's to report
        made[1].events.failed("again")
        Thread.sleep(300)
        assertEquals(2, opened.get())
    }

    /** Story 13.19 T06: the report goes with desktop-mpv, not on the forced transcode's reopen. */
    @Test
    fun t06_theReportGoesWithDesktopMpvButNotOnAForcedTranscode() {
        home.answer("GET /api/stream/capabilities", """{"client_profiles":["desktop-mpv"]}""")
        onMain { playback.play(PlayRequest("v1", "v1", startSec = 0)) }
        until("playing") { playback.player != null }
        val first = bodies().single()
        assertTrue(first, first.contains(""""device_caps":{"video":[{"codec":"hevc","max_height":2160,"bit_depth":10,"hardware":true},{"codec":"av1","max_height":1080,"bit_depth":10,"hardware":false}],"max_audio_channels":6}"""))
        made.single().events.failed("no decoder for this stream")
        until("reopened") { opened.get() == 2 }
        val second = bodies()[1]
        assertTrue(second, second.contains("\"force_transcode\":true"))
        assertFalse(second, second.contains("device_caps"))
    }

    /** T06: an older home (desktop-player) is sent no report, nor is JavaFX's engine. */
    @Test
    fun t06_noReportWithDesktopPlayer() {
        home.answer("GET /api/stream/capabilities", """{"codecs":["h264"]}""")
        onMain { playback.play(PlayRequest("v1", "v1", startSec = 0)) }
        until("playing") { playback.player != null }
        assertTrue(bodies().single(), bodies().single().contains("\"client_profile\":\"desktop-player\""))
        assertFalse(bodies().single(), bodies().single().contains("device_caps"))
        // JavaFX's players have no report
        assertEquals(null, com.hamzalabs.maktaba.desktop.FxPlayers.deviceCaps())
    }

    /** T06: before the probe has run, a video opens with the profile alone. */
    @Test
    fun beforeTheProbeAVideoOpensWithTheProfileAlone() {
        report.probe = null
        home.answer("GET /api/stream/capabilities", """{"client_profiles":["desktop-mpv"]}""")
        onMain { playback.play(PlayRequest("v1", "v1", startSec = 0)) }
        until("playing") { playback.player != null }
        assertTrue(bodies().single(), bodies().single().contains("\"client_profile\":\"desktop-mpv\""))
        assertFalse(bodies().single(), bodies().single().contains("device_caps"))
    }

    /**
     * Story 13.19 T07: a home whose session body reader refuses device_caps: the stream opens
     * without it on the same open, and the Player sends this home no report again.
     */
    @Test
    fun t07_aHomeThatRefusesTheReportIsAskedAgainWithoutIt() {
        home.answer("GET /api/stream/capabilities", """{"client_profiles":["desktop-mpv"]}""")
        home.answer("POST /api/stream/sessions") { r ->
            if (r.body!!.utf8().contains("device_caps")) {
                TestHome.json("""{"type":"https://maktaba.dev/problems/invalid-json","title":"invalid json","status":400,"detail":"json: unknown field \"device_caps\""}""", 400)
            } else {
                val n = opened.incrementAndGet()
                TestHome.json("""{"session_id":"s$n","mode":"direct","direct_url":"/stream/s$n.mkv"}""")
            }
        }
        onMain { playback.play(PlayRequest("v1", "v1", startSec = 0)) }
        until("playing") { playback.player != null }
        assertEquals(null, playback.problem)
        assertEquals(2, bodies().size)
        assertTrue(bodies()[0].contains("device_caps"))
        assertFalse(bodies()[1].contains("device_caps"))
        // the next video: no report sent to this home
        onMain { playback.play(PlayRequest("v2", "v2", startSec = 0)) }
        until("the second plays") { opened.get() == 2 && made.size == 2 }
        assertEquals(3, bodies().size)
        assertFalse(bodies()[2], bodies()[2].contains("device_caps"))
    }

    @Test
    fun aFailureAfterThePictureStartedIsNotTheFiles() {
        home.answer("GET /api/stream/capabilities", """{"client_profiles":["desktop-mpv"]}""")
        onMain { playback.play(PlayRequest("v1", "v1", startSec = 0)) }
        until("playing") { playback.player != null }
        made.single().started = true
        made.single().events.failed("the relay dropped")
        Thread.sleep(300)
        assertEquals("no transcode for a file that played", 1, opened.get())
    }

    @Test
    fun aDirectFilesAudioTrackIsSwitchedInThePlayer() {
        home.answer("GET /api/stream/capabilities", """{"client_profiles":["desktop-mpv"]}""")
        onMain { playback.play(PlayRequest("v1", "v1", startSec = 0)) }
        until("playing") { playback.player != null }
        val tracks = listOf(AudioOption("English", "en", index = 0), AudioOption("العربية", "ar", index = 1))
        made.single().events.ready(60_000, tracks)
        until("tracks offered") { playback.audioOptions.size == 2 }
        onMain { playback.chooseAudio(tracks[1]) }
        until("switched") { playback.audio == tracks[1] }
        assertEquals(listOf(1), made.single().selected)
        assertEquals("no new session for a track", 1, opened.get())
        // the next player of this video (a renewal, a quality change) plays the same track
        assertEquals(1, made.single().events.audioIndex)
    }
}
