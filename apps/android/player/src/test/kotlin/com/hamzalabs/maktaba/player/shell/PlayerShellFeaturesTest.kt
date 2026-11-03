package com.hamzalabs.maktaba.player.shell

import android.app.Application
import android.content.Intent
import androidx.activity.ComponentActivity
import androidx.compose.material3.Text
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import com.hamzalabs.maktaba.player.live.LiveGate
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.core.api.Feature
import com.hamzalabs.maktaba.player.harness.FakeHome
import com.hamzalabs.maktaba.player.harness.ListScreens
import com.hamzalabs.maktaba.player.harness.Window
import com.hamzalabs.maktaba.player.harness.info
import com.hamzalabs.maktaba.player.harness.settledUntil
import com.hamzalabs.maktaba.player.live.LiveFixtures
import com.hamzalabs.maktaba.player.live.LiveTvScreen
import com.hamzalabs.maktaba.player.live.rememberShownSections
import com.hamzalabs.maktaba.player.live.setLive
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config

/**
 * Story 51.11 T11: the shell with Live TV off on the home. A viewer has no Live TV section (and
 * nothing asks for channels); an admin keeps it, and its screen says Live TV is off with a
 * button that opens the home's Components page.
 */
@RunWith(AndroidJUnit4::class)
@Config(qualifiers = "w411dp-h891dp")
class PlayerShellFeaturesTest {
    @get:Rule(order = 0) val home = FakeHome()
    @get:Rule(order = 1) val compose = createAndroidComposeRule<ComponentActivity>()

    private val three = listOf(PlayerSection.Libraries, PlayerSection.LiveTv, PlayerSection.Search)

    /** The shell: Live TV's own screen, the other sections lists. */
    private val screens: PlayerScreens = { route, nav -> if (route == Route.LiveTv) LiveTvScreen(nav) else ListScreens(route, nav) }

    private fun shell() = compose.setLive(home) { signedIn ->
        PlayerShell(sections = rememberShownSections(signedIn, three), adaptiveInfo = Window.Phone.info(), screens = screens)
    }

    private fun sections(name: String) = compose.onAllNodes(hasTestTag("section-$name")).fetchSemanticsNodes().size

    @Test
    fun aViewerHasNoLiveTvSectionWhileItIsOff() {
        LiveFixtures.install(home) // the home lists channels: the section would show
        home.viewerWithOff(Feature.LIVE_TV)
        shell()
        compose.waitForIdle()
        assertEquals(0, sections("LiveTv"))
        assertEquals(1, sections("Libraries"))
        assertFalse("nothing asks for channels while Live TV is off", "/api/channels/now" in home.paths)
    }

    @Test
    fun anAdminKeepsTheSectionAndReadsWhyWithTheWayToTurnItOn() {
        home.adminWithOff(Feature.LIVE_TV)
        shell()
        compose.settledUntil(5_000) { sections("LiveTv") == 1 }
        compose.onNodeWithTag("section-LiveTv").performClick()
        compose.settledUntil(5_000) { compose.onAllNodes(hasTestTag("feature-off-live_tv")).fetchSemanticsNodes().isNotEmpty() }
        compose.onNodeWithText("Live TV is off on this home.").assertExists()
        compose.onNodeWithText("Turn it on in Components").performClick()
        val started = requireNotNull(shadowOf(ApplicationProvider.getApplicationContext<Application>()).nextStartedActivity)
        assertEquals(Intent.ACTION_VIEW, started.action)
        assertEquals(home.home.client.home.resolve("/settings/components").toString(), started.dataString)
        assertFalse("nothing asks for channels while Live TV is off", "/api/channels/now" in home.paths)
    }

    @Test
    @Config(qualifiers = "ar-w411dp-h891dp")
    fun theAdminNoteIsInArabic() {
        home.adminWithOff(Feature.LIVE_TV)
        shell()
        compose.settledUntil(5_000) { sections("LiveTv") == 1 }
        compose.onNodeWithTag("section-LiveTv").performClick()
        compose.settledUntil(5_000) { compose.onAllNodes(hasText("البث المباشر متوقفة على الجهاز الرئيسي.")).fetchSemanticsNodes().isNotEmpty() }
        compose.onNodeWithText("شغّلها من صفحة المكوّنات").assertExists()
    }

    @Test
    fun theSectionShownWhenLiveTvGoesOffStaysUntilAnotherIsChosen() {
        LiveFixtures.install(home)
        shell()
        compose.settledUntil(5_000) { sections("LiveTv") == 1 }
        compose.onNodeWithTag("section-LiveTv").performClick()
        compose.waitForIdle()

        // turned off while the person is on it: the open screen is not torn down
        home.viewerWithOff(Feature.LIVE_TV)
        compose.waitForIdle()
        assertEquals(1, sections("LiveTv"))
        compose.onNodeWithTag("live-tv").assertExists()

        // the next navigation: it is gone
        compose.onNodeWithTag("section-Libraries").performClick()
        compose.waitForIdle()
        compose.onAllNodes(hasTestTag("section-LiveTv")).assertCountEquals(0)
    }

    @Test
    fun aChannelOpenedWhileLiveTvIsOffSaysItIsNotAvailableToAViewer() {
        home.viewerWithOff(Feature.LIVE_TV)
        compose.setLive(home) { LiveGate { Text("the channel", Modifier.testTag("channel")) } }
        compose.onNodeWithText("Live TV isn't available on this home.").assertExists()
        compose.onAllNodes(hasTestTag("channel")).assertCountEquals(0)
    }

    @Test
    fun aChannelOpenedWhileLiveTvIsOffExplainsItToAnAdmin() {
        home.adminWithOff(Feature.LIVE_TV)
        compose.setLive(home) { LiveGate { Text("the channel", Modifier.testTag("channel")) } }
        compose.onNodeWithTag("feature-off-live_tv").assertExists()
        compose.onNodeWithTag("feature-turn-on").assertExists()
    }

    @Test
    fun aChannelOpenWhenLiveTvGoesOffIsNotTornDown() {
        compose.setLive(home) { LiveGate { Text("the channel", Modifier.testTag("channel")) } }
        compose.onNodeWithTag("channel").assertExists()
        home.viewerWithOff(Feature.LIVE_TV)
        compose.waitForIdle()
        compose.onNodeWithTag("channel").assertExists()
    }

    @Test
    fun turnedBackOnTheSectionReturns() {
        LiveFixtures.install(home)
        home.viewerWithOff(Feature.LIVE_TV)
        shell()
        compose.waitForIdle()
        assertEquals(0, sections("LiveTv"))
        home.viewerWithOff()
        compose.settledUntil(5_000) { sections("LiveTv") == 1 }
    }
}
