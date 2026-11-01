package com.hamzalabs.maktaba.player.shell

import androidx.activity.ComponentActivity
import androidx.compose.foundation.clickable
import androidx.compose.material3.Text
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsSelected
import androidx.compose.ui.test.getUnclippedBoundsInRoot
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollToIndex
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.player.harness.ListScreens
import com.hamzalabs.maktaba.player.harness.Window
import com.hamzalabs.maktaba.player.harness.setShell
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config

/**
 * The shell on the JVM (Robolectric), with the window's size class given (FR-8), and its
 * stacks: Back restores a list's scroll position (FR-9), sections keep their stacks.
 */
@RunWith(AndroidJUnit4::class)
class PlayerShellTest {
    @get:Rule val compose = createAndroidComposeRule<ComponentActivity>()

    private fun assertNavigation(expected: ShellNavigation) {
        compose.onNode(SemanticsMatcher.expectValue(ShellNavigationKey, expected)).assertExists()
    }

    private fun sectionBounds(section: PlayerSection) =
        compose.onNodeWithTag("section-${section.name}").getUnclippedBoundsInRoot()

    @Test
    @Config(qualifiers = "w411dp-h891dp")
    fun aPhoneHasTheBottomBar() {
        compose.setShell(window = Window.Phone, screens = ListScreens)
        assertNavigation(ShellNavigation.Bar)
        val root = compose.onRoot().getUnclippedBoundsInRoot()
        val home = sectionBounds(PlayerSection.Home)
        val profile = sectionBounds(PlayerSection.Profile)
        // along the bottom edge, side by side
        assertTrue("the bar is at the bottom: $home in $root", home.bottom > root.bottom - 100.dp)
        assertEquals(home.top, profile.top)
        assertTrue(profile.left > home.left)
    }

    @Test
    @Config(qualifiers = "w673dp-h841dp")
    fun aFoldableOpenHasTheRail() {
        compose.setShell(window = Window.Foldable, screens = ListScreens)
        assertNavigation(ShellNavigation.Rail)
        val home = sectionBounds(PlayerSection.Home)
        val profile = sectionBounds(PlayerSection.Profile)
        // down the start edge, one above the other, narrow
        assertTrue("the rail is at the start: $home", home.right < 100.dp)
        assertEquals(home.left, profile.left)
        assertTrue(profile.top > home.top)
    }

    @Test
    @Config(qualifiers = "w1280dp-h800dp")
    fun aLargeTabletHasThePermanentDrawer() {
        compose.setShell(window = Window.Tablet, screens = ListScreens)
        assertNavigation(ShellNavigation.Drawer)
        val home = sectionBounds(PlayerSection.Home)
        // a drawer's item spans its width, beside the content
        assertTrue("the drawer's item is wide: $home", home.right - home.left > 200.dp)
        assertTrue(home.right < 400.dp)
    }

    @Test
    @Config(qualifiers = "w673dp-h841dp")
    fun halfFoldedOnATableHasTheBar() {
        compose.setShell(window = Window.Foldable, tabletop = true, screens = ListScreens)
        assertNavigation(ShellNavigation.Bar)
    }

    @Test
    fun aVideoPlayingFullScreenHidesTheNavigation() {
        compose.setShell(immersive = true, screens = ListScreens)
        assertNavigation(ShellNavigation.None)
        compose.onAllNodesWithTag("section-Home").assertCountEquals(0)
    }

    @Test
    fun oneReachableSectionShowsNoNavigation() {
        compose.setShell(sections = listOf(PlayerSection.Libraries), screens = ListScreens)
        assertNavigation(ShellNavigation.None)
        compose.onNodeWithText("Libraries row 1").assertIsDisplayed()
    }

    @Test
    fun theFirstSectionShowsFirst() {
        compose.setShell(screens = ListScreens)
        compose.onNodeWithTag("section-Home").assertIsSelected()
        compose.onNodeWithText("Home row 1").assertIsDisplayed()
    }

    @Test
    fun backFromAPushedScreenRestoresTheListsScrollPosition() {
        compose.setShell(screens = ListScreens)
        compose.onNodeWithTag("list").performScrollToIndex(40)
        compose.onNodeWithTag("row-41").performClick()
        compose.onNodeWithText("video v41").assertIsDisplayed()

        compose.activityRule.scenario.onActivity { it.onBackPressedDispatcher.onBackPressed() }

        compose.onNodeWithText("Home row 41").assertIsDisplayed()
        compose.onAllNodesWithTag("row-1").assertCountEquals(0)
    }

    @Test
    fun eachSectionKeepsItsStackAndReselectingReturnsToTheRoot() {
        compose.setShell(screens = ListScreens)
        compose.onNodeWithTag("list").performScrollToIndex(30)
        compose.onNodeWithTag("row-31").performClick()
        compose.onNodeWithText("video v31").assertIsDisplayed()

        // another section, then back to Home: the video is still where Home was left
        compose.onNodeWithTag("section-Search").performClick()
        compose.onNodeWithText("Search row 1").assertIsDisplayed()
        compose.onNodeWithTag("section-Home").performClick()
        compose.onNodeWithText("video v31").assertIsDisplayed()

        // Home again: its root, at the row it was scrolled to
        compose.onNodeWithTag("section-Home").performClick()
        compose.onNodeWithText("Home row 31").assertIsDisplayed()
    }

    @Test
    fun backAtAnotherSectionsRootReturnsToTheFirstSection() {
        compose.setShell(screens = ListScreens)
        compose.onNodeWithTag("section-LiveTv").performClick()
        compose.onNodeWithText("LiveTv row 1").assertIsDisplayed()

        compose.activityRule.scenario.onActivity { it.onBackPressedDispatcher.onBackPressed() }

        compose.onNodeWithTag("section-Home").assertIsSelected()
        compose.onNodeWithText("Home row 1").assertIsDisplayed()
    }

    @Test
    fun backAtASectionsRootKeepsTheRoot() {
        val answers = mutableListOf<Boolean>()
        compose.setShell(sections = listOf(PlayerSection.Home)) { route, nav ->
            when (route) {
                // a pushed screen that goes back twice in one tap: the second reaches the root
                is Route.Video -> Text("video", Modifier.testTag("video").clickable { answers += nav.back(); answers += nav.back() })
                else -> Text("root $route", Modifier.testTag("root").clickable {
                    if (nav.canGoBack) answers += nav.back() else nav.open(Route.Video("v1"))
                })
            }
        }
        compose.onNodeWithTag("root").performClick()
        compose.onNodeWithTag("video").performClick()

        assertEquals("back once, then nothing to go back to", listOf(true, false), answers)
        compose.onNodeWithText("root Home").assertIsDisplayed()
    }
}
