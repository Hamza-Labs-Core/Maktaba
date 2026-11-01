package com.hamzalabs.maktaba.player.libraries

import androidx.activity.ComponentActivity
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextInput
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.player.harness.FakeHome
import com.hamzalabs.maktaba.player.harness.setShell
import com.hamzalabs.maktaba.player.shell.PlayerSection
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config

/**
 * Today's library browsing stays reachable in the shell until 48.27 replaces it: the app's
 * sections as they ship (only Libraries), against the fake home.
 */
@RunWith(AndroidJUnit4::class)
@Config(qualifiers = "w411dp-h891dp")
class LibrariesSectionTest {
    @get:Rule(order = 0) val home = FakeHome()
    @get:Rule(order = 1) val compose = createAndroidComposeRule<ComponentActivity>()

    private fun waitFor(text: String) =
        compose.waitUntil(5_000) { compose.onAllNodes(hasText(text)).fetchSemanticsNodes().isNotEmpty() }

    @Test
    fun theShippedShellOpensOnTheLibrariesWithTheHomesVideos() {
        compose.setShell(home, sections = PlayerSection.enabled)
        waitFor("The Message")
        compose.onNodeWithText("The Message").assertIsDisplayed()
        compose.onNodeWithText("Lion of the Desert").assertIsDisplayed()
        assertTrue(home.paths.toString(), "/api/videos" in home.paths)
    }

    @Test
    fun aVideoOpensItsPageAndBackReturnsToTheList() {
        compose.setShell(home, sections = PlayerSection.enabled)
        waitFor("Omar, Episode 1")
        compose.onNodeWithText("Omar, Episode 1").performClick()
        waitFor("A film about Omar, Episode 1.")
        compose.onNodeWithText("A film about Omar, Episode 1.").assertIsDisplayed()

        compose.activityRule.scenario.onActivity { it.onBackPressedDispatcher.onBackPressed() }

        waitFor("Lion of the Desert")
        compose.onNodeWithText("Lion of the Desert").assertIsDisplayed()
    }

    @Test
    fun withEverySectionOnLibrariesIsStillTheBrowser() {
        compose.setShell(home)
        compose.onNodeWithTag("section-Libraries").performClick()
        waitFor("The Message")
        compose.onNodeWithText("The Message").assertIsDisplayed()
    }

    @Test
    fun switchingSectionsKeepsTheBrowsersChoicesAndReselectingClosesTheVideo() {
        compose.setShell(home, sections = listOf(PlayerSection.Libraries, PlayerSection.Search))
        waitFor("Omar, Episode 1")
        compose.onNodeWithText("Search titles").performTextInput("Omar")
        waitFor("Omar, Episode 1")
        compose.onNodeWithText("Omar, Episode 1").performClick()
        waitFor("A film about Omar, Episode 1.")

        // another section, and back: the open video is still open
        compose.onNodeWithTag("section-Search").performClick()
        compose.onNodeWithTag("search").assertIsDisplayed()
        compose.onNodeWithTag("section-Libraries").performClick()
        waitFor("A film about Omar, Episode 1.")
        compose.onNodeWithText("A film about Omar, Episode 1.").assertIsDisplayed()

        // Libraries again: back to its list, the search still typed
        compose.onNodeWithTag("section-Libraries").performClick()
        waitFor("Lion of the Desert")
        compose.onAllNodes(hasText("A film about Omar, Episode 1.")).assertCountEquals(0)
        compose.onNode(hasText("Omar") and hasSetTextAction()).assertIsDisplayed()
    }
}
