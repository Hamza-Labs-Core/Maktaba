package com.hamzalabs.maktaba.player.libraries

import androidx.activity.ComponentActivity
import androidx.compose.material3.adaptive.WindowAdaptiveInfo
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.getUnclippedBoundsInRoot
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performSemanticsAction
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
 *   a screen that does not split itself keeps to the top half.
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
