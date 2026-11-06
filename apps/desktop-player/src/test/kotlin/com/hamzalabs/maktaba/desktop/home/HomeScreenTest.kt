package com.hamzalabs.maktaba.desktop.home

import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.runComposeUiTest
import com.hamzalabs.maktaba.core.api.Feature
import com.hamzalabs.maktaba.core.browse.KEY_FOR_YOU
import com.hamzalabs.maktaba.desktop.InPlayer
import com.hamzalabs.maktaba.desktop.Lang
import com.hamzalabs.maktaba.desktop.TestHome
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/** Story 55.03 T06 and T07, off screen. */
@OptIn(ExperimentalTestApi::class)
class HomeScreenTest {
    private val homeBody = """{"continue_watching":[{"id":"cw","title":"Fiqh 7","filename":"cw.mp4","duration_sec":2400,"position_sec":600}]}"""
    private val recsBody = """{"rails":[{"id":"fy","reason_kind":"for_you","items":[{"video_id":"r1"}]}]}"""

    private fun loaded(fake: TestHome, recsOn: Boolean): HomeState = HomeState(fake.client, { recsOn }, { null }, {}).also { runBlocking { it.load() } }

    @Test
    fun recommendationsOffHideForYouFromAViewerAndExplainItToAnAdmin() = TestHome().use { fake ->
        fake.answer("GET /api/home", homeBody)
        fake.answer("GET /api/recommendations", recsBody)
        fake.features.value = TestHome.caps(Feature.RECOMMENDATIONS)
        val viewer = loaded(fake, recsOn = false)
        runComposeUiTest {
            setContent { InPlayer(fake.home()) { HomeContent(viewer, fake.home(), fake.features.value) } }
            onNodeWithTag("row-continue").assertExists()
            assertEquals(0, onAllNodes(hasTestTag("row-$KEY_FOR_YOU")).fetchSemanticsNodes().size)
            assertEquals(0, onAllNodes(hasTestTag("feature-off-recommendations")).fetchSemanticsNodes().size)
        }
        fake.features.value = TestHome.caps(Feature.RECOMMENDATIONS, admin = true)
        runComposeUiTest {
            setContent { InPlayer(fake.home(admin = true), Lang.Ar) { HomeContent(viewer, fake.home(admin = true), fake.features.value) } }
            onNodeWithTag("feature-off-recommendations").assertExists()
            onNodeWithText("الاقتراحات متوقفة على الجهاز الرئيسي.").assertExists()
            onNodeWithText("تابع المشاهدة").assertExists()
        }
    }

    @Test
    fun continueWatchingPlaysFromWhereItWasLeft() = TestHome().use { fake ->
        fake.answer("GET /api/home", homeBody)
        val state = loaded(fake, recsOn = false)
        val home = fake.home()
        runComposeUiTest {
            setContent { InPlayer(home) { HomeContent(state, home, fake.features.value) } }
            onNodeWithTag("card-cw").performClick()
            assertEquals("cw", fake.played.single().videoId)
            assertNull("where the person left it", fake.played.single().startSec)
        }
    }

    @Test
    fun anEmptyHomeExplainsItselfToAViewerAndToAnAdmin() = TestHome().use { fake ->
        fake.answer("GET /api/home", "{}")
        val state = loaded(fake, recsOn = false)
        runComposeUiTest {
            setContent { InPlayer(fake.home()) { HomeContent(state, fake.home(), fake.features.value) } }
            onNodeWithText("Nothing to watch yet").assertExists()
            onNodeWithText("Open Libraries to see the videos you may watch.").assertExists()
        }
        runComposeUiTest {
            setContent { InPlayer(fake.home(admin = true)) { HomeContent(state, fake.home(admin = true), fake.features.value) } }
            onNodeWithText("Add a library on the home's web page and scan it; its videos show here.").assertExists()
        }
    }
}
