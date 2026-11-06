package com.hamzalabs.maktaba.desktop.shell

import androidx.compose.foundation.layout.size
import androidx.compose.ui.Modifier
import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.runComposeUiTest
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.api.Feature
import com.hamzalabs.maktaba.desktop.InPlayer
import com.hamzalabs.maktaba.desktop.Lang
import com.hamzalabs.maktaba.desktop.TestHome
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/** Story 55.01 T05 and 55.02's rail, off screen. */
@OptIn(ExperimentalTestApi::class)
class ShellTest {
    @Test
    fun inArabicTheRailIsOnTheRightWithFiveArabicLabels() = TestHome().use { fake ->
        fake.answer("GET /api/home", """{"continue_watching":[]}""")
        runComposeUiTest {
            setContent {
                InPlayer(fake.home(), Lang.Ar) {
                    androidx.compose.foundation.layout.Box(Modifier.size(1280.dp, 800.dp)) { Shell(fake.home()) }
                }
            }
            for (label in listOf("الرئيسية", "المكتبات", "البث المباشر", "بحث", "الملف الشخصي")) onNodeWithText(label).assertIsDisplayed()
            val rail = onNodeWithTag("rail").fetchSemanticsNode().boundsInRoot
            assertTrue("rail on the right: $rail", rail.left > 600f)
        }
    }

    @Test
    fun aViewerWithLiveTvOffHasNoLiveTvOnTheRail() = TestHome().use { fake ->
        fake.features.value = TestHome.caps(Feature.LIVE_TV)
        fake.answer("GET /api/home", """{"continue_watching":[]}""")
        val home = fake.home()
        runComposeUiTest {
            setContent { InPlayer(home) { Shell(home) } }
            onNodeWithTag("rail-Home").assertIsDisplayed()
            assertEquals(0, onAllNodes(androidx.compose.ui.test.hasTestTag("rail-LiveTv")).fetchSemanticsNodes().size)
            onNodeWithTag("rail-Search").performClick()
            onNodeWithTag("search-field").assertIsDisplayed()
        }
    }

    @Test
    fun backReturnsToThePageBefore() = TestHome().use { fake ->
        fake.answer("GET /api/home", """{"continue_watching":[]}""")
        val home = fake.home()
        home.nav.push(Route.Series("missing"))
        runComposeUiTest {
            setContent { InPlayer(home) { Shell(home) } }
            onNodeWithTag("back").performClick()
            waitForIdle()
            assertEquals(Route.Home, home.nav.current)
        }
    }
}
