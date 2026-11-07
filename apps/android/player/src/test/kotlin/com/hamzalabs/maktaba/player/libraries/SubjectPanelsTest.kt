package com.hamzalabs.maktaba.player.libraries

import androidx.activity.ComponentActivity
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.core.api.Capabilities
import com.hamzalabs.maktaba.player.harness.FakeHome
import com.hamzalabs.maktaba.player.harness.FakeHome.Companion.json
import com.hamzalabs.maktaba.player.harness.settledUntil
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config

/**
 * Story 54.20 on a series' page: "From plugins" below the episodes while the home answers
 * `plugin_panels: on`, asked for `series:<id>`; a chip searches Maktaba for its text. With
 * panels off the page never asks.
 */
@RunWith(AndroidJUnit4::class)
@Config(qualifiers = "w411dp-h891dp")
class SubjectPanelsTest {
    @get:Rule(order = 0) val home = FakeHome()
    @get:Rule(order = 1) val compose = createAndroidComposeRule<ComponentActivity>()

    private fun tagShows(tag: String) = compose.onAllNodes(hasTestTag(tag)).fetchSemanticsNodes().isNotEmpty()
    private fun panelAsks() = home.requests.filter { it.url.encodedPath == "/api/panels" }

    private fun openSeries() {
        home.answerLibraries()
        home.answer("/api/panels") {
            json(
                """{"panels":[{"plugin_id":"topics","plugin":"Topics","title":"Topics","blocks":[""" +
                    """{"type":"chips","value":["History","Andalusia"]},{"type":"kv","value":[{"label":"Era","value":"1920s"}]}]}]}""",
            )
        }
        home.answer("/api/search") { json("""{"search_id":"q1","titles":[],"transcript":[]}""") }
        compose.setLibraries(home)
        compose.settledUntil(10_000) { tagShows("library-l1") }
        compose.onNodeWithTag("tab-series").performClick()
        compose.settledUntil(10_000) { tagShows("series-s1") }
        compose.onNodeWithTag("series-s1").performClick()
        compose.settledUntil(10_000) { tagShows("continue") }
    }

    @Test
    fun aSeriesShowsItsPanelsAndAChipSearches() {
        home.features.value = Capabilities.AllOn.copy(panels = true)
        openSeries()
        compose.settledUntil(10_000) { panelAsks().isNotEmpty() }
        assertEquals("series:s1", panelAsks().last().url.queryParameter("subject"))
        compose.scrollUntil("episodes", "plugin-panels-toggle")
        compose.onNodeWithTag("plugin-panels-toggle").performScrollTo().performClick()
        compose.settledUntil(10_000) { compose.onAllNodes(hasText("History")).fetchSemanticsNodes().isNotEmpty() }
        compose.onNode(hasText("History")).performScrollTo().performClick()
        compose.settledUntil(10_000) {
            home.requests.any { it.method == "POST" && it.url.encodedPath == "/api/search" && "History" in it.bodyText() }
        }
    }

    @Test
    fun withPanelsOffTheSeriesNeverAsks() {
        openSeries()
        compose.waitForIdle()
        assertTrue(tagShows("episodes"))
        assertFalse("no panels asked while the home has them off", panelAsks().isNotEmpty())
        assertFalse(tagShows("plugin-panels"))
    }
}
