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
import androidx.compose.ui.test.junit4.createAndroidComposeRule
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
import com.hamzalabs.maktaba.player.harness.FakeHome
import com.hamzalabs.maktaba.player.harness.FakeHome.Companion.json
import com.hamzalabs.maktaba.player.harness.setShell
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
        compose.waitUntil(5_000) { compose.onAllNodes(hasText(text, substring = true)).fetchSemanticsNodes().isNotEmpty() }

    private fun waitForTag(tag: String) =
        compose.waitUntil(5_000) { compose.onAllNodesWithTag(tag).fetchSemanticsNodes().isNotEmpty() }

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
        compose.waitUntil(5_000) { compose.onAllNodesWithTag("suggestion").fetchSemanticsNodes().isNotEmpty() }
        compose.onNode(hasTestTag("suggestion") and hasText("patience in hardship", substring = true)).assertIsDisplayed()
        val asked = home.requests.last { it.url.encodedPath == "/api/search/suggest" }
        assertEquals("pati", asked.url.queryParameter("q"))

        compose.onNodeWithTag("suggestion").performClick()
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
        compose.waitUntil(5_000) { home.requests.any { it.method == "DELETE" && it.url.encodedPath == "/api/search/recent/r1" } }
        compose.waitUntil(5_000) { compose.onAllNodes(hasText("the night journey")).fetchSemanticsNodes().isEmpty() }

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
        compose.waitUntil(5_000) { home.requests.any { it.method == "DELETE" && it.url.encodedPath == "/api/search/recent" } }
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
}
