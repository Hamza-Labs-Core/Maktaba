package com.hamzalabs.maktaba.player.libraries

import androidx.activity.ComponentActivity
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.player.harness.FakeHome
import com.hamzalabs.maktaba.player.harness.setShell
import com.hamzalabs.maktaba.player.shell.PlayerSection
import com.hamzalabs.maktaba.player.shell.ShellNavigation
import com.hamzalabs.maktaba.player.shell.ShellNavigationKey
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
        // a single section offers no navigation
        compose.onNode(SemanticsMatcher.expectValue(ShellNavigationKey, ShellNavigation.None)).assertExists()
        assertTrue(home.requests.any { it.url.encodedPath == "/api/videos" })
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
}
