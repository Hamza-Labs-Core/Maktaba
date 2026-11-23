package com.hamzalabs.maktaba.player.search

import androidx.activity.ComponentActivity
import androidx.compose.material3.Text
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.SemanticsNodeInteraction
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performImeAction
import androidx.compose.ui.test.performScrollToNode
import androidx.compose.ui.test.performTextInput
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.core.AiStreams
import com.hamzalabs.maktaba.core.api.Feature
import androidx.compose.ui.test.assertIsSelected
import kotlinx.serialization.json.jsonArray
import mockwebserver3.MockResponse
import java.util.concurrent.TimeUnit
import com.hamzalabs.maktaba.player.harness.FakeHome
import com.hamzalabs.maktaba.player.harness.FakeHome.Companion.json
import com.hamzalabs.maktaba.player.harness.setShell
import com.hamzalabs.maktaba.player.harness.settledUntil
import com.hamzalabs.maktaba.player.shell.AppScreens
import com.hamzalabs.maktaba.player.shell.PlayerScreens
import com.hamzalabs.maktaba.player.shell.PlayerSection
import com.hamzalabs.maktaba.player.shell.Route
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config

/**
 * Search's UI test (48.29 as 48.20, NFR-19), in the shell against the fake home's
 * `/api/search*` answers: typing shows suggestions; submit shows Titles and In the transcript;
 * a passage opens the player at its time; an Arabic query is sent unchanged.
 */
@RunWith(AndroidJUnit4::class)
@Config(qualifiers = "w411dp-h891dp")
class SearchScreenTest {
    @get:Rule(order = 0) val home = FakeHome()
    @get:Rule(order = 1) val compose = createAndroidComposeRule<ComponentActivity>()

    /** The app's screens, with the pushed ones it opens shown as words to assert on. */
    private val screens: PlayerScreens = { route, nav ->
        when (route) {
            is Route.Watch -> Text("watch ${route.videoId} at ${route.startSec}", Modifier.testTag("watch"))
            is Route.Video -> Text("video ${route.id}", Modifier.testTag("video"))
            else -> AppScreens(route, nav)
        }
    }

    private fun open(l: SearchFixtures.Lang = SearchFixtures.EN) {
        SearchFixtures.install(home, l)
        compose.setShell(home, sections = PlayerSection.enabled, screens = screens)
        compose.onNodeWithTag("section-Search").performClick()
        compose.onNodeWithTag("search").assertIsDisplayed()
    }

    private fun waitFor(text: String) =
        compose.settledUntil(5_000) { compose.onAllNodes(hasText(text, substring = true)).fetchSemanticsNodes().isNotEmpty() }

    private fun waitForTag(tag: String) =
        compose.settledUntil(5_000) { compose.onAllNodesWithTag(tag).fetchSemanticsNodes().isNotEmpty() }

    private fun field(): SemanticsNodeInteraction = compose.onNode(hasSetTextAction())

    private fun search(q: String) {
        field().performClick()
        field().performTextInput(q)
        field().performImeAction()
        waitForTag("search-results")
    }

    private fun searchBodies() = home.requests.filter { it.method == "POST" && it.url.encodedPath == "/api/search" }
        .map { Json.parseToJsonElement(it.body!!.utf8()).jsonObject }

    @Test
    fun theSectionIsShipped() {
        assertTrue(PlayerSection.Search in PlayerSection.enabled)
    }

    @Test
    fun typingShowsSuggestionsAndOneRunsThatSearch() {
        open()
        field().performClick()
        field().performTextInput("pati")
        compose.settledUntil(5_000) { compose.onAllNodesWithTag("suggestion").fetchSemanticsNodes().isNotEmpty() }
        compose.onNode(hasTestTag("suggestion") and hasText("patience in hardship", substring = true)).assertIsDisplayed()
        val asked = home.requests.last { it.url.encodedPath == "/api/search/suggest" }
        assertEquals("pati", asked.url.queryParameter("q"))

        compose.onNode(hasTestTag("suggestion") and hasText("patience in hardship", substring = true)).performClick()
        waitForTag("search-results")
        assertEquals("patience in hardship", searchBodies().last()["q"]!!.jsonPrimitive.content)
    }

    @Test
    fun submitShowsTitlesAndInTheTranscriptInHybridMode() {
        open()
        search("patience")
        compose.onNodeWithTag("titles-header").assertIsDisplayed()
        compose.onNodeWithText("Titles").assertIsDisplayed()
        compose.onNode(hasTestTag("title-v1")).assertIsDisplayed()
        compose.onNode(hasContentDescription("On Patience", substring = true)).assertIsDisplayed()
        compose.onNodeWithTag("search-results").performScrollToNode(hasTestTag("transcripts-header"))
        compose.onNodeWithText("In the transcript").assertIsDisplayed()
        compose.onNodeWithTag("search-results").performScrollToNode(hasTestTag("passage-v4-0"))
        compose.onNodeWithTag("passage-v4-0").assertIsDisplayed()

        val body = searchBodies().single()
        assertEquals("patience", body["q"]!!.jsonPrimitive.content)
        assertEquals("hybrid", body["mode"]!!.jsonPrimitive.content)
    }

    @Test
    fun aPassageOpensThePlayerAtItsTime() {
        open()
        search("patience")
        compose.onNodeWithTag("search-results").performScrollToNode(hasTestTag("passage-v4-1"))
        compose.onNodeWithTag("passage-v4-1").assertIsDisplayed()
        compose.onNode(hasContentDescription("Play from 22:02", substring = true)).assertExists()
        compose.onNodeWithTag("passage-v4-1").performClick()
        compose.onNodeWithTag("watch").assertIsDisplayed()
        compose.onNodeWithText("watch v4 at 1322").assertIsDisplayed()
    }

    @Test
    fun aTitleOpensTheVideosPage() {
        open()
        search("patience")
        compose.onNodeWithTag("title-v2").performClick()
        compose.onNodeWithText("video v2").assertIsDisplayed()
    }

    @Test
    @Config(qualifiers = "ar-w411dp-h891dp")
    fun anArabicQueryIsSentUnchanged() {
        val l = SearchFixtures.AR
        open(l)
        search(l.query) // with its diacritics: the home ranks it, the app changes nothing
        assertEquals(l.query, searchBodies().single()["q"]!!.jsonPrimitive.content)
        compose.onNodeWithText("العناوين").assertIsDisplayed()
        compose.onNode(hasContentDescription("في الصبر", substring = true)).assertIsDisplayed()
    }

    // ---------- story 51.11: search by meaning off on the home (T13) ----------

    @Test
    fun meaningOffAnAdminReadsTheWordsOnlyLineAndKeywordResultsShowWithoutAnError() {
        home.adminWithOff(Feature.SEMANTIC_SEARCH)
        // the home skipped the meaning half because it is off: not a failure, whatever degraded says
        SearchFixtures.install(home) { SearchFixtures.results(SearchFixtures.EN, degraded = true).replace("\"degraded\":true", "\"degraded\":true,\"semantic\":\"off\"") }
        compose.setShell(home, sections = PlayerSection.enabled, screens = screens)
        compose.onNodeWithTag("section-Search").performClick()
        waitForTag("search-words-only")
        compose.onNodeWithText("Searching by words only. Search by meaning is off.").assertIsDisplayed()
        search("patience")
        compose.onNode(hasTestTag("title-v1")).assertIsDisplayed()
        assertTrue("nothing says the results are degraded", compose.onAllNodes(hasText("text matches", substring = true, ignoreCase = true)).fetchSemanticsNodes().isEmpty())
        assertEquals("keyword only", "fts", searchBodies().single()["mode"]!!.jsonPrimitive.content)
        compose.onNodeWithTag("search-words-only").assertIsDisplayed()
    }

    @Test
    @Config(qualifiers = "ar-w411dp-h891dp")
    fun theWordsOnlyLineIsInArabic() {
        home.adminWithOff(Feature.SEMANTIC_SEARCH)
        open(SearchFixtures.AR)
        waitForTag("search-words-only")
        compose.onNodeWithText("البحث بالكلمات فقط. البحث بالمعنى متوقف.").assertIsDisplayed()
    }

    @Test
    fun meaningOffAViewerSearchesByWordsWithNoLine() {
        home.viewerWithOff(Feature.SEMANTIC_SEARCH)
        open()
        search("patience")
        compose.onNode(hasTestTag("title-v1")).assertIsDisplayed()
        assertTrue(compose.onAllNodesWithTag("search-words-only").fetchSemanticsNodes().isEmpty())
        assertEquals("fts", searchBodies().single()["mode"]!!.jsonPrimitive.content)
    }

    @Test
    fun meaningOnShowsNoLine() {
        open()
        assertTrue(compose.onAllNodesWithTag("search-words-only").fetchSemanticsNodes().isEmpty())
    }

    @Test
    fun showMorePagesTheTitlesFromTheKeptResults() {
        SearchFixtures.install(home) { SearchFixtures.results(SearchFixtures.EN, titlesTotal = 3) }
        compose.setShell(home, sections = PlayerSection.enabled, screens = screens)
        compose.onNodeWithTag("section-Search").performClick()
        search("patience")
        compose.onNodeWithTag("titles-more").performClick()
        waitForTag("title-v3")
        val page = home.requests.last { it.url.encodedPath == "/api/search/results/${SearchFixtures.SEARCH_ID}" }
        assertEquals("titles", page.url.queryParameter("list"))
        assertEquals("2", page.url.queryParameter("offset"))
        // every title is now shown: no more to ask for
        compose.onAllNodesWithTag("titles-more").fetchSemanticsNodes().let { assertTrue(it.isEmpty()) }
    }

    @Test
    fun recentSearchesShowRunAgainAndAreForgotten() {
        open()
        waitFor("the night journey")
        compose.onNodeWithText("Recent searches").assertIsDisplayed()

        // ✕ forgets one on the home
        compose.onNode(hasContentDescription("Remove the night journey")).performClick()
        compose.settledUntil(5_000) { home.requests.any { it.method == "DELETE" && it.url.encodedPath == "/api/search/recent/r1" } }
        compose.settledUntil(5_000) { compose.onAllNodes(hasText("the night journey")).fetchSemanticsNodes().isEmpty() }

        // a tap runs it again
        compose.onNode(hasTestTag("recent") and hasText("patience", substring = true)).performClick()
        waitForTag("search-results")
        assertEquals("patience", searchBodies().last()["q"]!!.jsonPrimitive.content)
    }

    @Test
    fun clearAllForgetsEveryRecentSearch() {
        open()
        waitForTag("recent-clear")
        compose.onNodeWithTag("recent-clear").performClick()
        compose.settledUntil(5_000) { home.requests.any { it.method == "DELETE" && it.url.encodedPath == "/api/search/recent" } }
        waitFor("Search for a title")
    }

    @Test
    fun nothingFoundSaysSo() {
        SearchFixtures.install(home) { SearchFixtures.EMPTY }
        compose.setShell(home, sections = PlayerSection.enabled, screens = screens)
        compose.onNodeWithTag("section-Search").performClick()
        field().performClick()
        field().performTextInput("nothing")
        field().performImeAction()
        waitForTag("search-none")
        compose.onNodeWithText("Nothing matches “nothing”.").assertIsDisplayed()
    }

    @Test
    fun aFailedSearchSaysWhyAndTryAgainAsksAgain() {
        var calls = 0
        SearchFixtures.install(home)
        home.answer("/api/search") { calls++; if (calls == 1) json("""{"error":"boom"}""", 500) else json(SearchFixtures.results(SearchFixtures.EN)) }
        compose.setShell(home, sections = PlayerSection.enabled, screens = screens)
        compose.onNodeWithTag("section-Search").performClick()
        field().performClick()
        field().performTextInput("patience")
        field().performImeAction()
        waitForTag("error-state")
        compose.onNodeWithText("Search failed").assertIsDisplayed()
        compose.onNodeWithText("Try again").performClick()
        waitForTag("search-results")
        assertEquals(2, searchBodies().size)
    }

    @Test
    fun anotherSectionAndBackKeepsTheResults() {
        open()
        search("patience")
        compose.onNodeWithTag("section-Libraries").performClick()
        compose.onNodeWithTag("section-Search").performClick()
        waitForTag("search-results")
        compose.onNode(hasTestTag("title-v1")).assertIsDisplayed()
        assertEquals("one search, not two", 1, searchBodies().size)
    }

    @Test
    fun marksBecomeHighlightedRunsInOneText() {
        val style = SpanStyle(fontWeight = FontWeight.Bold)
        val t = markedText("أقم <mark>الصلاة</mark> لدلوك", style)
        assertEquals("أقم الصلاة لدلوك", t.text)
        assertEquals(listOf("الصلاة"), t.spanStyles.map { t.text.substring(it.start, it.end) })
        assertEquals("plain words", markedText("plain words", style).text)
        assertEquals("a b", plain("a <mark>b</mark>"))
    }

    // ---------- story 49.11: suggestions' kinds, did-you-mean, facets, sort ----------

    private fun tagged(tag: String) = compose.onAllNodesWithTag(tag).fetchSemanticsNodes().isNotEmpty()

    @Test
    fun suggestionsSayWhatTheyAreAndATitleOpensItsVideo() {
        open()
        field().performClick()
        field().performTextInput("pa")
        compose.settledUntil(5_000) { compose.onAllNodesWithTag("suggestion").fetchSemanticsNodes().size == 4 }
        for ((text, kind) in listOf("patience" to "Recent", "patience in hardship" to "Word", "On Patience" to "Video", "Shaykh Ahmad" to "Speaker")) {
            compose.onNode(hasTestTag("suggestion") and hasText(text) and hasText(kind)).assertExists()
        }
        val before = searchBodies().size
        compose.onNode(hasTestTag("suggestion") and hasText("Video")).performClick()
        compose.onNodeWithText("video v1").assertIsDisplayed()
        assertEquals("a title is opened, not searched", before, searchBodies().size)
    }

    @Test
    fun didYouMeanRunsTheCorrectedWords() {
        open()
        search("patiance")
        waitForTag("search-did-you-mean")
        compose.onNodeWithText("Did you mean", substring = true).assertIsDisplayed()
        compose.onNodeWithTag("search-did-you-mean-run").performClick()
        compose.settledUntil(5_000) { searchBodies().size == 2 }
        assertEquals("patience", searchBodies().last()["q"]!!.jsonPrimitive.content)
        compose.settledUntil(5_000) { !tagged("search-did-you-mean") }
    }

    @Test
    fun theSortMenuRunsTheSearchInTheOrderChosen() {
        open()
        search("patience")
        compose.onNodeWithText("Sort by: Relevance").assertIsDisplayed()
        compose.onNodeWithTag("search-sort").performClick()
        compose.onNodeWithTag("search-sort-newest").performClick()
        compose.settledUntil(5_000) { searchBodies().size == 2 }
        assertEquals("newest", searchBodies().last()["sort"]!!.jsonPrimitive.content)
        waitFor("Sort by: Newest")
    }

    @Test
    fun facetsNarrowTheSearchAndTheFiltersInUseComeOff() {
        open()
        search("patience")
        compose.onNodeWithTag("search-filters").performClick()
        waitForTag("search-facets")
        // each bucket with its count, read as "name, N videos"
        compose.onNode(hasContentDescription("Talks, 1 video")).assertExists()
        compose.onNode(hasContentDescription("English, 3 videos")).assertExists()
        compose.onNode(hasContentDescription("Over an hour, 2 videos")).assertExists()
        compose.onNode(hasContentDescription("2024, 3 videos")).assertExists()

        compose.onNodeWithTag("facet-library-l2").performClick()
        compose.settledUntil(5_000) { searchBodies().size == 2 }
        compose.onNodeWithTag("facet-speaker-Shaykh Ahmad").performClick()
        compose.settledUntil(5_000) { searchBodies().size == 3 }
        val filters = searchBodies().last()["filters"]!!.jsonObject
        assertEquals("[\"l2\"]", filters["library_id"].toString())
        assertEquals("[\"Shaykh Ahmad\"]", filters["speaker"].toString())

        compose.onNodeWithTag("facets-done").performClick()
        compose.settledUntil(5_000) { !tagged("search-facets") }
        compose.onNode(hasContentDescription("Remove Library: Talks")).assertIsDisplayed()
        compose.onNode(hasContentDescription("Filters, 2 in use")).assertIsDisplayed()

        compose.onNodeWithTag("active-library-l2").performClick()
        compose.settledUntil(5_000) { searchBodies().size == 4 }
        assertEquals(null, searchBodies().last()["filters"]!!.jsonObject["library_id"])
        compose.onNodeWithTag("search-filters-clear").performClick()
        compose.settledUntil(5_000) { searchBodies().size == 5 }
        assertEquals(null, searchBodies().last()["filters"])
        compose.settledUntil(5_000) { !tagged("search-active-filters") }
    }

    @Test
    fun anOlderHomeShowsTheOrderButNoFilters() {
        SearchFixtures.install(home) { SearchFixtures.results(SearchFixtures.EN, help = false) }
        compose.setShell(home, sections = PlayerSection.enabled, screens = screens)
        compose.onNodeWithTag("section-Search").performClick()
        search("patience")
        compose.onNodeWithTag("search-sort").assertIsDisplayed()
        assertTrue(!tagged("search-filters"))
        assertTrue(!tagged("search-did-you-mean"))
        compose.onNode(hasTestTag("title-v1")).assertIsDisplayed()
    }

    @Test
    @Config(qualifiers = "ar-w411dp-h891dp")
    fun theHelpIsInArabic() {
        val l = SearchFixtures.AR
        open(l)
        search(l.misspelt)
        compose.onNodeWithText("الترتيب حسب: الأقرب صلة").assertIsDisplayed()
        compose.onNodeWithText("التصفية").assertIsDisplayed()
        compose.onNodeWithText("هل تقصد", substring = true).assertIsDisplayed()
        compose.onNodeWithTag("search-filters").performClick()
        waitForTag("search-facets")
        compose.onNodeWithText("تضييق النتائج").assertIsDisplayed()
        compose.onNodeWithText("المتحدث").assertIsDisplayed()
        compose.onNode(hasContentDescription("أكثر من ساعة، مقطعان")).assertExists()
        compose.onNode(hasContentDescription("الإنجليزية", substring = true)).assertExists()
    }

    @Test
    fun aFilterThatLeavesNothingCanBeTakenOffThere() {
        // narrowed to the second library, the home finds nothing (and sends no facets)
        SearchFixtures.install(home) { r ->
            val f = SearchFixtures.body(r)["filters"]
            if (f != null && f.toString().contains(SearchFixtures.OTHER_LIB)) SearchFixtures.EMPTY else SearchFixtures.answering(SearchFixtures.EN)(r)
        }
        compose.setShell(home, sections = PlayerSection.enabled, screens = screens)
        compose.onNodeWithTag("section-Search").performClick()
        search("patience")
        compose.onNodeWithTag("search-filters").performClick()
        waitForTag("search-facets")
        compose.onNodeWithTag("facet-library-${SearchFixtures.OTHER_LIB}").performClick()
        compose.settledUntil(5_000) { searchBodies().size == 2 }
        compose.onNodeWithTag("facets-done").performClick()
        waitForTag("search-none")
        compose.onNode(hasContentDescription("Remove Library: Talks")).assertIsDisplayed()
        compose.onNodeWithTag("active-library-${SearchFixtures.OTHER_LIB}").performClick()
        compose.settledUntil(5_000) { searchBodies().size == 3 }
        assertEquals(null, searchBodies().last()["filters"])
        waitForTag("search-results")
    }

    // ---------- story 49.14: AI mode's tab ----------

    private fun aiBodies() = home.requests.filter { it.method == "POST" && it.url.encodedPath == "/api/search/ai" }
        .map { Json.parseToJsonElement(it.body!!.utf8()).jsonObject }

    private fun stream(body: String, firstBytes: Long? = null): MockResponse {
        val b = MockResponse.Builder().code(200).setHeader("Content-Type", "text/event-stream").body(body)
        // the sources at once, the sentences seconds later: what is shown before they come can be seen
        if (firstBytes != null) b.throttleBody(firstBytes, 3, TimeUnit.SECONDS)
        return b.build()
    }

    private fun answerAi(l: SearchFixtures.Lang = SearchFixtures.EN, slow: Boolean = false) {
        val (passages, sentences) = if (l == SearchFixtures.AR) AiStreams.AR_PASSAGES to AiStreams.AR_SENTENCES else AiStreams.EN_PASSAGES to AiStreams.EN_SENTENCES
        home.answer("/api/search/ai") { r ->
            val q = SearchFixtures.body(r)["q"]!!.jsonPrimitive.content
            val first = AiStreams.PING + AiStreams.sources(q, passages)
            stream(AiStreams.answer(q, passages, sentences), if (slow) first.toByteArray().size.toLong() else null)
        }
    }

    private fun openAi() {
        compose.onNodeWithTag("tab-ai").performClick()
        waitForTag("ai")
    }

    @Test
    fun resultsIsTheDefaultAndAiModeAsksOnlyWhenOpened() {
        answerAi()
        open()
        search("patience")
        compose.onNodeWithTag("tab-results").assertIsSelected()
        assertTrue("Results never asks AI mode", aiBodies().isEmpty())
        openAi()
        waitForTag("ai-sentence-1-1")
        assertEquals(listOf("patience"), aiBodies().map { it["q"]!!.jsonPrimitive.content })
        // back and forth on the same search asks nothing more
        compose.onNodeWithTag("tab-results").performClick()
        waitForTag("search-results")
        openAi()
        waitForTag("ai-sentence-1-1")
        assertEquals(1, aiBodies().size)
    }

    @Test
    fun aiModeShowsTheSourcesFirstThenEachSentence() {
        answerAi(slow = true)
        open()
        search("patience")
        openAi()
        waitForTag("ai-passage-1-1")
        compose.onNodeWithText("Passages read").assertIsDisplayed()
        compose.onNodeWithTag("ai-passage-1-2").assertIsDisplayed()
        assertTrue("no sentence before the sources", compose.onAllNodesWithTag("ai-sentence-1-0").fetchSemanticsNodes().isEmpty())
        compose.onNodeWithText("Writing the answer…").assertIsDisplayed()
        compose.settledUntil(15_000) { compose.onAllNodesWithTag("ai-sentence-1-1").fetchSemanticsNodes().isNotEmpty() }
        compose.onNodeWithText("Patience is half of faith.").assertIsDisplayed()
        compose.onNodeWithText("Its reward comes without measure.").assertIsDisplayed()
    }

    @Test
    fun aCitationOpensThePlayerAtItsMoment() {
        answerAi()
        open()
        search("patience")
        openAi()
        waitForTag("ai-cite-1-1-2")
        compose.onNode(hasContentDescription("Source 2, The Night Journey, play from 22:02")).assertExists()
        compose.onNodeWithTag("ai-cite-1-1-2").performClick()
        compose.onNodeWithText("watch v4 at 1322").assertIsDisplayed()
    }

    @Test
    fun aFollowUpCarriesTheEarlierQuestionAndAnswer() {
        answerAi()
        open()
        search("patience")
        openAi()
        waitForTag("ai-sentence-1-1")
        compose.onNodeWithTag("ai-follow-up").performTextInput("and its reward?")
        compose.onNodeWithTag("ai-follow-up").performImeAction()
        compose.settledUntil(5_000) { aiBodies().size == 2 }
        val follow = aiBodies().last()
        assertEquals("and its reward?", follow["q"]!!.jsonPrimitive.content)
        val turn = follow["history"]!!.jsonArray.single().jsonObject
        assertEquals("patience", turn["q"]!!.jsonPrimitive.content)
        assertEquals("Patience is half of faith. Its reward comes without measure.", turn["answer"]!!.jsonPrimitive.content)
        waitForTag("ai-sentence-2-1")
        compose.onNodeWithTag("ai-question-2").assertExists()
    }

    @Test
    fun passagesThatDoNotAnswerSaySoAndStayShown() {
        home.answer("/api/search/ai") { stream(AiStreams.noAnswer("patience", AiStreams.EN_PASSAGES)) }
        open()
        search("patience")
        openAi()
        waitFor("These passages do not answer the question.")
        compose.onNodeWithTag("ai-passage-1-1").assertExists()
    }

    @Test
    fun anErrorKeepsWhatWasShownAndSaysTheAnswerStopped() {
        home.answer("/api/search/ai") {
            stream(AiStreams.sources("patience", AiStreams.EN_PASSAGES) + AiStreams.text(AiStreams.EN_SENTENCES[0], AiStreams.EN_PASSAGES) + AiStreams.error("model-failed"))
        }
        open()
        search("patience")
        openAi()
        waitFor("The answer stopped. What was written stands.")
        compose.onNodeWithText("Patience is half of faith.").assertIsDisplayed()
        compose.onNodeWithTag("ai-again").assertIsDisplayed()
    }

    @Test
    fun noLanguageModelIsTheTabsMessageWithItsHelp() {
        home.answer("/api/search/ai") { json(AiStreams.UNAVAILABLE, 409) }
        open()
        search("patience")
        openAi()
        waitForTag("ai-unavailable")
        compose.onNodeWithText("AI mode is not set up").assertIsDisplayed()
        compose.onNodeWithText("Settings → Rules → Tag check", substring = true).assertIsDisplayed()
        compose.onNodeWithTag("ai-help").assertIsDisplayed()
        // the results are where they were
        compose.onNodeWithTag("tab-results").performClick()
        waitForTag("search-results")
    }

    @Test
    fun offBusyAndAnOlderHomeEachSayWhy() {
        home.answer("/api/search/ai") { json(AiStreams.OFF, 409) }
        open()
        search("patience")
        openAi()
        waitFor("An admin turned AI mode off for this home.")
        home.answer("/api/search/ai") { json(AiStreams.BUSY, 429) }
        compose.onNodeWithTag("ai-follow-up").performTextInput("again")
        compose.onNodeWithTag("ai-follow-up").performImeAction()
        waitFor("AI mode is busy answering other questions. Ask again shortly.")
        // a home before story 49.12: no such route
        home.answer("/api/search/ai") { MockResponse.Builder().code(404).build() }
        compose.onNodeWithTag("ai-follow-up").performTextInput("once more")
        compose.onNodeWithTag("ai-follow-up").performImeAction()
        waitFor("This home does not have AI mode yet.")
    }

    @Test
    fun leavingTheTabStopsTheAnswer() {
        answerAi(slow = true)
        open()
        search("patience")
        openAi()
        waitForTag("ai-passage-1-1")
        compose.onNodeWithTag("tab-results").performClick()
        waitForTag("search-results")
        openAi()
        waitFor("The answer stopped. What was written stands.")
        assertTrue(compose.onAllNodesWithTag("ai-sentence-1-0").fetchSemanticsNodes().isEmpty())
        assertEquals(1, aiBodies().size)
    }

    @Test
    @Config(qualifiers = "ar-w411dp-h891dp")
    fun aiModeIsInArabic() {
        val l = SearchFixtures.AR
        answerAi(l)
        open(l)
        search(l.query)
        compose.onNodeWithText("وضع الذكاء الاصطناعي").performClick()
        waitForTag("ai-sentence-1-1")
        assertEquals(l.query, aiBodies().single()["q"]!!.jsonPrimitive.content)
        compose.onNodeWithText("الصبر نصف الإيمان.").assertIsDisplayed()
        compose.onNodeWithText("المقاطع المقروءة").assertIsDisplayed()
        // TalkBack reads each citation's video and moment (the digits are the locale's)
        compose.onNode(hasTestTag("ai-cite-1-0-1") and hasContentDescription("، الإسراء والمعراج، تشغيل من ", substring = true)).assertExists()
    }
}
