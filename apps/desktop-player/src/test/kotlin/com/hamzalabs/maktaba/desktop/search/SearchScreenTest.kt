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
import com.hamzalabs.maktaba.core.AiStreams
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.test.performKeyInput
import androidx.compose.ui.test.performTextInput
import androidx.compose.ui.test.pressKey
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import mockwebserver3.MockResponse

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

    @Test
    fun aTitleSuggestionOnAWideWindowShowsItsPageBesideEvenBeforeAnyResults() = TestHome().use { fake ->
        fake.answer("GET /api/search/suggest", """{"suggestions":["Fasting 1"],"items":[{"text":"Fasting 1","kind":"title","video_id":"v1"}]}""")
        val m = SearchModel(fake.client, rest = {}).also { runBlocking { it.typing("fa") } }
        val home = fake.home()
        // a window wider than the page-beside breakpoint (1,100 dp)
        androidx.compose.ui.test.runDesktopComposeUiTest(width = 1400, height = 800) {
            setContent { InPlayer(home) { Box(Modifier.size(1400.dp, 800.dp)) { SearchContent(m, home, fake.features.value) } } }
            onNodeWithTag("suggestion-0").performClick()
            waitUntil(timeoutMillis = 5_000) { onAllNodes(hasTestTag("search-preview")).fetchSemanticsNodes().isNotEmpty() }
            assertEquals("v1", m.chosen)
            assertTrue("no search was run", fake.asked("POST /api/search").isEmpty())
        }
    }

    // ---------- story 49.14: AI mode's tab ----------

    private fun stream(body: String) = MockResponse.Builder().code(200).setHeader("Content-Type", "text/event-stream").body(body).build()

    private fun aiAsks(fake: TestHome) = fake.asked("POST /api/search/ai").map { Json.parseToJsonElement(it.body!!.utf8()).jsonObject }

    private fun answering(fake: TestHome, passages: List<AiStreams.Passage> = AiStreams.EN_PASSAGES, sentences: List<AiStreams.Sentence> = AiStreams.EN_SENTENCES) =
        fake.answer("POST /api/search/ai") { r ->
            stream(AiStreams.answer(Json.parseToJsonElement(r.body!!.utf8()).jsonObject["q"]!!.jsonPrimitive.content, passages, sentences))
        }

    @Test
    fun aiModeAsksOnlyWhenOpenedThenStreamsAndACitationPlays() = TestHome().use { fake ->
        answering(fake)
        val m = searched(fake)
        val home = fake.home()
        runComposeUiTest {
            setContent { InPlayer(home) { Box(Modifier.size(900.dp, 900.dp)) { SearchContent(m, home, fake.features.value) } } }
            onNodeWithTag("title-v1").assertExists()
            assertTrue("Results never asks AI mode", fake.asked("POST /api/search/ai").isEmpty())
            onNodeWithTag("tab-ai").performClick()
            waitUntil(timeoutMillis = 5_000) { onAllNodes(hasTestTag("ai-sentence-1-1")).fetchSemanticsNodes().isNotEmpty() }
            assertEquals("fasting", aiAsks(fake).single()["q"]!!.jsonPrimitive.content)
            onNodeWithText("Passages read").assertExists()
            onNodeWithTag("ai-passage-1-2").assertExists()
            onNodeWithText("Patience is half of faith.").assertExists()
            onNode(hasContentDescription("Source 2, The Night Journey, play from \u2066" + "22:02", substring = true)).assertExists()
            onNodeWithTag("ai-cite-1-1-2").performClick()
            assertEquals("v4", fake.played.single().videoId)
            assertEquals(1322, fake.played.single().startSec)
            // a passage plays from its moment too
            onNodeWithTag("ai-passage-1-1").performClick()
            assertEquals(754, fake.played.last().startSec)
        }
    }

    @Test
    fun aFollowUpWithEnterCarriesTheEarlierAnswer() = TestHome().use { fake ->
        answering(fake)
        val m = searched(fake)
        val home = fake.home()
        runComposeUiTest {
            setContent { InPlayer(home) { Box(Modifier.size(900.dp, 900.dp)) { SearchContent(m, home, fake.features.value) } } }
            onNodeWithTag("tab-ai").performClick()
            waitUntil(timeoutMillis = 5_000) { onAllNodes(hasTestTag("ai-sentence-1-1")).fetchSemanticsNodes().isNotEmpty() }
            onNodeWithTag("ai-follow-up").performTextInput("and its reward?")
            onNodeWithTag("ai-follow-up").performKeyInput { pressKey(Key.Enter) }
            waitUntil(timeoutMillis = 5_000) { aiAsks(fake).size == 2 }
            val turn = aiAsks(fake).last()["history"]!!.jsonArray.single().jsonObject
            assertEquals("fasting", turn["q"]!!.jsonPrimitive.content)
            assertEquals("Patience is half of faith. Its reward comes without measure.", turn["answer"]!!.jsonPrimitive.content)
            waitUntil(timeoutMillis = 5_000) { onAllNodes(hasTestTag("ai-question-2")).fetchSemanticsNodes().isNotEmpty() }
        }
    }

    @Test
    fun noLanguageModelOpensTheSettingAndTheHelpInTheBrowser() = TestHome().use { fake ->
        fake.answer("POST /api/search/ai", AiStreams.UNAVAILABLE, 409)
        val m = searched(fake)
        val home = fake.home()
        runComposeUiTest {
            setContent { InPlayer(home) { Box(Modifier.size(900.dp, 900.dp)) { SearchContent(m, home, fake.features.value) } } }
            onNodeWithTag("tab-ai").performClick()
            waitUntil(timeoutMillis = 5_000) { onAllNodes(hasTestTag("ai-unavailable")).fetchSemanticsNodes().isNotEmpty() }
            onNodeWithText("AI mode is not set up").assertExists()
            onNodeWithTag("ai-help").performClick()
            assertTrue(fake.opened.last().endsWith("/help/search#ai-mode"))
            onNodeWithTag("ai-settings").performClick()
            assertTrue(fake.opened.last().endsWith("/settings/rules?section=tagcheck&q=url"))
        }
    }

    @Test
    fun anOlderHomeAndAnAdminsSwitchEachSayWhy() = TestHome().use { fake ->
        val m = searched(fake)
        val home = fake.home()
        runComposeUiTest {
            setContent { InPlayer(home) { Box(Modifier.size(900.dp, 900.dp)) { SearchContent(m, home, fake.features.value) } } }
            onNodeWithTag("tab-ai").performClick()
            waitUntil(timeoutMillis = 5_000) { onAllNodes(hasTestTag("ai-old-home")).fetchSemanticsNodes().isNotEmpty() }
            fake.answer("POST /api/search/ai", AiStreams.OFF, 409)
            onNodeWithTag("ai-follow-up").performTextInput("again")
            onNodeWithTag("ai-ask").performClick()
            waitUntil(timeoutMillis = 5_000) { onAllNodes(hasTestTag("ai-off")).fetchSemanticsNodes().isNotEmpty() }
        }
    }

    @Test
    fun aiModeIsInArabic() = TestHome().use { fake ->
        answering(fake, AiStreams.AR_PASSAGES, AiStreams.AR_SENTENCES)
        val m = searched(fake)
        val home = fake.home()
        runComposeUiTest {
            setContent { InPlayer(home, Lang.Ar) { Box(Modifier.size(900.dp, 900.dp)) { SearchContent(m, home, fake.features.value) } } }
            onNodeWithText("وضع الذكاء الاصطناعي").performClick()
            waitUntil(timeoutMillis = 5_000) { onAllNodes(hasTestTag("ai-sentence-1-1")).fetchSemanticsNodes().isNotEmpty() }
            onNodeWithText("الصبر نصف الإيمان.").assertExists()
            onNodeWithText("المقاطع المقروءة").assertExists()
            onNode(hasContentDescription("المصدر 1، الإسراء والمعراج، تشغيل من \u2066" + "12:34", substring = true)).assertExists()
        }
    }
}
