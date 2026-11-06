package com.hamzalabs.maktaba.desktop.watch

import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.runComposeUiTest
import com.hamzalabs.maktaba.core.api.Capabilities
import com.hamzalabs.maktaba.core.api.Feature
import com.hamzalabs.maktaba.core.playback.QualityOption
import com.hamzalabs.maktaba.desktop.AudioOption
import com.hamzalabs.maktaba.desktop.ControlsActions
import com.hamzalabs.maktaba.desktop.ControlsBar
import com.hamzalabs.maktaba.desktop.InPlayer
import com.hamzalabs.maktaba.desktop.TestHome
import org.junit.Assert.assertEquals
import org.junit.Test

/** Story 55.08 T08 (subtitles gated) and the menus. */
@OptIn(ExperimentalTestApi::class)
class ControlsBarTest {
    private val subs = listOf(Rendition("SUBTITLES", "s", "Arabic", "ar", false, "ar.m3u8"))

    private fun androidx.compose.ui.test.ComposeUiTest.bar(features: Capabilities, on: ControlsActions = ControlsActions(), home: TestHome) {
        setContent {
            InPlayer(home.home()) {
                ControlsBar(
                    playing = true, positionMs = 1_000, durationMs = 60_000, chapters = emptyList(), volume = 1f, muted = false,
                    features = features, homeUrl = "https://home.test", fullScreen = false,
                    subtitles = subs, subtitle = null,
                    audioOptions = listOf(AudioOption("Arabic", "ar", rendition = "Arabic"), AudioOption("English", "en", rendition = "English")),
                    audio = null,
                    qualities = listOf(QualityOption.Auto, QualityOption(1080, 6000), QualityOption(480, 1200)),
                    quality = QualityOption.Auto,
                    on = on,
                )
            }
        }
    }

    @Test
    fun subtitlesOffHideTheControlFromAViewer() = TestHome().use { fake ->
        runComposeUiTest {
            bar(TestHome.caps(Feature.SUBTITLES), home = fake)
            assertEquals(0, onAllNodes(hasTestTag("subtitles")).fetchSemanticsNodes().size)
            onNodeWithTag("audio").assertExists()
        }
    }

    @Test
    fun anAdminSeesTheControlWithTheOffNote() = TestHome().use { fake ->
        runComposeUiTest {
            bar(TestHome.caps(Feature.SUBTITLES, admin = true), home = fake)
            onNodeWithTag("subtitles").performClick()
            onNodeWithText("Subtitles are off on this home.").assertExists()
        }
    }

    @Test
    fun choosingAQualityAndASubtitle() = TestHome().use { fake ->
        val chosen = mutableListOf<Any?>()
        runComposeUiTest {
            bar(Capabilities.AllOn, ControlsActions(quality = { chosen += it }, subtitle = { chosen += it }), fake)
            onNodeWithTag("quality").performClick()
            onNodeWithText("480p").performClick()
            onNodeWithTag("subtitles").performClick()
            onNodeWithTag("subtitles-0").performClick()
            assertEquals(listOf(QualityOption(480, 1200), subs[0]), chosen)
        }
    }
}
