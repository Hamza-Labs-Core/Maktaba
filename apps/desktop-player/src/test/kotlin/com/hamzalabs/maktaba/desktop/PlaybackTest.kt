package com.hamzalabs.maktaba.desktop

import com.hamzalabs.maktaba.core.playback.PlaybackValues
import com.hamzalabs.maktaba.core.playback.QualityOption
import com.hamzalabs.maktaba.core.playback.StreamQuality
import com.hamzalabs.maktaba.core.playback.SubtitleLanguage
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.asCoroutineDispatcher
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withContext
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.util.Collections
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger

/**
 * Story 55.08 with a fake player: each `POST /api/stream/sessions` is session s1, s2 … (a direct
 * file); [held] sessions answer only once their gate opens, as a slow reopen.
 */
class PlaybackTest {
    private val home = TestHome()
    private val main = Executors.newSingleThreadExecutor { r -> Thread(r, "test-main").apply { isDaemon = true } }.asCoroutineDispatcher()
    private val scope = CoroutineScope(SupervisorJob() + main)
    private val background = CoroutineScope(SupervisorJob() + main)
    private val opened = AtomicInteger()
    private val held = ConcurrentHashMap<Int, CountDownLatch>()

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

    /** The audio tracks each new player reports when ready (none: it reports nothing). */
    private var tracks: List<AudioOption> = emptyList()

    private val players = Players { url, _, _, events ->
        FakeVideo(url).also {
            made += it
            if (tracks.isNotEmpty()) events.ready(60_000, tracks)
        }
    }

    private var settings = PlaybackValues()

    private val playback = Playback(home.client, scope, background, signedOut = {}, settings = { settings }, players = players)

    init {
        home.answer("POST /api/stream/sessions") {
            val n = opened.incrementAndGet()
            held[n]?.await(60, TimeUnit.SECONDS)
            TestHome.json("""{"session_id":"s$n","mode":"direct","direct_url":"/stream/s$n.mp4"}""")
        }
    }

    @After
    fun tearDown() {
        held.values.forEach { it.countDown() }
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

    private fun closed(s: String) = home.asked("DELETE /api/stream/sessions/$s").isNotEmpty()

    /** The URL the player shown plays (read on the main thread). */
    private fun shownUrl(): String? = (playback.player as FakeVideo?)?.url

    private fun shown(): String? = onMain(::shownUrl)

    private fun start(id: String) {
        onMain { playback.play(PlayRequest(id, id, startSec = 0)) }
    }

    @Test
    fun aQualityChangeThatEndsAfterCloseLeavesNothingPlaying() {
        start("A")
        until("A plays") { playback.player != null }
        held[2] = CountDownLatch(1)
        onMain { playback.chooseQuality(QualityOption(480, 1200)) }
        until("the reopen asks") { opened.get() == 2 }
        // Escape while it is changing
        onMain { playback.stop() }
        held.getValue(2).countDown()
        until("the late session is closed") { closed("s2") }
        Thread.sleep(200)
        assertNull("no player once closed", onMain { playback.player })
        assertTrue("every player made is disposed", made.all { it.disposed })
        assertTrue(closed("s1"))
    }

    @Test
    fun aQualityChangeThatEndsAfterTheNextVideoStartsLeavesItAlone() {
        start("A")
        until("A plays") { playback.player != null }
        held[2] = CountDownLatch(1)
        onMain { playback.chooseQuality(QualityOption(480, 1200)) }
        until("the reopen asks") { opened.get() == 2 }
        // video B starts while A's change waits
        start("B")
        until("B plays") { shownUrl()?.endsWith("/stream/s3.mp4") == true }
        held.getValue(2).countDown()
        until("A's late session is closed") { closed("s2") }
        Thread.sleep(200)
        assertTrue("B still plays: ${shown()}", shown()!!.endsWith("/stream/s3.mp4"))
        assertTrue("B's session stays open", !closed("s3"))
        assertEquals("B's request", "B", onMain { playback.request?.videoId })
        assertTrue("nothing of A's change is left playing", made.filter { !it.url.endsWith("s3.mp4") }.all { it.disposed })
    }

    @Test
    fun aDirectFileStartsInTheKeptAudioLanguage() {
        settings = PlaybackValues(audioLanguage = SubtitleLanguage.Arabic)
        tracks = listOf(AudioOption("English", "eng", index = 0), AudioOption("العربية", "ara", index = 1))
        start("A")
        until("the Arabic track is asked for") { opened.get() == 2 }
        val body = home.asked("POST /api/stream/sessions")[1].body!!.utf8()
        assertTrue(body, "\"audio_track\":1" in body && "\"format\":\"hls\"" in body)
        until("Arabic is chosen") { playback.audio?.index == 1 }
    }

    @Test
    fun aDirectFileWithTheLanguageFirstIsNotReopened() {
        settings = PlaybackValues(audioLanguage = SubtitleLanguage.English)
        tracks = listOf(AudioOption("English", "eng", index = 0), AudioOption("العربية", "ara", index = 1))
        start("A")
        until("A plays") { playback.player != null && playback.audioOptions.size == 2 }
        Thread.sleep(300)
        assertEquals(1, opened.get())
    }

    @Test
    fun theSettingsQualityIsTheLadderRungOfItsHeight() {
        settings = PlaybackValues(qualityWifi = StreamQuality.P720)
        start("A")
        until("A plays") { playback.player != null }
        // the settings' 720p caps at 4000; the ladder's 720p rung is 3000: the menu marks that one
        assertEquals(QualityOption(720, 3000), onMain { playback.quality })
        assertTrue(onMain { playback.quality in playback.ladder })
    }
}
