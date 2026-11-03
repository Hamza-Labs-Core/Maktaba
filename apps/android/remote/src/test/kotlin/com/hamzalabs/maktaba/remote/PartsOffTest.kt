package com.hamzalabs.maktaba.remote

import android.app.Application
import android.content.Intent
import androidx.activity.ComponentActivity
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.core.api.Capabilities
import com.hamzalabs.maktaba.core.api.FeatureOff
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config

/**
 * Story 51.11: Machines (the home and its processes) says how many parts of the home are off,
 * with the way to its Components page, when the home's admin answer lists any.
 */
@RunWith(AndroidJUnit4::class)
@Config(qualifiers = "w411dp-h923dp-420dpi")
class PartsOffTest {
    @get:Rule(order = 0) val home = FakeRemoteHome()
    @get:Rule(order = 1) val compose = createAndroidComposeRule<ComponentActivity>()

    private val threeOff = Capabilities(
        off = setOf("live_tv", "semantic_search"),
        admin = true,
        adminOff = mapOf(
            "live_tv" to FeatureOff(listOf("livetv.slots", "livetv.inprocess"), "switched_off"),
            "semantic_search" to FeatureOff(listOf("search.semantic"), "switched_off"),
        ),
    )

    private fun machines() {
        home.answer("/api/cluster") { FakeRemoteHome.json("""{"home":{"name":"Darwish home","version":"0.3.62"},"nodes":[]}""") }
        compose.setContent { RemoteForTest(home, RemoteWindow.PixelFoldCover.info) }
        compose.onNodeWithTag("place-Machines").performClick()
        compose.settledUntil { compose.onAllNodes(hasText("Darwish home"), useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty() }
    }

    @Test
    fun threePartsOffSayItAndOpenComponents() {
        home.features.value = threeOff
        machines()
        compose.settledUntil { compose.has("parts-off") }
        compose.onNodeWithTag("parts-off").assertExistsWith("3 parts are off")
        compose.onNodeWithTag("parts-off").performClick()
        val started = requireNotNull(shadowOf(ApplicationProvider.getApplicationContext<Application>()).nextStartedActivity)
        assertEquals(Intent.ACTION_VIEW, started.action)
        assertEquals(home.remote.client.home.resolve("/settings/components").toString(), started.dataString)
    }

    @Test
    @Config(qualifiers = "ar-w411dp-h923dp-420dpi")
    fun inArabic() {
        home.features.value = threeOff
        machines()
        compose.settledUntil { compose.has("parts-off") }
        compose.onNodeWithTag("parts-off").assertExistsWith("٣ أجزاء متوقفة") // Arabic digits, as the locale writes them
    }

    @Test
    fun nothingOffSaysNothing() {
        home.features.value = Capabilities(admin = true)
        machines()
        assertFalse(compose.has("parts-off"))
    }

    @Test
    fun beforeTheHomeAnswersSaysNothing() {
        machines()
        assertFalse(compose.has("parts-off"))
    }

    private fun androidx.compose.ui.test.SemanticsNodeInteraction.assertExistsWith(text: String) {
        val texts = fetchSemanticsNode().config.getOrElse(androidx.compose.ui.semantics.SemanticsProperties.Text) { emptyList() }.map { it.text }
        assertTrue("expected \"$text\" in $texts", texts.any { it == text })
    }
}
