package com.hamzalabs.maktaba.player.libraries

import androidx.activity.ComponentActivity
import androidx.compose.material3.adaptive.WindowAdaptiveInfo
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assertIsNotSelected
import androidx.compose.ui.test.assertIsSelected
import androidx.compose.ui.test.getUnclippedBoundsInRoot
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performImeAction
import androidx.compose.ui.test.performSemanticsAction
import androidx.compose.ui.test.performTextInput
import androidx.compose.ui.unit.DpRect
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.player.harness.FakeHome
import com.hamzalabs.maktaba.player.harness.ListScreens
import com.hamzalabs.maktaba.player.harness.Window
import com.hamzalabs.maktaba.player.harness.acrossHinge
import com.hamzalabs.maktaba.player.harness.folded
import com.hamzalabs.maktaba.player.harness.setShell
import com.hamzalabs.maktaba.player.harness.settled
import com.hamzalabs.maktaba.player.harness.settledUntil
import com.hamzalabs.maktaba.player.harness.uprightHinge
import com.hamzalabs.maktaba.player.home.HomeFixtures
import com.hamzalabs.maktaba.player.home.KEY_CONTINUE
import com.hamzalabs.maktaba.player.home.KEY_WATCHED
import com.hamzalabs.maktaba.player.home.setHome
import com.hamzalabs.maktaba.player.search.SearchFixtures
import com.hamzalabs.maktaba.player.shell.CollapsedRailWidth
import com.hamzalabs.maktaba.player.shell.PlayerSection
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
 * The shell and Libraries half folded (FR-8), with the hinge faked through Material's adaptive
 * info (the JVM cannot fold a window). Nothing sits under the hinge:
 * - like a book: the collapsed rail and the list on the start page (the list ends at the
 *   hinge), the page on the other; the expanded rail stays on the start page;
 * - on a table: the page on the upright top half, the list and the bar on the flat bottom one;
 *   a screen that does not split itself keeps to the top half;
 * - Home like a book: the hero and the first rows on the start page, the rest on the other;
 * - Search like a book: the field and the results on the start page, the chosen result's page
 *   on the other (the first result until one is chosen).
 * Right to left (Arabic) the start page is the right one.
 */
@RunWith(AndroidJUnit4::class)
class PostureLayoutTest {
    @get:Rule(order = 0) val home = FakeHome()
    @get:Rule(order = 1) val compose = createAndroidComposeRule<ComponentActivity>()

    @Before fun routes() = home.answerLibraries()

    private fun exists(tag: String) = compose.onAllNodes(hasTestTag(tag)).fetchSemanticsNodes().isNotEmpty()
    private fun bounds(tag: String) = compose.onNodeWithTag(tag).getUnclippedBoundsInRoot()
    private val root get() = compose.onRoot().getUnclippedBoundsInRoot()
    private val widthPx get() = compose.activity.resources.displayMetrics.widthPixels
    private val heightPx get() = compose.activity.resources.displayMetrics.heightPixels

    /** The hinge, from window px to the root's dp (the root may not start at the window's corner). */
    private fun inRoot(hinge: Rect): DpRect {
        val density = compose.activity.resources.displayMetrics.density
        val origin = compose.onRoot().fetchSemanticsNode().positionInWindow
        return DpRect(
            ((hinge.left - origin.x) / density).dp, ((hinge.top - origin.y) / density).dp,
            ((hinge.right - origin.x) / density).dp, ((hinge.bottom - origin.y) / density).dp,
        )
    }

    private fun libraries(window: Window, info: WindowAdaptiveInfo) {
        compose.setLibraries(home, window, sections = PlayerSection.entries, info = info)
        compose.settled(home, "the shell") { exists("section-Libraries") }
        compose.onNodeWithTag("section-Libraries").performSemanticsAction(SemanticsActions.OnClick)
        compose.settled(home, "the list and the page") { exists("libraries-list") && exists("libraries-detail") }
    }

    private fun assertNavigation(expected: ShellNavigation) =
        compose.onNode(SemanticsMatcher.expectValue(ShellNavigationKey, expected)).assertExists()

    // ---------- like a book ----------

    @Test @Config(qualifiers = "w841dp-h673dp-xhdpi")
    fun likeABookTheRailAndTheListAreOnTheStartPageAndThePageOnTheOther() {
        val hingePx = uprightHinge(widthPx, heightPx)
        libraries(Window.FoldableWide, Window.FoldableWide.folded(hingePx, vertical = true))
        val hinge = inRoot(hingePx)
        assertNavigation(ShellNavigation.CollapsibleRail)

        val rail = bounds("shell-rail")
        val list = bounds("libraries-list")
        val detail = bounds("libraries-detail")
        assertTrue("the rail is on the start page: $rail, hinge $hinge", rail.right <= hinge.left)
        assertEquals("the list starts after the rail", CollapsedRailWidth.value, list.left.value, 1f)
        assertEquals("the list ends at the hinge", hinge.left.value, list.right.value, 1f)
        assertEquals("the page starts after the hinge", hinge.right.value, detail.left.value, 1f)
        assertEquals("the page fills the other page", root.right.value, detail.right.value, 1f)
        // the hinge is the split: no divider to drag onto it
        assertTrue(!exists("split-handle"))

        // the expanded rail lies over the start page only
        compose.onNodeWithTag("rail-toggle").performClick()
        compose.settledUntil(5_000) { exists("rail-scrim") }
        compose.waitForIdle()
        val open = bounds("shell-rail")
        assertTrue("expanded: $open", open.right > CollapsedRailWidth + 50.dp)
        assertTrue("never across the hinge: $open, hinge $hinge", open.right <= hinge.left + 0.5.dp)
    }

    @Test @Config(qualifiers = "ar-w841dp-h673dp-xhdpi")
    fun likeABookRightToLeftTheStartPageIsTheRightOne() {
        val hingePx = uprightHinge(widthPx, heightPx)
        libraries(Window.FoldableWide, Window.FoldableWide.folded(hingePx, vertical = true))
        val hinge = inRoot(hingePx)
        val rail = bounds("shell-rail")
        val list = bounds("libraries-list")
        val detail = bounds("libraries-detail")
        assertTrue("the rail is right of the hinge: $rail", rail.left >= hinge.right)
        assertEquals("the list ends at the rail", (root.right - CollapsedRailWidth).value, list.right.value, 1f)
        assertEquals("the list starts at the hinge", hinge.right.value, list.left.value, 1f)
        assertEquals("the page ends at the hinge", hinge.left.value, detail.right.value, 1f)
        assertEquals(0f, detail.left.value, 1f)

        compose.onNodeWithTag("rail-toggle").performClick()
        compose.settledUntil(5_000) { exists("rail-scrim") }
        compose.waitForIdle()
        assertTrue("never across the hinge", bounds("shell-rail").left >= hinge.right - 0.5.dp)
    }

    // ---------- Home like a book ----------

    private fun homeBook(): DpRect {
        HomeFixtures.install(home)
        compose.registerIdlingResource(home.idling)
        val hingePx = uprightHinge(widthPx, heightPx)
        compose.setHome(home, Window.FoldableWide, info = Window.FoldableWide.folded(hingePx, vertical = true))
        // every answer in (the recommendations too): the rows are where they stay
        compose.settled(home, "both pages of rows") { exists("hero") && exists("home-rows-end") && exists("row-$KEY_WATCHED") }
        compose.waitForIdle()
        assertNavigation(ShellNavigation.CollapsibleRail)
        return inRoot(hingePx)
    }

    @Test @Config(qualifiers = "w841dp-h673dp-xhdpi")
    fun homeLikeABookHasRowsOnBothPagesAndNoneUnderTheHinge() {
        val hinge = homeBook()
        val start = bounds("home-rows")
        val end = bounds("home-rows-end")
        assertEquals("the start page's rows start after the rail", CollapsedRailWidth.value, start.left.value, 1f)
        assertEquals("they end at the hinge", hinge.left.value, start.right.value, 1f)
        assertEquals("the other page's rows start after the hinge", hinge.right.value, end.left.value, 1f)
        assertEquals("they fill the other page", root.right.value, end.right.value, 1f)
        // the hero and the first rows on the start page, the rest on the other, in Home's order
        assertTrue("the hero is on the start page", bounds("hero").right <= hinge.left + 0.5.dp)
        assertTrue("Continue Watching is on the start page", bounds("row-$KEY_CONTINUE").right <= hinge.left + 0.5.dp)
        assertTrue("Recently Watched is on the other page", bounds("row-$KEY_WATCHED").left >= hinge.right - 0.5.dp)
    }

    @Test @Config(qualifiers = "ar-w841dp-h673dp-xhdpi")
    fun homeLikeABookRightToLeftTheStartPageIsTheRightOne() {
        val hinge = homeBook()
        val start = bounds("home-rows")
        val end = bounds("home-rows-end")
        assertEquals("the start page's rows end at the rail", (root.right - CollapsedRailWidth).value, start.right.value, 1f)
        assertEquals("they start at the hinge", hinge.right.value, start.left.value, 1f)
        assertEquals("the other page's rows end at the hinge", hinge.left.value, end.right.value, 1f)
        assertEquals(0f, end.left.value, 1f)
        assertTrue("the hero is on the start page", bounds("hero").left >= hinge.right - 0.5.dp)
        assertTrue("Recently Watched is on the other page", bounds("row-$KEY_WATCHED").right <= hinge.left + 0.5.dp)
    }

    // ---------- Search like a book ----------

    private fun searchBook(): DpRect {
        SearchFixtures.install(home, SearchFixtures.EN)
        val hingePx = uprightHinge(widthPx, heightPx)
        compose.setShell(home, sections = PlayerSection.enabled, window = Window.FoldableWide, info = Window.FoldableWide.folded(hingePx, vertical = true))
        compose.settled(home, "the shell") { exists("section-Search") }
        compose.onNodeWithTag("section-Search").performSemanticsAction(SemanticsActions.OnClick)
        compose.settled(home, "Search and the page beside it") { exists("search") && exists("search-preview-hint") }
        return inRoot(hingePx)
    }

    private fun searchFor(q: String) {
        val field = compose.onNode(hasSetTextAction())
        field.performClick()
        field.performTextInput(q)
        field.performImeAction()
        compose.settled(home, "the results and the first one's page") { exists("title-v1") && exists("video-details") }
    }

    @Test @Config(qualifiers = "w841dp-h673dp-xhdpi")
    fun searchLikeABookShowsTheChosenResultOnTheOtherPage() {
        val hinge = searchBook()
        assertNavigation(ShellNavigation.CollapsibleRail)
        val search = bounds("search")
        val preview = bounds("search-preview")
        assertEquals("the field and the results start after the rail", CollapsedRailWidth.value, search.left.value, 1f)
        assertTrue("and end at the hinge: $search, hinge $hinge", search.right <= hinge.left + 0.5.dp)
        assertEquals("the page starts after the hinge", hinge.right.value, preview.left.value, 1f)
        assertEquals("the page fills the other page", root.right.value, preview.right.value, 1f)

        // the first result's page until one is chosen; a tap shows the chosen one there, nothing is pushed
        searchFor(SearchFixtures.EN.query)
        assertTrue("the results are on the start page", bounds("search-results").right <= hinge.left + 0.5.dp)
        compose.onNodeWithTag("title-v1").assertIsSelected()
        // the page's heading: the poster shows the title too once its picture is found missing
        compose.onNodeWithTag("video-title").assertTextEquals("The Message")
        compose.onNodeWithTag("title-v2").performClick()
        compose.settled(home, "the second result's page") { compose.onAllNodes(hasText("Omar, Episode 1")).fetchSemanticsNodes().isNotEmpty() }
        compose.onNodeWithTag("title-v2").assertIsSelected()
        compose.onNodeWithTag("title-v1").assertIsNotSelected()
        assertTrue("the page is on the other page", bounds("video-details").left >= hinge.right - 0.5.dp)
        assertTrue("Search is still the screen", exists("search-results"))
    }

    @Test @Config(qualifiers = "ar-w841dp-h673dp-xhdpi")
    fun searchLikeABookRightToLeftTheResultsAreOnTheRightPage() {
        val hinge = searchBook()
        val search = bounds("search")
        val preview = bounds("search-preview")
        assertEquals("the field and the results end at the rail", (root.right - CollapsedRailWidth).value, search.right.value, 1f)
        assertTrue("and start at the hinge", search.left >= hinge.right - 0.5.dp)
        assertEquals("the page ends at the hinge", hinge.left.value, preview.right.value, 1f)
        assertEquals(0f, preview.left.value, 1f)
        searchFor(SearchFixtures.EN.query)
        assertTrue("the results are right of the hinge", bounds("search-results").left >= hinge.right - 0.5.dp)
        assertTrue("the page is left of it", bounds("video-details").right <= hinge.left + 0.5.dp)
    }

    // ---------- on a table ----------

    @Test @Config(qualifiers = "w673dp-h841dp-xhdpi")
    fun onATableThePageIsOnTopAndTheListAndTheBarBelow() {
        val hingePx = acrossHinge(widthPx, heightPx)
        libraries(Window.Foldable, Window.Foldable.folded(hingePx, vertical = false))
        val hinge = inRoot(hingePx)
        assertNavigation(ShellNavigation.Bar)

        val detail = bounds("libraries-detail")
        val list = bounds("libraries-list")
        val bar = bounds("section-Home")
        assertTrue("the page is on the top half: $detail, hinge $hinge", detail.bottom <= hinge.top + 0.5.dp)
        assertEquals("the list starts after the hinge", hinge.bottom.value, list.top.value, 1f)
        assertTrue("the bar is on the bottom half: $bar", bar.top >= hinge.bottom)
        assertTrue("the list ends above the bar", list.bottom <= bar.top)
    }

    @Test @Config(qualifiers = "w673dp-h841dp-xhdpi")
    fun onATableAScreenThatDoesNotSplitKeepsToTheTopHalf() {
        val hingePx = acrossHinge(widthPx, heightPx)
        compose.setShell(window = Window.Foldable, info = Window.Foldable.folded(hingePx, vertical = false), screens = ListScreens)
        compose.waitForIdle()
        val hinge = inRoot(hingePx)
        val top = bounds("tabletop-top")
        assertTrue("the screen is above the hinge: $top, hinge $hinge", top.bottom <= hinge.top + 0.5.dp)
        assertEquals(0f, top.top.value, 1f)
        assertTrue("the bar is below it", bounds("section-Home").top >= hinge.bottom)
    }
}
