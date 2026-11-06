package com.hamzalabs.maktaba.desktop.libraries

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.size
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.runComposeUiTest
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.desktop.InPlayer
import com.hamzalabs.maktaba.desktop.Lang
import com.hamzalabs.maktaba.desktop.TestHome
import com.hamzalabs.maktaba.desktop.shell.Route
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.util.concurrent.CountDownLatch

/** Story 55.05 T06, and choosing a library or a series. */
@OptIn(ExperimentalTestApi::class)
class LibrariesScreenTest {
    private fun library(fake: TestHome): LibraryVideos {
        fake.answer("GET /api/libraries/l1/hub", """{"library":{"id":"l1","name":"Lectures"}}""")
        fake.answer("GET /api/videos/facets", """{"languages":[{"code":"ar"}],"speakers":[{"name":"Shaykh A"}]}""")
        fake.answer("GET /api/videos", """{"items":[{"id":"v1","title":"One"}]}""")
        fake.answer("GET /api/libraries/l1/series", """{"items":[{"id":"s1","name":"Seerah","episodes":10,"watched":3}]}""")
        return LibraryVideos(fake.client, "l1") {}.also { runBlocking { it.open() } }
    }

    @Test
    fun aWideWindowShowsTheFiltersAsMenusANarrowOneAsChips() = TestHome().use { fake ->
        val state = library(fake)
        runComposeUiTest {
            setContent { InPlayer(fake.home()) { Box(Modifier.size(1200.dp, 800.dp)) { LibraryContent(state, fake.home()) } } }
            onNodeWithTag("filters-menus").assertExists()
            onNodeWithTag("menu-speaker").assertExists()
        }
        runComposeUiTest {
            setContent { InPlayer(fake.home(), Lang.Ar) { Box(Modifier.size(600.dp, 800.dp)) { LibraryContent(state, fake.home()) } } }
            onNodeWithTag("filters-chips").assertExists()
            onNodeWithText("الأحدث").assertExists()
            onNodeWithText("المقاطع").assertExists()
        }
    }

    @Test
    fun theSeriesTabOpensASeries() = TestHome().use { fake ->
        val state = library(fake)
        val home = fake.home()
        runComposeUiTest {
            setContent { InPlayer(home) { Box(Modifier.size(1200.dp, 800.dp)) { LibraryContent(state, home) } } }
            onNodeWithTag("tab-series").performClick()
            waitUntil(timeoutMillis = 5_000) { onAllNodes(hasTestTag("series-s1")).fetchSemanticsNodes().isNotEmpty() }
            onNodeWithText("3 of 10 watched").assertExists()
            onNodeWithTag("series-s1").performClick()
            assertEquals(Route.Series("s1"), home.nav.current)
        }
    }

    @Test
    fun thePinnedAreUnderPinned() = TestHome().use { fake ->
        fake.answer("GET /api/libraries/summary", """{"items":[{"id":"a","name":"Alpha"},{"id":"b","name":"Beta","pinned":true,"pin_position":0}]}""")
        val s = LibrariesState(fake.client) {}.also { runBlocking { it.load() } }
        val home = fake.home()
        runComposeUiTest {
            setContent { InPlayer(home) { LibrariesContent(s, home) } }
            onNodeWithTag("pinned").assertExists()
            onNodeWithTag("library-all").assertExists()
            onNodeWithText("Beta").performClick()
            assertEquals(Route.Library("b"), home.nav.current)
        }
    }

    @Test
    fun aPinMadeJustBeforeLeavingThePageStays() = TestHome().use { fake ->
        fake.answer("GET /api/libraries/summary", """{"items":[{"id":"a","name":"Alpha","pinned":false},{"id":"b","name":"Beta","pinned":true,"pin_position":0}]}""")
        val gate = CountDownLatch(1)
        // the home puts the new pin first: its order wins once it answers
        fake.held("PUT /api/me/pinned-libraries", { """{"library_ids":["a","b"]}""" }, gate)
        val s = LibrariesState(fake.client) {}.also { runBlocking { it.load() } }
        val home = fake.home()
        var shown by mutableStateOf(true)
        runComposeUiTest {
            setContent { InPlayer(home) { if (shown) LibrariesContent(s, home) } }
            onNodeWithTag("pin-a").performClick()
            waitUntil(timeoutMillis = 5_000) { fake.asked("PUT /api/me/pinned-libraries").isNotEmpty() }
            // the person leaves Libraries while the home has not answered yet
            shown = false
            waitForIdle()
            gate.countDown()
            Thread.sleep(500)
            waitForIdle()
            assertTrue("the pin stays", s.libraries.first { it.id == "a" }.pinned)
            assertEquals("the home's answer was taken", listOf("a", "b"), s.libraries.filter { it.pinned }.map { it.id })
            assertFalse(s.saveFailed)
        }
    }
}
