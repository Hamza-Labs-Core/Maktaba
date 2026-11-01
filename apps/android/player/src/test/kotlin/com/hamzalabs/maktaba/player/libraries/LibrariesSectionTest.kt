package com.hamzalabs.maktaba.player.libraries

import androidx.activity.ComponentActivity
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.getUnclippedBoundsInRoot
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.player.harness.FakeHome
import com.hamzalabs.maktaba.player.shell.ShellNavigation
import com.hamzalabs.maktaba.player.shell.ShellNavigationKey
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config

/**
 * The Libraries section on a phone (story 48.27, FR-14 to FR-17), in the shell as it ships,
 * against the fake home: the acceptance of 48.18 on Android.
 */
@RunWith(AndroidJUnit4::class)
@Config(qualifiers = "w411dp-h891dp")
class LibrariesSectionTest {
    @get:Rule(order = 0) val home = FakeHome()
    @get:Rule(order = 1) val compose = createAndroidComposeRule<ComponentActivity>()

    @Before fun routes() = home.answerLibraries()

    private fun waitForTag(tag: String) =
        compose.waitUntil(5_000) { compose.onAllNodes(hasTestTag(tag)).fetchSemanticsNodes().isNotEmpty() }

    private fun waitForText(text: String) =
        compose.waitUntil(5_000) { compose.onAllNodes(hasText(text)).fetchSemanticsNodes().isNotEmpty() }

    private fun waitForRequest(what: String, test: (mockwebserver3.RecordedRequest) -> Boolean) =
        compose.waitUntil(5_000) { home.requests.any(test) }.also { assertTrue(what, home.requests.any(test)) }

    private fun back() = compose.activityRule.scenario.onActivity { it.onBackPressedDispatcher.onBackPressed() }

    private fun openLibrary(id: String = "l1") {
        compose.setLibraries(home)
        waitForTag("library-$id")
        compose.onNodeWithTag("library-$id").performClick()
        waitForTag("library-page")
    }

    private fun openSeries() {
        compose.setLibraries(home)
        waitForTag("library-l1")
        compose.onNodeWithTag("tab-series").performClick()
        waitForTag("series-s1")
        compose.onNodeWithTag("series-s1").performClick()
        waitForTag("continue")
    }

    // ---------- Libraries (FR-14) ----------

    @Test
    fun theShippedShellOpensOnTheLibrariesWithTheirCounts() {
        compose.setLibraries(home)
        waitForTag("library-l1")
        compose.onNodeWithText("Films").assertIsDisplayed()
        compose.onNodeWithText("5 videos").assertIsDisplayed()
        compose.onNodeWithText("Lessons").assertIsDisplayed()
        // a single section offers no navigation
        compose.onNode(SemanticsMatcher.expectValue(ShellNavigationKey, ShellNavigation.None)).assertExists()
        assertTrue(home.paths.toString(), "/api/libraries/summary" in home.paths)
    }

    @Test
    fun pinMovesALibraryFirstAndCallsPut() {
        compose.setLibraries(home)
        waitForTag("library-l2")
        assertTrue("Films leads before the pin", first("library-l1", "library-l2"))

        compose.onNodeWithTag("pin-l2").performClick()

        waitForRequest("a PUT of the pins") { it.method == "PUT" && it.url.encodedPath == "/api/me/pinned-libraries" }
        val put = home.requests.last { it.method == "PUT" }
        assertEquals(listOf("l2"), put.ids("library_ids"))
        compose.waitUntil(5_000) { first("library-l2", "library-l1") }
        compose.onNodeWithText("3 videos · Pinned").assertIsDisplayed()
        compose.onNode(SemanticsMatcher.expectValue(SemanticsProperties.ContentDescription, listOf("Unpin Lessons"))).assertExists()

        // and Unpin puts it back
        compose.onNodeWithTag("pin-l2").performClick()
        compose.waitUntil(5_000) { home.requests.count { it.method == "PUT" } == 2 }
        assertEquals(emptyList<String>(), home.requests.last { it.method == "PUT" }.ids("library_ids"))
        compose.waitUntil(5_000) { first("library-l1", "library-l2") }
    }

    /** True when [a] comes before [b] in reading order (above, or beside it on the same row). */
    private fun first(a: String, b: String): Boolean {
        val ra = compose.onNodeWithTag(a).getUnclippedBoundsInRoot()
        val rb = compose.onNodeWithTag(b).getUnclippedBoundsInRoot()
        return ra.top < rb.top || (ra.top == rb.top && ra.left < rb.left)
    }

    // ---------- a library (FR-15) ----------

    @Test
    fun aLibraryShowsItsHubRowsAndLeavesTheEmptyOnesOut() {
        openLibrary()
        waitForTag("row-continue")
        compose.onNodeWithTag("row-continue").assertExists()
        compose.onNodeWithTag("row-recently-added").assertExists()
        compose.onAllNodesWithTag("row-on-deck").assertCountEquals(0)
        compose.onAllNodesWithTag("row-recently-watched").assertCountEquals(0)
    }

    @Test
    fun theGridPagesByCursorToTheEnd() {
        openLibrary()
        compose.scrollUntil("library-grid", "video-v5")
        compose.onNodeWithTag("video-v5").assertExists()
        val pages = home.requests.filter { it.url.encodedPath == "/api/videos" }
        val cursors = pages.map { it.url.queryParameter("cursor") }
        assertEquals("first page, then c2, then c3, then nothing more", listOf(null, "c2", "c3"), cursors)
        assertTrue(pages.all { it.url.queryParameter("library") == "l1" && it.url.queryParameter("sort") == "created_at" })
    }

    /** Opens the chip [tag]'s menu, picks [option], and waits for the menu to close. */
    private fun choose(tag: String, option: String) {
        compose.onNodeWithTag(tag).performClick()
        waitForText(option)
        compose.onNodeWithText(option).performClick()
        compose.waitUntil(5_000) { compose.onAllNodes(hasText(option)).fetchSemanticsNodes().isEmpty() }
    }

    @Test
    fun facetsAndSortChangeTheRequestsQuery() {
        openLibrary()
        waitForTag("filter-language")

        choose("filter-sort", "A–Z")
        waitForRequest("sort A to Z") { it.url.queryParameter("sort") == "title" && it.url.queryParameter("order") == "asc" }

        choose("filter-language", "Arabic")
        waitForRequest("language ar") { it.url.queryParameter("language") == "ar" && it.url.queryParameter("sort") == "title" }

        choose("filter-speaker", "Mustafa Akkad")
        waitForRequest("speaker") { it.url.queryParameter("speaker") == "Mustafa Akkad" && it.url.queryParameter("language") == "ar" }

        // Any language takes the facet off again
        choose("filter-language", "Any language")
        compose.waitUntil(5_000) {
            home.requests.last { it.url.encodedPath == "/api/videos" }.url.queryParameter("language") == null
        }
    }

    @Test
    fun aVideoOpensItsPageAndBackReturnsToTheLibrary() {
        openLibrary()
        waitForTag("video-v1")
        compose.onNodeWithTag("video-v1").performClick()
        waitForText("A film about The Message.")
        back()
        compose.waitUntil(5_000) { compose.onAllNodes(hasText("A film about The Message.")).fetchSemanticsNodes().isEmpty() }
        compose.onNodeWithTag("library-page").assertIsDisplayed()
        // and Back again: the libraries
        back()
        waitForTag("libraries-grid")
    }

    @Test
    fun aLibraryTheCallerMayNotSeeSaysNotAvailable() {
        home.answer("/api/libraries/summary") { FakeHome.json("""{"items":[{"id":"l9","name":"Elsewhere","videos":1}]}""") }
        openLibrary("l9")
        waitForText("Not available")
        compose.onNodeWithText("Not available").assertIsDisplayed()
    }

    @Test
    fun aLibrarysSeriesTabListsItsSeries() {
        openLibrary()
        waitForTag("row-continue")
        compose.onNodeWithTag("library-tab-series").performClick()
        waitForTag("series-s1")
        assertTrue(home.paths.toString(), "/api/libraries/l1/series" in home.paths)
        compose.onNodeWithTag("series-s1").performClick()
        waitForTag("series-page")
    }

    // ---------- Series and a series (FR-16, FR-17) ----------

    @Test
    fun seriesListsEverySeriesWithProgress() {
        compose.setLibraries(home)
        waitForTag("library-l1")
        compose.onNodeWithTag("tab-series").performClick()
        waitForTag("series-s1")
        compose.onNode(SemanticsMatcher.expectValue(SemanticsProperties.ContentDescription, listOf("Omar, 1 of 4 watched, 25% watched"))).assertExists()
    }

    @Test
    fun aSeriesContinueOpensItsNextEpisode() {
        openSeries()
        compose.onNodeWithTag("continue").performClick()
        // the video's page (48.28) for the next episode (v3), pushed on the stack
        waitForText("A film about Lion of the Desert.")
        assertTrue(home.paths.toString(), "/api/videos/v3" in home.paths)
    }

    @Test
    fun markWatchedOnASeasonMarksEachEpisode() {
        openSeries()
        // Continue's season is chosen: season 1 (v2 watched, v3 not)
        compose.onNodeWithTag("episode-v2").assertExists()
        compose.onNodeWithTag("mark-season").performClick()
        waitForRequest("a POST of the season") { it.method == "POST" && it.url.encodedPath == "/api/me/watched" }
        val post = home.requests.last { it.method == "POST" }
        assertEquals(listOf("v2", "v3"), post.ids("video_ids"))
        assertTrue(post.bodyText(), post.bodyText().contains("\"watched\":true"))
        waitForText("Mark the season not watched")

        // season 2, one episode
        compose.onNodeWithTag("season-2").performClick()
        waitForTag("episode-v4")
        compose.onNodeWithTag("mark-v4").performClick()
        compose.waitUntil(5_000) { home.requests.count { it.method == "POST" } == 2 }
        assertEquals(listOf("v4"), home.requests.last { it.method == "POST" }.ids("video_ids"))
        compose.onNode(SemanticsMatcher.expectValue(SemanticsProperties.ContentDescription, listOf("Mark not watched, The Night Journey"))).assertExists()
    }

    // ---------- accessibility (NFR-6) ----------

    @Test
    fun everyControlHasWordsForTalkBack() {
        openSeries()
        assertEveryClickableIsNamed()
        back()
        waitForTag("series-grid")
        assertEveryClickableIsNamed()
        compose.onNodeWithTag("tab-libraries").performClick()
        waitForTag("libraries-grid")
        assertEveryClickableIsNamed()
    }

    private fun assertEveryClickableIsNamed() {
        compose.waitForIdle()
        val unnamed = compose.onAllNodes(SemanticsMatcher.keyIsDefined(SemanticsActions.OnClick), useUnmergedTree = false)
            .fetchSemanticsNodes()
            .filter { n ->
                val c = n.config
                c.getOrNull(SemanticsProperties.ContentDescription).isNullOrEmpty() &&
                    c.getOrNull(SemanticsProperties.Text).isNullOrEmpty() &&
                    n.children.none { ch -> !ch.config.getOrNull(SemanticsProperties.ContentDescription).isNullOrEmpty() || !ch.config.getOrNull(SemanticsProperties.Text).isNullOrEmpty() }
            }
        assertTrue("controls without words: ${unnamed.map { it.config }}", unnamed.isEmpty())
    }
}

