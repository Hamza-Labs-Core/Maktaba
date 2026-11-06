package com.hamzalabs.maktaba.desktop.search

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.size
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
import org.junit.Test

/** Story 55.04 T06 and T07, off screen. */
@OptIn(ExperimentalTestApi::class)
class SearchScreenTest {
    private val results = """{"search_id":"s1","q":"fasting","videos":[{"video_id":"v1","title":"Fasting 1"}],"videos_total":1,
        "transcript_videos":[{"video_id":"v2","title":"Talk","duration_sec":3000,"passages":[{"start_sec":724,"snippet":"the <mark>fasting</mark> day"}]}],"transcript_total":1}"""

    private fun searched(fake: TestHome, meaning: Boolean = true): SearchModel {
        fake.answer("POST /api/search", results)
        return SearchModel(fake.client, meaningOn = { meaning }, rest = {}).also { runBlocking { it.submit("fasting") } }
    }

    @Test
    fun playFromPlaysAtThatSecond() = TestHome().use { fake ->
        val m = searched(fake)
        val home = fake.home()
        runComposeUiTest {
            setContent { InPlayer(home) { Box(Modifier.size(800.dp, 700.dp)) { SearchContent(m, home, fake.features.value) } } }
            onNodeWithTag("play-from-v2-724").performClick()
            assertEquals(724, fake.played.single().startSec)
            assertEquals("v2", fake.played.single().videoId)
        }
    }

    @Test
    fun transcriptsOffHideTheGroupFromAViewerAndExplainItToAnAdmin() = TestHome().use { fake ->
        val m = searched(fake)
        fake.features.value = TestHome.caps(Feature.TRANSCRIPTS, Feature.SEMANTIC_SEARCH)
        runComposeUiTest {
            setContent { InPlayer(fake.home()) { Box(Modifier.size(800.dp, 700.dp)) { SearchContent(m, fake.home(), fake.features.value) } } }
            onNodeWithTag("title-v1").assertExists()
            assertEquals(0, onAllNodes(hasTestTag("transcripts")).fetchSemanticsNodes().size)
            assertEquals(0, onAllNodes(hasTestTag("feature-off-transcripts")).fetchSemanticsNodes().size)
            assertEquals("a viewer sees no line", 0, onAllNodes(hasTestTag("words-only")).fetchSemanticsNodes().size)
        }
        fake.features.value = TestHome.caps(Feature.TRANSCRIPTS, Feature.SEMANTIC_SEARCH, admin = true)
        runComposeUiTest {
            setContent { InPlayer(fake.home(admin = true), Lang.Ar) { Box(Modifier.size(800.dp, 700.dp)) { SearchContent(m, fake.home(admin = true), fake.features.value) } } }
            onNodeWithTag("feature-off-transcripts").assertExists()
            onNodeWithText("البحث بالكلمات فقط. البحث بالمعنى متوقف.").assertExists()
        }
    }
}
