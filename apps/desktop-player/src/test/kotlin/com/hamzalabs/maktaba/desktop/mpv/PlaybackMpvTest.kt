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
        @Volatile var disposed = false
        override fun dispose() {
            disposed = true
        }
        override fun selectAudio(index: Int): Boolean {
            selected += index
            return true
        }
    }

    private val made: MutableList<FakeMpv> = Collections.synchronizedList(mutableListOf())

    private val players = object : Players {
        override val profile = MPV_PROFILE
        override suspend fun open(url: String, fromMs: Long, autoplay: Boolean, events: VideoEvents): Video =
            FakeMpv(events).also { made += it }
    }

    /** JavaFX's, as the older engine (story 13.21): the `desktop-player` profile. */
    private val fxMade: MutableList<FakeMpv> = Collections.synchronizedList(mutableListOf())
    private val fx = Players { _, _, _, events -> FakeMpv(events).also { fxMade += it } }

    /** The Player's own settings, where Profile keeps the engine (story 13.21). */
    private val prefs = com.hamzalabs.maktaba.desktop.shell.AppPrefs(com.hamzalabs.maktaba.core.MapStore())

    /** The Player's engines, as Main sets them up: libmpv loaded, the setting read at each video's open. */
    private val engines = Engines(
        emptyMap(),
        load = {
            val lib = java.lang.reflect.Proxy.newProxyInstance(LibMpv::class.java.classLoader, arrayOf(LibMpv::class.java)) { _, _, _ -> null } as LibMpv
            MpvLoad.Loaded(lib, java.io.File("libmpv.so.2"))
        },
        mpv = { players },
        fx = fx,
    ).apply { setting = { prefs.engine } }

    private val playback = Playback(home.client, scope, background, signedOut = {}, engine = { engines.players() })

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
    fun t02_theEngineChosenInProfileAppliesToTheNextVideo() {
        home.answer("GET /api/stream/capabilities", """{"client_profiles":["desktop-mpv","desktop-player"]}""")
        onMain { playback.play(PlayRequest("v1", "v1", startSec = 0)) }
        until("v1 playing") { playback.player != null && playback.player === made.singleOrNull() }
        val v1 = made.single()
        // the older engine chosen in Profile while v1 plays: v1's player and session are untouched
        onMain { prefs.chooseEngine(EngineChoice.Older) }
        Thread.sleep(300)
        assertTrue(onMain { playback.player } === v1)
        assertFalse(v1.disposed)
        assertEquals(1, opened.get())
        assertTrue("v1's session was not closed", home.asked("DELETE /api/stream/sessions/s1").isEmpty())
        // a reopen of v1 (a quality cap) keeps v1's engine: libmpv's players and desktop-mpv
        onMain { playback.chooseQuality(com.hamzalabs.maktaba.core.playback.QualityOption(720, 3000)) }
        until("v1 reopened") { made.size == 2 && playback.player === made[1] }
        assertTrue(bodies()[1], bodies()[1].contains("\"client_profile\":\"desktop-mpv\""))
        assertTrue("no JavaFX player for the video playing", fxMade.isEmpty())
        // the next video opens with JavaFX and asks for desktop-player
        onMain { playback.play(PlayRequest("v2", "v2", startSec = 0)) }
        until("v2 playing") { playback.player != null && playback.player === fxMade.singleOrNull() }
        assertTrue(bodies()[2], bodies()[2].contains("\"client_profile\":\"desktop-player\""))
        assertTrue(made[1].disposed)
        // Automatic again: v2 is untouched; the next video is libmpv's, with desktop-mpv
        onMain { prefs.chooseEngine(EngineChoice.Auto) }
        Thread.sleep(300)
        assertEquals(3, opened.get())
        assertFalse(fxMade.single().disposed)
        assertTrue(onMain { playback.player } === fxMade.single())
        onMain { playback.play(PlayRequest("v3", "v3", startSec = 0)) }
        until("v3 playing") { made.size == 3 && playback.player === made[2] }
        assertTrue(bodies()[3], bodies()[3].contains("\"client_profile\":\"desktop-mpv\""))
        assertEquals(1, fxMade.size)
        // the problem report names each video's engine and why
        val log = engines.logLines().joinToString("\n")
        assertTrue(log, log.contains("libmpv, automatic") && log.contains("JavaFX, chosen in Profile"))
    }

    @Test
    fun aFileThatFailsWithTheOlderEngineIsNotForcedToTranscode() {
        // JavaFX's sessions already ask the home to convert: a failure is the watch loop's
        prefs.chooseEngine(EngineChoice.Older)
        home.answer("GET /api/stream/capabilities", """{"client_profiles":["desktop-mpv"]}""")
        onMain { playback.play(PlayRequest("v1", "v1", startSec = 0)) }
        until("playing") { playback.player != null && playback.player === fxMade.singleOrNull() }
        fxMade.single().started = false
        fxMade.single().events.failed("no decoder")
        Thread.sleep(300)
        assertEquals(1, opened.get())
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
