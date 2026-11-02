package com.hamzalabs.maktaba.player.libraries

import android.content.Context
import androidx.activity.ComponentActivity
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.getUnclippedBoundsInRoot
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performCustomAccessibilityActionWithLabel
import androidx.compose.ui.test.performKeyInput
import androidx.compose.ui.test.performSemanticsAction
import androidx.compose.ui.test.performTouchInput
import androidx.compose.ui.test.pressKey
import androidx.compose.ui.test.swipe
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.core.R as CoreR
import com.hamzalabs.maktaba.core.layout.DetailPaneMinWidth
import com.hamzalabs.maktaba.core.layout.ListPaneMinWidth
import com.hamzalabs.maktaba.core.layout.SplitHandleTarget
import com.hamzalabs.maktaba.core.layout.SplitStep
import com.hamzalabs.maktaba.player.harness.FakeHome
import com.hamzalabs.maktaba.player.harness.Window
import com.hamzalabs.maktaba.player.harness.settled
import com.hamzalabs.maktaba.player.shell.CollapsedRailWidth
import com.hamzalabs.maktaba.player.shell.PlayerSection
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config

/**
 * Libraries on a foldable open flat held sideways (841 dp, expanded: the collapsible rail):
 * exactly the list and the page, the page at least its minimum; the divider dragged (or moved
 * by TalkBack's actions, or the arrow keys) changes both, inside both minimums, mirrored right
 * to left, and the split is kept.
 */
@RunWith(AndroidJUnit4::class)
@Config(qualifiers = "w841dp-h673dp-xhdpi")
class LibrariesSplitTest {
    @get:Rule(order = 0) val home = FakeHome()
    @get:Rule(order = 1) val compose = createAndroidComposeRule<ComponentActivity>()

    @Before fun routes() = home.answerLibraries()

    private fun exists(tag: String) = compose.onAllNodes(hasTestTag(tag)).fetchSemanticsNodes().isNotEmpty()
    private fun bounds(tag: String) = compose.onNodeWithTag(tag).getUnclippedBoundsInRoot()
    private fun width(tag: String): Dp = bounds(tag).let { it.right - it.left }
    private val root get() = compose.onRoot().getUnclippedBoundsInRoot()
    private val density get() = compose.activity.resources.displayMetrics.density
    private val slopDp get() = android.view.ViewConfiguration.get(compose.activity).scaledTouchSlop / density

    private fun open(window: Window = Window.FoldableWide) {
        compose.setLibraries(home, window, sections = PlayerSection.entries)
        compose.settled(home, "the shell") { exists("section-Libraries") }
        compose.onNodeWithTag("section-Libraries").performSemanticsAction(SemanticsActions.OnClick)
        compose.settled(home, "the list beside the page") { exists("libraries-list") && exists("libraries-detail") }
    }

    /** Drags the divider [dp] to the right (negative: left), as a finger would. */
    private fun drag(dp: Float) {
        compose.onNodeWithTag("split-handle").performTouchInput {
            swipe(center, center + Offset(dp * density, 0f), durationMillis = 300)
        }
        compose.waitForIdle()
    }

    @Test
    fun exactlyTheListAndThePageAtUsableWidths() {
        open()
        val list = bounds("split-list")
        val detail = bounds("split-detail")
        assertEquals("the list starts after the collapsed rail", CollapsedRailWidth.value, list.left.value, 1f)
        assertTrue("the list leads", list.right <= detail.left)
        assertEquals("the page reaches the right edge", root.right.value, detail.right.value, 1f)
        // two panes and the 1 dp divider fill the room beside the rail: no third pane
        assertEquals((root.right - CollapsedRailWidth).value, (width("split-list") + width("split-detail") + 1.dp).value, 1.5f)
        assertTrue("the page is usable: ${width("split-detail")}", width("split-detail") >= DetailPaneMinWidth)
        assertTrue("the list is usable: ${width("split-list")}", width("split-list") >= ListPaneMinWidth)
        // the library's videos are on the page
        compose.settled(home, "the library's page") { exists("library-page") }
        // the handle is a 48 dp target on the divider
        val handle = bounds("split-handle")
        assertEquals(SplitHandleTarget.value, (handle.right - handle.left).value, 0.5f)
        assertEquals(list.right.value + 0.5f, ((handle.left + handle.right) / 2).value, 1f)
    }

    @Test
    fun draggingTheDividerChangesBothWithinTheMinimums() {
        open()
        val list = width("split-list")
        val detail = width("split-detail")
        drag(80f)
        val grown = width("split-list") - list
        assertTrue("the list grew by the drag, less the touch slop: $grown", grown.value in (80f - slopDp - 2f)..81f)
        assertEquals("the page gave the same", grown.value, (detail - width("split-detail")).value, 1f)

        drag(1000f)
        assertEquals("the page stops at its minimum", DetailPaneMinWidth.value, width("split-detail").value, 1f)
        drag(-1000f)
        assertEquals("the list stops at its minimum", ListPaneMinWidth.value, width("split-list").value, 1f)
    }

    @Test
    @OptIn(androidx.compose.ui.test.ExperimentalTestApi::class)
    fun talkBackWidensAndNarrowsTheList() {
        open()
        val list = width("split-list")
        val handle = compose.onNodeWithTag("split-handle")
        handle.performCustomAccessibilityActionWithLabel(compose.activity.getString(CoreR.string.split_widen_list))
        compose.waitForIdle()
        assertEquals((list + SplitStep).value, width("split-list").value, 1f)
        handle.performCustomAccessibilityActionWithLabel(compose.activity.getString(CoreR.string.split_narrow_list))
        handle.performCustomAccessibilityActionWithLabel(compose.activity.getString(CoreR.string.split_narrow_list))
        compose.waitForIdle()
        assertEquals((list - SplitStep).value, width("split-list").value, 1f)
    }

    @Test
    fun theArrowKeysMoveTheFocusedDivider() {
        open()
        val list = width("split-list")
        val handle = compose.onNodeWithTag("split-handle")
        handle.performSemanticsAction(SemanticsActions.RequestFocus)
        handle.performKeyInput { pressKey(Key.DirectionRight) }
        compose.waitForIdle()
        assertEquals((list + SplitStep).value, width("split-list").value, 1f)
    }

    @Test
    fun theSplitIsKept() {
        open()
        drag(80f)
        val share = compose.activity.getSharedPreferences("maktaba.splits", Context.MODE_PRIVATE).getFloat("libraries", Float.NaN)
        assertFalse("written for the next launch", share.isNaN())
        val total = (root.right - CollapsedRailWidth).value
        assertEquals(width("split-list").value / total, share, 0.01f)
    }

    @Test
    @Config(qualifiers = "ar-w841dp-h673dp-xhdpi")
    fun rightToLeftTheListIsOnTheRightAndDraggingIsMirrored() {
        open()
        val list = bounds("split-list")
        val detail = bounds("split-detail")
        assertEquals("the list ends at the rail, on the right", (root.right - CollapsedRailWidth).value, list.right.value, 1f)
        assertTrue("the page is left of it", detail.right <= list.left)
        val before = width("split-list")
        drag(-80f)
        assertTrue("dragging left widens the list: ${width("split-list")} vs $before", width("split-list") > before + 60.dp)
    }

    @Test
    @Config(qualifiers = "w1280dp-h800dp-xhdpi")
    fun aTabletGivesTheListItsPreferredWidth() {
        open(Window.Tablet)
        assertEquals(380f, width("split-list").value, 1f)
        assertTrue(width("split-detail") > 700.dp)
    }
}
