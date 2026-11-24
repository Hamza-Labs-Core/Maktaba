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

    private val players = object : Players {
        override val profile = MPV_PROFILE
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
    }
}
