package com.hamzalabs.maktaba.player.libraries

import androidx.activity.ComponentActivity
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.player.harness.FakeHome
import com.hamzalabs.maktaba.player.harness.FakeHome.Companion.json
import com.hamzalabs.maktaba.player.harness.settledUntil
import org.junit.After
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean

/**
 * A series' page is read again each time it shows (back from an episode). A watched mark
 * tapped while that read is in flight stays: the read was answered before the save, so it
 * still has the old marks and must not put them back while the home has the new ones.
 */
@RunWith(AndroidJUnit4::class)
@Config(qualifiers = "w411dp-h891dp")
class SeriesReadAgainTest {
    @get:Rule(order = 0) val home = FakeHome()
    @get:Rule(order = 1) val compose = createAndroidComposeRule<ComponentActivity>()

    private val hold = AtomicBoolean(false)
    private val asked = CountDownLatch(1)
    private val release = CountDownLatch(1)

    /** A read still held is let go, so the fake home can close. */
    @After fun letGo() = release.countDown()

    private fun tagShows(tag: String) = compose.onAllNodes(hasTestTag(tag)).fetchSemanticsNodes().isNotEmpty()
    private fun described(text: String) =
        compose.onAllNodes(SemanticsMatcher.expectValue(SemanticsProperties.ContentDescription, listOf(text))).fetchSemanticsNodes().isNotEmpty()

    @Test
    fun aMarkWhileTheSeriesIsReadAgainIsKept() {
        home.answerLibraries()
        home.answer("/api/series/s1/episodes") {
            // answered as the home has it when asked (Lion of the Desert not watched)
            if (hold.getAndSet(false)) {
                asked.countDown()
                release.await(10, TimeUnit.SECONDS)
            }
            json(EPISODES)
        }
        compose.setLibraries(home)
        compose.settledUntil(10_000) { tagShows("library-l1") }
        compose.onNodeWithTag("tab-series").performClick()
        compose.settledUntil(10_000) { tagShows("series-s1") }
        compose.onNodeWithTag("series-s1").performClick()
        compose.settledUntil(10_000) { tagShows("continue") }

        // an episode, and back: the page shows what it read last and reads it again (held)
        hold.set(true)
        compose.onNodeWithTag("continue").performClick()
        compose.settledUntil(10_000) { compose.onAllNodes(hasText("A film about Lion of the Desert.")).fetchSemanticsNodes().isNotEmpty() }
        compose.activityRule.scenario.onActivity { it.onBackPressedDispatcher.onBackPressed() }
        compose.settledUntil(10_000) { tagShows("mark-v3") }
        compose.settledUntil(10_000) { asked.count == 0L }
        compose.onNodeWithTag("mark-v3").performClick()
        compose.settledUntil(10_000) {
            home.requests.any { it.method == "POST" && it.url.encodedPath == "/api/me/watched" } &&
                described("Mark not watched, Lion of the Desert")
        }

        release.countDown()
        // the held read answered and applied: the home has nothing more under way
        compose.registerIdlingResource(home.idling)
        compose.waitForIdle()
        assertTrue("the mark tapped while the page was read again stays", described("Mark not watched, Lion of the Desert"))
    }
}
