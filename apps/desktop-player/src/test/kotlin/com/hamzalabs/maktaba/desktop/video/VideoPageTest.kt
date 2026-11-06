package com.hamzalabs.maktaba.desktop.video

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.size
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.runComposeUiTest
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.api.Feature
import com.hamzalabs.maktaba.desktop.InPlayer
import com.hamzalabs.maktaba.desktop.Lang
import com.hamzalabs.maktaba.desktop.TestHome
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.util.concurrent.CountDownLatch

/** Story 55.06 T06 and T07, off screen. */
@OptIn(ExperimentalTestApi::class)
class VideoPageTest {
    private fun loaded(f: TestHome): VideoPageState {
        f.answer("GET /api/videos/v1", """{"id":"v1","title":"Fiqh 7","duration_sec":3000}""")
        f.answer("GET /api/me/videos/v1", """{"video_id":"v1","position_sec":724}""")
        f.answer("GET /api/videos/v1/chapters", """{"items":[{"seq":2,"start_sec":600,"title":"Rules"}]}""")
        return VideoPageState(f.client, "v1", {}).also { runBlocking { it.load() } }
    }

    @Test
    fun resumePlaysFromTheSavedSecondAndAChapterFromItsStart() = TestHome().use { f ->
        val s = loaded(f)
        val home = f.home()
        runComposeUiTest {
            setContent { InPlayer(home) { Box(Modifier.size(1200.dp, 900.dp)) { VideoContent(s, home, f.features.value) } } }
            onNodeWithTag("resume").performClick()
            onNodeWithTag("from-start").assertExists()
            onNodeWithTag("chapter-2").performClick()
            assertEquals(listOf(724, 600), f.played.map { it.startSec })
        }
    }

    @Test
    fun aFavoriteMadeJustBeforeLeavingThePageStays() = TestHome().use { f ->
        val s = loaded(f)
        val gate = CountDownLatch(1)
        f.held("POST /api/me/favorites", { """{"updated":1}""" }, gate)
        val home = f.home()
        var shown by mutableStateOf(true)
        runComposeUiTest {
            setContent { InPlayer(home) { if (shown) Box(Modifier.size(1200.dp, 900.dp)) { VideoContent(s, home, f.features.value) } } }
            onNodeWithTag("favorite").performClick()
            waitUntil(timeoutMillis = 5_000) { f.asked("POST /api/me/favorites").isNotEmpty() }
            // the person goes back while the home has not answered yet
            shown = false
            waitForIdle()
            gate.countDown()
            Thread.sleep(500)
            waitForIdle()
            assertTrue("the favorite stays", s.favorite)
            assertFalse(s.saveFailed)
        }
    }

    @Test
    fun chaptersOffHideTheListFromAViewerAndExplainItToAnAdmin() = TestHome().use { f ->
        val s = loaded(f)
        f.features.value = TestHome.caps(Feature.CHAPTERS)
        runComposeUiTest {
            setContent { InPlayer(f.home()) { Box(Modifier.size(1200.dp, 900.dp)) { VideoContent(s, f.home(), f.features.value) } } }
            assertEquals(0, onAllNodes(hasTestTag("chapters")).fetchSemanticsNodes().size)
            assertEquals(0, onAllNodes(hasTestTag("feature-off-chapters")).fetchSemanticsNodes().size)
        }
        f.features.value = TestHome.caps(Feature.CHAPTERS, admin = true)
        runComposeUiTest {
            setContent { InPlayer(f.home(admin = true), Lang.Ar) { Box(Modifier.size(1200.dp, 900.dp)) { VideoContent(s, f.home(admin = true), f.features.value) } } }
            onNodeWithText("الفصول متوقفة على الجهاز الرئيسي.").assertExists()
            // the poster is on the right in Arabic
            val page = onNodeWithTag("video-page").fetchSemanticsNode().boundsInRoot
            val resume = onNodeWithTag("resume").fetchSemanticsNode().boundsInRoot
            assertTrue("details on the left of the poster: $resume in $page", resume.right < page.right - 400f)
        }
    }
}
