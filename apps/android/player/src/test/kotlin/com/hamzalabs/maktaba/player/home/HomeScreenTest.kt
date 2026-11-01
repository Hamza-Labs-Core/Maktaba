package com.hamzalabs.maktaba.player.home

import androidx.activity.ComponentActivity
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.isHeading
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.longClick
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollToNode
import androidx.compose.ui.test.performTouchInput
import androidx.compose.ui.test.swipeDown
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.core.session.AppSession
import com.hamzalabs.maktaba.player.harness.FakeHome
import com.hamzalabs.maktaba.player.harness.Window
import com.hamzalabs.maktaba.player.shell.ShellNavigation
import com.hamzalabs.maktaba.player.shell.ShellNavigationKey
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import java.io.File
import java.util.concurrent.CountDownLatch

/**
 * Home against the mock home (48.26, as 48.17's criteria): the hero then FR-10's rows, an empty
 * row absent; a card opens its video, Resume opens the player at the position; Not interested
 * removes the card and calls the DELETE route; the next launch shows the kept rows before the
 * home answers (the mock holds its answer until the test releases it); pull to refresh; a failure; the empty home (FR-13); and an
 * accessibility audit.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
// tall enough that every row is laid out at once, so their order is read from where they are
@Config(qualifiers = "w411dp-h3200dp-mdpi")
class HomeScreenTest {
    @get:Rule(order = 0) val home = FakeHome()
    @get:Rule(order = 1) val compose = createAndroidComposeRule<ComponentActivity>()

    private fun waitForTag(tag: String, timeout: Long = 5_000) =
        compose.waitUntil(timeout) { compose.onAllNodes(hasTestTag(tag)).fetchSemanticsNodes().isNotEmpty() }

    /** [text] shown: as a text, or in a card's words (a card speaks as one node). */
    private fun waitForText(text: String, timeout: Long = 5_000) =
        compose.waitUntil(timeout) { compose.onAllNodes(hasText(text) or hasContentDescription(text, substring = true)).fetchSemanticsNodes().isNotEmpty() }

    private fun inRow(row: String, tag: String) = hasTestTag(tag) and hasAnyAncestor(hasTestTag("row-$row"))

    private fun count(path: String, method: String = "GET") = home.requests.count { it.url.encodedPath == path && it.method == method }

    private fun showFull() {
        HomeFixtures.install(home)
        compose.setHome(home)
        waitForTag("row-$KEY_FROM_LIBRARY")
    }

    @Test
    fun theHeroThenTheRowsInFr10sOrderAndAnEmptyRowIsAbsent() {
        showFull()
        val tops = compose.onAllNodes(SemanticsMatcher("a row or the hero") { n ->
            val tag = n.config.getOrElseNullable(androidx.compose.ui.semantics.SemanticsProperties.TestTag) { null }
            tag == "hero" || (tag?.startsWith("row-") == true)
        }).fetchSemanticsNodes().sortedBy { it.boundsInRoot.top }
            .map { it.config[androidx.compose.ui.semantics.SemanticsProperties.TestTag] }
        assertEquals(listOf("hero") + HomeFixtures.ROW_ORDER.map { "row-$it" }, tops)
        assertTrue("Favorites is empty: absent", compose.onAllNodes(hasTestTag("row-$KEY_FAVORITES")).fetchSemanticsNodes().isEmpty())

        // the hero is the featured series: its name, its lessons, Play and Details
        compose.onNode(hasText("سيرة ابن هشام") and isHeading() and hasAnyAncestor(hasTestTag("hero"))).assertIsDisplayed()
        compose.onNode(hasText("24 lessons")).assertIsDisplayed()
        // every row's title is a heading; Live TV's card is its programme on its channel
        compose.onNode(hasText("Continue Watching") and isHeading()).assertIsDisplayed()
        compose.onNode(hasText("What's On Now") and isHeading()).assertIsDisplayed()
        compose.onNode(hasText("Lectures") and isHeading()).assertIsDisplayed()
        compose.onNodeWithTag("channel-${HomeFixtures.CHANNEL}").assertIsDisplayed()
        // a recommendation's title is read from its video
        waitForText("The Night Journey")
        assertTrue(home.paths.toString(), "/api/recommendations" in home.paths && "/api/channels/now" in home.paths)
        assertEquals("surface=mobile-home", home.requests.first { it.url.encodedPath == "/api/recommendations" }.url.encodedQuery)
    }

    @Test
    fun aCardOpensItsVideosPageAndASeriesGroupItsSeries() {
        showFull()
        compose.onNode(inRow(KEY_CONTINUE, "card-${HomeFixtures.CONTINUE}")).performClick()
        waitForTag("opened")
        compose.onNodeWithTag("opened").assertTextEquals("video ${HomeFixtures.CONTINUE}")

        compose.activityRule.scenario.onActivity { it.onBackPressedDispatcher.onBackPressed() }
        waitForTag("row-$KEY_ADDED")
        compose.onNode(inRow(KEY_ADDED, "card-series:${HomeFixtures.FEATURED_SERIES}")).performClick()
        waitForTag("opened")
        compose.onNodeWithTag("opened").assertTextEquals("series ${HomeFixtures.FEATURED_SERIES}")
    }

    @Test
    fun resumeInAContinueWatchingCardsMenuOpensThePlayerAtThePosition() {
        showFull()
        compose.onNode(inRow(KEY_CONTINUE, "card-${HomeFixtures.CONTINUE}")).performTouchInput { longClick() }
        waitForTag("menu-resume")
        compose.onNodeWithTag("menu-resume").assertIsDisplayed().performClick()
        waitForTag("opened")
        compose.onNodeWithTag("opened").assertTextEquals("watch ${HomeFixtures.CONTINUE} at 600")
    }

    @Test
    fun notInterestedRemovesTheCardAndCallsTheDeleteRoute() {
        showFull()
        val card = inRow(KEY_FOR_YOU, "card-${HomeFixtures.REC_1}")
        compose.onNode(card).performTouchInput { longClick() }
        waitForTag("menu-not-interested")
        assertTrue("a recommendation offers no Resume", compose.onAllNodes(hasTestTag("menu-resume")).fetchSemanticsNodes().isEmpty())
        compose.onNodeWithTag("menu-not-interested").performClick()

        compose.waitUntil(5_000) { compose.onAllNodes(card).fetchSemanticsNodes().isEmpty() }
        compose.waitUntil(5_000) { count("/api/recommendations/items/${HomeFixtures.REC_1}", "DELETE") == 1 }
        // the rest of the rail stays
        compose.onNode(inRow(KEY_FOR_YOU, "card-${HomeFixtures.REC_2}")).assertExists()
    }

    @Test
    fun theNextLaunchShowsTheKeptRowsBeforeTheHomeAnswers() {
        val launch = mutableIntStateOf(0)
        HomeFixtures.install(home)
        compose.setHome(home, launch = launch)
        waitForTag("row-$KEY_CONTINUE")
        val kept = File(ApplicationProvider.getApplicationContext<android.content.Context>().cacheDir, HomeCache.FOLDER)
        compose.waitUntil(5_000) { kept.listFiles().orEmpty().any { it.name.endsWith(".json") } }

        // the home now answers only when the test lets it, with a new title
        val gate = CountDownLatch(1)
        HomeFixtures.install(home, homeBody = HomeFixtures.HOME.replace("Fiqh of Fasting 7", "Fiqh of Fasting 8"), homeGate = gate)
        val asked = count("/api/home")
        try {
            compose.runOnIdle { launch.intValue = 1 }

            // while the home has not answered: the kept rows, and Home asking again behind them
            // (a node query first: it lets Compose run the new launch, which a request count alone does not)
            waitForTag("row-$KEY_CONTINUE", timeout = 15_000)
            compose.waitUntil(15_000) { compose.waitForIdle(); count("/api/home") > asked }
            compose.onNode(inRow(KEY_CONTINUE, "card-${HomeFixtures.CONTINUE}")).assertExists()
            assertTrue("the new answer is not in yet", compose.onAllNodes(hasContentDescription("Fiqh of Fasting 8", substring = true)).fetchSemanticsNodes().isEmpty())
        } finally {
            gate.countDown()
        }

        // then the fresh answer replaces them
        compose.waitUntil(15_000) { compose.onAllNodes(hasContentDescription("Fiqh of Fasting 8", substring = true)).fetchSemanticsNodes().isNotEmpty() }
        assertTrue(compose.onAllNodes(hasContentDescription("Fiqh of Fasting 7", substring = true)).fetchSemanticsNodes().isEmpty())
    }

    @Test
    fun pullToRefreshReadsHomeAgain() {
        showFull()
        compose.waitUntil(5_000) { count("/api/home") == 1 }
        compose.onNodeWithTag("home-rows").performTouchInput { swipeDown() }
        compose.waitUntil(5_000) { count("/api/home") == 2 }
    }

    @Test
    fun aHomeThatFailsSaysSoAndTryAgainLoadsIt() {
        HomeFixtures.install(home)
        home.answer("/api/home") { FakeHome.json("""{"title":"boom"}""", code = 500) }
        compose.setHome(home)
        waitForTag("error-state")
        compose.onNode(hasText("Home could not load")).assertIsDisplayed()

        HomeFixtures.install(home)
        compose.onNode(hasText("Try again")).performClick()
        waitForTag("row-$KEY_CONTINUE")
    }

    @Test
    fun aRefreshThatFailsKeepsTheRowsAndSaysSo() {
        showFull()
        compose.waitUntil(5_000) { count("/api/home") == 1 }
        home.answer("/api/home") { FakeHome.json("{}", code = 503) }
        compose.onNodeWithTag("home-rows").performTouchInput { swipeDown() }
        waitForTag("home-stale")
        compose.onNodeWithTag("row-$KEY_CONTINUE").assertExists()
    }

    @Test
    fun anEmptyHomeSaysSoWithSwitchHomeAndSignOut() {
        HomeFixtures.install(home, homeBody = HomeFixtures.EMPTY_HOME, recs = HomeFixtures.EMPTY_RECS, now = null)
        compose.setHome(home)
        waitForTag("home-empty")
        compose.onNode(hasText("Nothing to watch yet") and isHeading()).assertIsDisplayed()
        compose.onNodeWithTag("home-switch").assertIsDisplayed()
        compose.onNodeWithTag("home-sign-out").assertIsDisplayed().performClick()
        compose.waitUntil(2_000) { home.home.session.phase.value == AppSession.Phase.SigningIn }
    }

    @Test
    fun switchHomeFromTheEmptyHomeChoosesAnother() {
        HomeFixtures.install(home, homeBody = HomeFixtures.EMPTY_HOME, recs = HomeFixtures.EMPTY_RECS, now = null)
        home.home.session.signedOut() // a phase other than choosing, to see it change
        compose.setHome(home)
        waitForTag("home-switch")
        compose.onNodeWithTag("home-switch").performClick()
        compose.waitUntil(2_000) { home.home.session.phase.value == AppSession.Phase.ChoosingHome }
    }

    @Test
    fun theShippedShellOpensOnHome() {
        showFull()
        // Home is the first section, with the others beside it in the bar
        compose.onNode(SemanticsMatcher.expectValue(ShellNavigationKey, ShellNavigation.Bar)).assertExists()
        compose.onNodeWithTag("section-Home").assertIsDisplayed()
        compose.onNodeWithTag("home-rows").assertIsDisplayed()
    }

    @Test
    fun theAccessibilityAuditPasses() {
        showFull()
        waitForText("The Night Journey")
        compose.waitForIdle()
        val problems = compose.accessibilityProblems()
        assertTrue("Accessibility problems:\n" + problems.joinToString("\n"), problems.isEmpty())
        // the long press is named for TalkBack on a card with a menu, and absent on one without
        val cw = compose.onNode(inRow(KEY_CONTINUE, "card-${HomeFixtures.CONTINUE}")).fetchSemanticsNode()
        assertEquals("More options", cw.config[androidx.compose.ui.semantics.SemanticsActions.OnLongClick].label)
        val deck = compose.onNode(inRow(KEY_ON_DECK, "card-${HomeFixtures.ON_DECK}")).fetchSemanticsNode()
        assertFalse(deck.config.contains(androidx.compose.ui.semantics.SemanticsActions.OnLongClick))
    }

    @Test
    @Config(qualifiers = "w1280dp-h2400dp-mdpi")
    fun onATabletTheRowsAreTheSameAndTheAuditPasses() {
        HomeFixtures.install(home)
        compose.setHome(home, window = Window.Tablet)
        waitForTag("row-$KEY_FROM_LIBRARY")
        compose.onNodeWithTag("home-rows").performScrollToNode(hasTestTag("row-$KEY_CONTINUE"))
        waitForText("The Night Journey")
        val problems = compose.accessibilityProblems()
        assertTrue("Accessibility problems:\n" + problems.joinToString("\n"), problems.isEmpty())
    }
}
