package com.hamzalabs.maktaba.desktop.search

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.size
import androidx.compose.ui.Modifier
import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
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
import org.junit.Assert.assertTrue
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

    // ---------- story 49.11 ----------

    private val helped = """{"search_id":"s1","q":"fasting","sort":"relevance","did_you_mean":"fasting rules","videos":[{"video_id":"v1","title":"Fasting 1"}],"videos_total":1,
        "transcript_videos":[],"transcript_total":0,
        "facets":{"library":[{"value":"l1","label":"Lessons","count":3},{"value":"l2","label":"Talks","count":1}],"language":[{"value":"ar","count":2}],
          "speaker":[],"year":[{"value":"2024","count":3,"filter":{"date":{"gte":"2024-01-01T00:00:00.000Z","lt":"2025-01-01T00:00:00.000Z"}}}],
          "duration":[{"value":"over_60m","count":1,"filter":{"duration_sec":{"gte":3600}}}],"videos":4}}"""

    private fun helped(fake: TestHome): SearchModel {
        fake.answer("POST /api/search", helped)
        return SearchModel(fake.client, rest = {}).also { runBlocking { it.submit("fasting") } }
    }

    private fun lastBody(fake: TestHome) = fake.asked("POST /api/search").last().body!!.utf8()

    @Test
    fun facetChipsSortAndDidYouMeanAreOnTheResults() = TestHome().use { fake ->
        val m = helped(fake)
        val home = fake.home()
        runComposeUiTest {
            setContent { InPlayer(home) { Box(Modifier.size(900.dp, 900.dp)) { SearchContent(m, home, fake.features.value) } } }
            onNodeWithText("Sort by: Relevance").assertExists()
            onNodeWithText("Narrow the results").assertExists()
            onNode(hasContentDescription("Talks, 1 video")).assertExists()
            onNode(hasContentDescription("Over an hour, 1 video")).assertExists()
            onNode(hasContentDescription("Arabic, 2 videos")).assertExists()
            onNodeWithText("Did you mean", substring = true).assertExists()

            onNodeWithTag("facet-library-l2").performClick()
            waitUntil(timeoutMillis = 5_000) { fake.asked("POST /api/search").size == 2 }
            assertTrue(lastBody(fake).contains("\"library_id\":[\"l2\"]"))
            waitUntil(timeoutMillis = 5_000) { onAllNodes(hasContentDescription("Remove Library: Talks")).fetchSemanticsNodes().isNotEmpty() }

            onNodeWithTag("search-sort").performClick()
            onNodeWithTag("search-sort-newest").performClick()
            waitUntil(timeoutMillis = 5_000) { fake.asked("POST /api/search").size == 3 }
            assertTrue(lastBody(fake).contains("\"sort\":\"newest\""))
            assertTrue("the filter stays", lastBody(fake).contains("\"library_id\":[\"l2\"]"))

            onNodeWithTag("active-library-l2").performClick()
            waitUntil(timeoutMillis = 5_000) { fake.asked("POST /api/search").size == 4 }
            assertTrue(!lastBody(fake).contains("filters"))

            onNodeWithTag("search-did-you-mean-run").performClick()
            waitUntil(timeoutMillis = 5_000) { fake.asked("POST /api/search").size == 5 }
            assertTrue(lastBody(fake).contains("\"q\":\"fasting rules\""))
        }
    }

    @Test
    fun theHelpIsInArabic() = TestHome().use { fake ->
        val m = helped(fake)
        val home = fake.home()
        runComposeUiTest {
            setContent { InPlayer(home, Lang.Ar) { Box(Modifier.size(900.dp, 900.dp)) { SearchContent(m, home, fake.features.value) } } }
            onNodeWithText("الترتيب حسب: الأقرب صلة").assertExists()
            onNodeWithText("تضييق النتائج").assertExists()
            onNodeWithText("هل تقصد", substring = true).assertExists()
            onNode(hasContentDescription("أكثر من ساعة، عدد المقاطع: 1")).assertExists()
            onNode(hasContentDescription("العربية", substring = true)).assertExists()
        }
    }

    @Test
    fun suggestionsShowTheirKind() = TestHome().use { fake ->
        fake.answer("GET /api/search/suggest", """{"suggestions":["fasting","Fasting 1"],"items":[{"text":"fasting","kind":"term"},{"text":"Fasting 1","kind":"title","video_id":"v1"}]}""")
        val m = SearchModel(fake.client, rest = {}).also { runBlocking { it.typing("fa") } }
        val home = fake.home()
        runComposeUiTest {
            setContent { InPlayer(home) { Box(Modifier.size(800.dp, 700.dp)) { SearchContent(m, home, fake.features.value) } } }
            onNode(hasTestTag("suggestion-0") and hasText("Word", substring = true)).assertExists()
            onNode(hasTestTag("suggestion-1") and hasText("Video", substring = true)).assertExists()
        }
    }
}
