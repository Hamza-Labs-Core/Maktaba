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
import com.hamzalabs.maktaba.desktop.TestHome
import com.hamzalabs.maktaba.desktop.Video
import com.hamzalabs.maktaba.desktop.Watch
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import org.junit.Assert.assertEquals
import org.junit.Test
import java.util.concurrent.atomic.AtomicInteger

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
                waitUntil(timeoutMillis = 10_000) { onAllNodes(hasTestTag("parental-pin")).fetchSemanticsNodes().isNotEmpty() }
                assertEquals(1, opens.get())
                onNodeWithTag("parental-pin").performClick()
                onNodeWithTag("parental-pin-field").performTextInput("123456")
                onNodeWithTag("parental-pin-submit").performClick()
                // the unlock checks again once; the home still refuses and the screen is back
                waitUntil(timeoutMillis = 10_000) { opens.get() >= 2 }
                waitUntil(timeoutMillis = 10_000) { onAllNodes(hasTestTag("parental-pin")).fetchSemanticsNodes().isNotEmpty() }
                // a few seconds of the screen standing there: nothing asks by itself
                val end = System.currentTimeMillis() + 2_000
                while (System.currentTimeMillis() < end) {
                    waitForIdle()
                    Thread.sleep(50)
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
}
