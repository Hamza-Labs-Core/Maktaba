package com.hamzalabs.maktaba.player.shell

import androidx.activity.ComponentActivity
import androidx.compose.material3.Surface
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performScrollToIndex
import androidx.compose.ui.unit.Density
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.core.ui.MaktabaTheme
import com.hamzalabs.maktaba.core.ui.ThemeMode
import com.hamzalabs.maktaba.player.harness.ListScreens
import com.hamzalabs.maktaba.player.harness.Window
import com.hamzalabs.maktaba.player.harness.assertBarLabelsAligned
import com.hamzalabs.maktaba.player.harness.info
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config

/**
 * Closed, open, closed again while the app runs (a foldable's cover screen, then its inner one,
 * at text size 1.3): the scaffold swaps the bar for the collapsible rail and back, and the bar
 * comes back as it was (its labels one line and lined up, nothing of the rail's), with the
 * section's list where it was scrolled to.
 */
@RunWith(AndroidJUnit4::class)
@Config(qualifiers = "w344dp-h882dp-420dpi")
class ShellFoldTest {
    @get:Rule val compose = createAndroidComposeRule<ComponentActivity>()

    private val labels: Map<String, String>
        get() = PlayerSection.entries.associate { "section-${it.name}" to compose.activity.getString(it.label) }

    private fun assertNavigation(expected: ShellNavigation) {
        compose.onNode(SemanticsMatcher.expectValue(ShellNavigationKey, expected)).assertExists()
    }

    private fun has(tag: String) = compose.onAllNodes(hasTestTag(tag)).fetchSemanticsNodes().isNotEmpty()

    @Test
    fun foldUnfoldFoldKeepsTheBarAlignedAndTheListWhereItWas() {
        var info by mutableStateOf(Window.FoldCover.info())
        compose.setContent {
            val density = LocalDensity.current
            CompositionLocalProvider(LocalDensity provides Density(density.density, 1.3f)) {
                MaktabaTheme(ThemeMode.Dark) { Surface { PlayerShell(adaptiveInfo = info, screens = ListScreens) } }
            }
        }
        compose.waitForIdle()
        assertNavigation(ShellNavigation.Bar)
        compose.assertBarLabelsAligned(labels)
        compose.onNodeWithTag("list").performScrollToIndex(30)
        compose.waitForIdle()

        // open flat, held sideways: the collapsible rail
        info = Window.FoldableWide.info()
        compose.waitForIdle()
        assertNavigation(ShellNavigation.CollapsibleRail)
        assertTrue("open: the same row", has("row-31"))

        // closed again: the bar as it was
        info = Window.FoldCover.info()
        compose.waitForIdle()
        assertNavigation(ShellNavigation.Bar)
        assertTrue("closed: the same row", has("row-31"))
        assertTrue("closed: not from the top", !has("row-1"))
        compose.assertBarLabelsAligned(labels)
    }
}
