package com.hamzalabs.maktaba.desktop.watch

import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextInput
import androidx.compose.ui.test.runComposeUiTest
import com.hamzalabs.maktaba.core.api.Capabilities
import com.hamzalabs.maktaba.desktop.InPlayer
import com.hamzalabs.maktaba.desktop.PlayRequest
import com.hamzalabs.maktaba.desktop.Playback
import com.hamzalabs.maktaba.desktop.Players
import com.hamzalabs.maktaba.desktop.STALL_MS
import com.hamzalabs.maktaba.desktop.TestHome
import com.hamzalabs.maktaba.desktop.Video
import com.hamzalabs.maktaba.desktop.Watch
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.launch
import org.junit.Assert.assertEquals
import org.junit.Test
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.locks.LockSupport

/**
 * Epic 57 story 57.15 on the desktop Player's Watch screen: a parent's PIN asks the home once.
 * When the time is still over (or runs out again) the time's-up screen is back as it first was,
 * and nothing asks the home again by itself.
 */
@OptIn(ExperimentalTestApi::class)
class WatchTimesUpTest {
    private val timeOver = """{"type":"https://maktaba.tv/problems/parental-time","title":"watching time is over","status":403,""" +
        """"code":"parental_time","reason":"limit_reached","next_allowed_at":"2026-11-12T04:00:00Z"}"""

    private val noVideo = Players { _, _, _, _ ->
        object : Video {
            override fun toggle() {}
            override fun pause() {}
            override fun seek(ms: Long) {}
            override fun sound(volume: Float, muted: Boolean) {}
            override fun dispose() {}
        }
    }

    @Test
    fun anUnlockAsksTheHomeOnceAndNeverAgainByItself() = TestHome().use { fake ->
        val opens = AtomicInteger()
        fake.answer("POST /api/stream/sessions") {
            opens.incrementAndGet()
            TestHome.json(timeOver, 403)
        }
        fake.answer("GET /api/me/parental/guardians", """{"guardians":[{"id":"g1","username":"huda"}]}""")
        fake.answer("POST /api/me/parental/unlock", """{"grant":{"id":"t1","minutes":15}}""")
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default.limitedParallelism(1))
        val playback = Playback(fake.client, scope, scope, signedOut = {}, players = noVideo)
        try {
            runComposeUiTest {
                setContent {
                    InPlayer(fake.home()) { Watch(playback, Capabilities.AllOn, "http://home", fullScreen = false, onFullScreen = {}, onClose = {}) }
                }
                runOnIdle { playback.play(PlayRequest("A", "A", startSec = 754)) }
                waitUntil(timeoutMillis = STALL_MS) { onAllNodes(hasTestTag("parental-pin")).fetchSemanticsNodes().isNotEmpty() }
                assertEquals(1, opens.get())
                onNodeWithTag("parental-pin").performClick()
                onNodeWithTag("parental-pin-field").performTextInput("123456")
                onNodeWithTag("parental-pin-submit").performClick()
                // the unlock checks again once; the home still refuses and the screen is back
                waitUntil(timeoutMillis = STALL_MS) { opens.get() >= 2 }
                waitUntil(timeoutMillis = STALL_MS) { onAllNodes(hasTestTag("parental-pin")).fetchSemanticsNodes().isNotEmpty() }
                // the screen stands there with every answer read and nothing left on its way:
                // nothing asks by itself
                waitUntil(timeoutMillis = STALL_MS) {
                    val asked = fake.seen.size
                    val quiet = fake.idle && scope.coroutineContext[Job]!!.children.none()
                    waitForIdle()
                    quiet && fake.idle && scope.coroutineContext[Job]!!.children.none() && asked == fake.seen.size
                }
                assertEquals("one open for the video, one for the unlock", 2, opens.get())
                assertEquals("one PIN was sent", 1, fake.asked("POST /api/me/parental/unlock").size)
                // the screen is as it first was: the PIN panel is closed and can be opened again
                assertEquals(0, onAllNodes(hasTestTag("parental-pin-panel")).fetchSemanticsNodes().size)
                onNodeWithTag("parental-time-check").assertExists()
            }
        } finally {
            playback.close()
            scope.cancel()
        }
    }

    /** Story 57.15: a rule may be on one library only, so the time is asked for with the video's library. */
    @Test
    fun theTimeIsAskedForWithTheVideosLibrary() = TestHome().use { fake ->
        fake.answer("GET /api/me/parental", """{"restricted":true,"max_tier":13,"allow_only":false,"version":1}""")
        fake.answer("GET /api/me/parental/time", """{"allowed":true,"limited":true,"remaining_seconds":1800}""")
        fake.answer("GET /api/videos/A", """{"id":"A","library_id":"l1","filename":"a.mkv","title":"A","state":"ready"}""")
        // no stream opens here (the video's surface is Swing's, which a test window has none of)
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default.limitedParallelism(1))
        val playback = Playback(fake.client, scope, scope, signedOut = {}, players = noVideo)
        val home = fake.home()
        try {
            kotlinx.coroutines.runBlocking { home.parental.refresh() }
            runComposeUiTest {
                setContent {
                    InPlayer(home) { Watch(playback, Capabilities.AllOn, "http://home", fullScreen = false, onFullScreen = {}, onClose = {}, parental = home.parental) }
                }
                // a page that does not know the library (a search hit): the Player asks the home
                runOnIdle { playback.play(PlayRequest("A", "A", startSec = 0)) }
                waitUntil(timeoutMillis = STALL_MS) {
                    fake.asked("GET /api/me/parental/time").any { it.url.queryParameter("library_id") == "l1" }
                }
            }
            // and a page that knows it says so: nothing is looked up
            val before = fake.asked("GET /api/videos/A").size
            scope.launch { playback.play(PlayRequest("A", "A", startSec = 5, libraryId = "l9")) }
            // a wait for the state; the bound is only the net under a stall
            val end = System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(STALL_MS)
            while (playback.libraryId != "l9") {
                check(System.nanoTime() < end) { "the request's library was not taken" }
                LockSupport.parkNanos(500_000)
            }
            assertEquals(before, fake.asked("GET /api/videos/A").size)
        } finally {
            playback.close()
            scope.cancel()
        }
    }
}
