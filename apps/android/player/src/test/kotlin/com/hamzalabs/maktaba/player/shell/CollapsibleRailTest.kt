package com.hamzalabs.maktaba.player.shell

import androidx.activity.ComponentActivity
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assertContentDescriptionEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsSelected
import androidx.compose.ui.test.getUnclippedBoundsInRoot
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performMouseInput
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.player.R
import com.hamzalabs.maktaba.player.harness.ListScreens
import com.hamzalabs.maktaba.player.harness.Window
import com.hamzalabs.maktaba.player.harness.setShell
import com.hamzalabs.maktaba.player.harness.settledUntil
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config

/**
 * The collapsible rail of an expanded window (FR-8): icons only at first; its menu button, or
 * a mouse resting on it, expands it over the content (the content never moves); choosing a
 * section, a tap outside, Back or the mouse leaving collapse it. Right to left it is on the right.
 */
@RunWith(AndroidJUnit4::class)
@Config(qualifiers = "w1280dp-h800dp-xhdpi")
class CollapsibleRailTest {
    @get:Rule val compose = createAndroidComposeRule<ComponentActivity>()

    private fun bounds(tag: String) = compose.onNodeWithTag(tag).getUnclippedBoundsInRoot()
    private fun exists(tag: String) = compose.onAllNodes(hasTestTag(tag)).fetchSemanticsNodes().isNotEmpty()
    private fun railWidth() = bounds("shell-rail").let { it.right - it.left }
    private val expanded get() = exists("rail-scrim")

    private fun open() {
        compose.setShell(window = Window.Tablet, screens = ListScreens)
        compose.onNode(SemanticsMatcher.expectValue(ShellNavigationKey, ShellNavigation.CollapsibleRail)).assertExists()
    }

    private fun expand() {
        compose.onNodeWithTag("rail-toggle").performClick()
        compose.settledUntil(5_000) { expanded && railWidth() > 200.dp }
    }

    private fun assertCollapsed() {
        compose.settledUntil(5_000) { !expanded && railWidth() <= CollapsedRailWidth }
        assertFalse("no scrim", expanded)
        assertEquals(CollapsedRailWidth.value, railWidth().value, 1f)
    }

    @Test
    fun collapsedByDefaultTheContentHasNearlyTheWholeWidth() {
        open()
        assertCollapsed()
        val content = bounds("shell-content")
        val root = compose.onRoot().getUnclippedBoundsInRoot()
        assertEquals("the content starts after the rail", CollapsedRailWidth.value, content.left.value, 1f)
        assertEquals(root.right.value, content.right.value, 1f)
        // icons alone, each named for TalkBack
        assertTrue(compose.onAllNodes(hasText("Search")).fetchSemanticsNodes().isEmpty())
        compose.onNode(hasTestTag("section-Search") and hasContentDescription("Search")).assertExists()
        compose.onNodeWithTag("rail-toggle").assertContentDescriptionEquals(compose.activity.getString(R.string.shell_nav_expand))
    }

    @Test
    fun theMenuButtonExpandsItOverTheContentAndChoosingCollapsesIt() {
        open()
        val before = bounds("shell-content")
        expand()
        // modal: over the content, which keeps its place and width
        assertEquals(before, bounds("shell-content"))
        compose.onNodeWithText("Search").assertIsDisplayed()
        compose.onNodeWithTag("rail-toggle").assertContentDescriptionEquals(compose.activity.getString(R.string.shell_nav_collapse))

        compose.onNodeWithTag("section-Search").performClick()
        assertCollapsed()
        compose.onNodeWithTag("section-Search").assertIsSelected()
        compose.onNodeWithText("Search row 1").assertIsDisplayed()
    }

    @Test
    fun aTapOutsideCollapsesIt() {
        open()
        expand()
        compose.onNodeWithTag("rail-scrim").performClick()
        assertCollapsed()
        compose.onNodeWithTag("section-Home").assertIsSelected()
    }

    @Test
    fun backCollapsesItFirst() {
        open()
        compose.onNodeWithTag("section-LiveTv").performClick()
        expand()
        compose.activity.onBackPressedDispatcher.onBackPressed()
        assertCollapsed()
        // the section's own Back was not taken
        compose.onNodeWithTag("section-LiveTv").assertIsSelected()
    }

    @Test
    fun aMouseRestingOnItExpandsItAndLeavingCollapsesIt() {
        open()
        compose.onNodeWithTag("shell-rail").performMouseInput { moveTo(center) }
        compose.settledUntil(5_000) { expanded }
        compose.onNodeWithTag("shell-rail").performMouseInput { moveTo(Offset(width + 400f, centerY)) }
        assertCollapsed()
    }

    @Test
    @Config(qualifiers = "ar-w1280dp-h800dp-xhdpi")
    fun rightToLeftTheRailIsOnTheRight() {
        open()
        val root = compose.onRoot().getUnclippedBoundsInRoot()
        val rail = bounds("shell-rail")
        assertEquals("the rail ends at the right edge", root.right.value, rail.right.value, 1f)
        assertEquals((root.right - CollapsedRailWidth).value, bounds("shell-content").right.value, 1f)
        expand()
        // it grows leftwards, over the content
        val open = bounds("shell-rail")
        assertEquals(root.right.value, open.right.value, 1f)
        assertTrue(open.left < rail.left)
    }
}
