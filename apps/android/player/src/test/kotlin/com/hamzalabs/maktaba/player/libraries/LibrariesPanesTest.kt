package com.hamzalabs.maktaba.player.libraries

import androidx.activity.ComponentActivity
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsSelected
import androidx.compose.ui.test.getUnclippedBoundsInRoot
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.core.layout.Panes
import com.hamzalabs.maktaba.player.harness.FakeHome
import com.hamzalabs.maktaba.player.harness.Window
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config

/**
 * The section's list-detail (FR-8, FR-15): from 600 dp the list stays beside the chosen
 * library's or series' page; half folded it splits at the hinge, the page on the first half and
 * the list on the second. The JVM cannot fold a window, so the hinge is given as [Panes] (the
 * pattern of core's instrumented `HingeSplitTest`, which runs on a device).
 */
@RunWith(AndroidJUnit4::class)
class LibrariesPanesTest {
    @get:Rule(order = 0) val home = FakeHome()
    @get:Rule(order = 1) val compose = createAndroidComposeRule<ComponentActivity>()

    @Before fun routes() = home.answerLibraries()

    private fun waitForTag(tag: String) =
        compose.waitUntil(5_000) { compose.onAllNodes(hasTestTag(tag)).fetchSemanticsNodes().isNotEmpty() }

    private fun pageTitled(name: String) =
        compose.waitUntil(5_000) {
            compose.onAllNodes(hasText(name) and hasAnyAncestorTagged("library-page")).fetchSemanticsNodes().isNotEmpty()
        }

    private fun hasAnyAncestorTagged(tag: String) = androidx.compose.ui.test.hasAnyAncestor(hasTestTag(tag))

    @Test @Config(qualifiers = "w1280dp-h800dp-xhdpi")
    fun aTabletKeepsTheListBesideTheChosenLibrarysPage() {
        compose.setLibraries(home, Window.Tablet)
        waitForTag("libraries-detail")
        // the first library is shown at once, beside the list (no empty pane)
        pageTitled("Films")
        val list = compose.onNodeWithTag("libraries-list").getUnclippedBoundsInRoot()
        val detail = compose.onNodeWithTag("libraries-detail").getUnclippedBoundsInRoot()
        assertTrue("the list leads", list.right <= detail.left)
        assertEquals(380f, (list.right - list.left).value, 1f)
        compose.onNodeWithTag("library-l1").assertIsSelected()

        // another library replaces the page; nothing is pushed
        compose.onNodeWithTag("library-l2").performClick()
        pageTitled("Lessons")
        compose.onNodeWithTag("library-l2").assertIsSelected()
        compose.onNodeWithTag("libraries-list").assertIsDisplayed()

        // the Series tab: the first series' page beside it
        compose.onNodeWithTag("tab-series").performClick()
        waitForTag("series-page")
        waitForTag("continue")
    }

    @Test @Config(qualifiers = "w600dp-h900dp-xhdpi")
    fun sixHundredDpIsWideEnoughForTheList() {
        compose.setLibraries(home, Window.Foldable)
        waitForTag("libraries-detail")
        compose.onNodeWithTag("libraries-list").assertIsDisplayed()
    }

    @Test @Config(qualifiers = "w599dp-h900dp-xhdpi")
    fun narrowerIsOnePane() {
        compose.setLibraries(home, Window.Phone)
        waitForTag("libraries-grid")
        assertTrue(compose.onAllNodes(hasTestTag("libraries-detail")).fetchSemanticsNodes().isEmpty())
    }

    // ---------- the hinge ----------

    private var widthDp = 0.dp
    private var heightDp = 0.dp

    /** A book fold upright at a third of the window, 12 px wide, as a Fold reports it. */
    private fun book(rtl: Boolean): Pair<Dp, Dp> {
        val px = compose.activity.resources.displayMetrics.widthPixels
        val density = compose.activity.resources.displayMetrics.density
        val hinge = (px / 3)..(px / 3 + 12)
        compose.setLibraries(home, Window.Foldable, panes = Panes.Book(hinge), rtl = rtl)
        waitForTag("libraries-detail")
        widthDp = compose.onRoot().getUnclippedBoundsInRoot().right
        return (hinge.first / density).dp to (hinge.last / density).dp
    }

    @Test @Config(qualifiers = "w673dp-h841dp-xhdpi")
    fun halfFoldedLikeABookThePageIsLeftOfTheHingeAndTheListRight() {
        val (start, end) = book(rtl = false)
        val detail = compose.onNodeWithTag("libraries-detail").getUnclippedBoundsInRoot()
        val list = compose.onNodeWithTag("libraries-list").getUnclippedBoundsInRoot()
        assertEquals("the page ends at the hinge", start.value, detail.right.value, 2f)
        assertEquals("the list starts after it", end.value, list.left.value, 2f)
        assertEquals("the list ends at the right", widthDp.value, list.right.value, 2f)
    }

    @Test @Config(qualifiers = "ar-w673dp-h841dp-xhdpi")
    fun rightToLeftThePageIsRightOfTheHinge() {
        val (start, end) = book(rtl = true)
        val detail = compose.onNodeWithTag("libraries-detail").getUnclippedBoundsInRoot()
        val list = compose.onNodeWithTag("libraries-list").getUnclippedBoundsInRoot()
        assertEquals("the page starts after the hinge", end.value, detail.left.value, 2f)
        assertEquals("the list ends at the hinge", start.value, list.right.value, 2f)
        assertTrue("the page leads, on the right", detail.left > list.right)
    }

    @Test @Config(qualifiers = "w841dp-h673dp-xhdpi")
    fun tabletopThePageIsAboveTheHingeAndTheListBelow() {
        val px = compose.activity.resources.displayMetrics.heightPixels
        val density = compose.activity.resources.displayMetrics.density
        val hinge = (px / 2)..(px / 2 + 12)
        compose.setLibraries(home, Window.Tablet, panes = Panes.Tabletop(hinge))
        waitForTag("libraries-detail")
        heightDp = compose.onRoot().getUnclippedBoundsInRoot().bottom
        val detail = compose.onNodeWithTag("libraries-detail").getUnclippedBoundsInRoot()
        val list = compose.onNodeWithTag("libraries-list").getUnclippedBoundsInRoot()
        assertTrue("the page is above the list", detail.bottom <= list.top)
        assertEquals("the list starts after the hinge", (hinge.last / density), list.top.value, 2f)
        // choosing on the bottom half changes the top one
        compose.onNodeWithTag("library-l2").performClick()
        pageTitled("Lessons")
    }
}
