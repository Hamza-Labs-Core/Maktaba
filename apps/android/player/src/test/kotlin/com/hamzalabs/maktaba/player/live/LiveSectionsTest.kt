package com.hamzalabs.maktaba.player.live

import androidx.activity.ComponentActivity
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.player.harness.FakeHome
import com.hamzalabs.maktaba.player.harness.ListScreens
import com.hamzalabs.maktaba.player.harness.Window
import com.hamzalabs.maktaba.player.harness.info
import com.hamzalabs.maktaba.player.shell.PlayerSection
import com.hamzalabs.maktaba.player.shell.PlayerSection.Libraries
import com.hamzalabs.maktaba.player.shell.PlayerSection.LiveTv
import com.hamzalabs.maktaba.player.shell.PlayerSection.Search
import com.hamzalabs.maktaba.player.shell.PlayerShell
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config

/** FR-5 / 48.21: no channel for this user, no Live TV section. */
@RunWith(AndroidJUnit4::class)
@Config(qualifiers = "w411dp-h891dp")
class LiveSectionsTest {
    @get:Rule(order = 0) val home = FakeHome()
    @get:Rule(order = 1) val compose = createAndroidComposeRule<ComponentActivity>()

    private val three = listOf(Libraries, LiveTv, Search)

    @Test
    fun liveTvIsOfferedOnlyWhenTheHomeListsAChannel() {
        assertEquals(three, liveSections(three, hasChannels = true))
        assertEquals(listOf(Libraries, Search), liveSections(three, hasChannels = false))
        assertEquals("until the home has said", listOf(Libraries, Search), liveSections(three, hasChannels = null))
        assertEquals("never an empty shell", listOf(LiveTv), liveSections(listOf(LiveTv), hasChannels = false))
    }

    @Test
    fun liveTvShipsInTheSectionList() {
        assertTrue(LiveTv in PlayerSection.enabled)
    }

    private fun shell() = compose.setLive(home) { signedIn ->
        PlayerShell(sections = rememberShownSections(signedIn, three), adaptiveInfo = Window.Phone.info(), screens = ListScreens)
    }

    @Test
    fun noChannelsHidesTheLiveTvSection() {
        LiveFixtures.install(home, channels = emptyList())
        shell()
        compose.waitUntil(5_000) { "/api/channels/now" in home.paths }
        compose.waitForIdle()
        compose.onAllNodes(hasTestTag("section-Libraries")).assertCountEquals(1)
        compose.onAllNodes(hasTestTag("section-LiveTv")).assertCountEquals(0)
        compose.onAllNodes(hasTestTag("section-Search")).assertCountEquals(1)
    }

    @Test
    fun aChannelShowsTheLiveTvSection() {
        LiveFixtures.install(home)
        shell()
        compose.waitUntil(5_000) { compose.onAllNodes(hasTestTag("section-LiveTv")).fetchSemanticsNodes().isNotEmpty() }
    }

    @Test
    fun anUnreachableHomeDoesNotOfferLiveTv() {
        // the fake home answers 404 to /api/channels/now: no answer, no section
        shell()
        compose.waitUntil(5_000) { "/api/channels/now" in home.paths }
        compose.waitForIdle()
        compose.onAllNodes(hasTestTag("section-LiveTv")).assertCountEquals(0)
    }
}
