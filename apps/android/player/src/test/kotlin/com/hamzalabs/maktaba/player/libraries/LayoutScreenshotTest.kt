package com.hamzalabs.maktaba.player.libraries

import androidx.activity.ComponentActivity
import androidx.compose.material3.adaptive.WindowAdaptiveInfo
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.github.takahirom.roborazzi.RoborazziOptions
import com.github.takahirom.roborazzi.captureRoboImage
import com.hamzalabs.maktaba.player.harness.FakeHome
import com.hamzalabs.maktaba.player.harness.Window
import com.hamzalabs.maktaba.player.harness.uprightHinge
import com.hamzalabs.maktaba.player.harness.folded
import com.hamzalabs.maktaba.player.harness.info
import com.hamzalabs.maktaba.player.harness.settledUntil
import com.hamzalabs.maktaba.player.harness.acrossHinge
import com.hamzalabs.maktaba.player.shell.PlayerSection
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

/**
 * The shell and Libraries laid out by the window and its fold (FR-8), every section on, dark,
 * English and Arabic: a foldable open flat held sideways (841 dp) and a large tablet with the
 * collapsed rail and the list beside the page; the tablet's rail expanded over the content; a
 * foldable half folded like a book (the rail and the list on the start page, the page on the
 * other) and on a table (the page on the top half, the list and the bar on the bottom one).
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class LayoutScreenshotTest {
    @get:Rule(order = 0) val home = FakeHome()
    @get:Rule(order = 1) val compose = createAndroidComposeRule<ComponentActivity>()

    @Before fun routes() = home.answerLibraries()

    // a picture waits for every answer from the fake home, not only the first card's
    @Before fun idleOnTheHome() = compose.registerIdlingResource(home.idling)

    private fun exists(tag: String) = compose.onAllNodes(hasTestTag(tag)).fetchSemanticsNodes().isNotEmpty()

    private val lang get() = if (compose.activity.resources.configuration.locales[0].language == "ar") "ar" else "en"
    private val widthPx get() = compose.activity.resources.displayMetrics.widthPixels
    private val heightPx get() = compose.activity.resources.displayMetrics.heightPixels

    private fun open(window: Window, info: WindowAdaptiveInfo = window.info()) {
        compose.setLibraries(home, window, sections = PlayerSection.entries, info = info)
        compose.settledUntil(10_000) { exists("section-Libraries") }
        compose.onNodeWithTag("section-Libraries").performClick()
        compose.settledUntil(10_000) { exists("libraries-detail") && exists("video-v1") }
    }

    private fun shoot(name: String) {
        compose.waitForIdle()
        compose.onRoot().captureRoboImage("src/test/screenshots/layout_${name}_dark_$lang.png", roborazziOptions = options)
    }

    private fun flatFoldable() { open(Window.FoldableWide); shoot("flat_foldable") }
    private fun flatTablet() { open(Window.Tablet); shoot("flat_tablet") }
    private fun railExpanded() {
        open(Window.Tablet)
        compose.onNodeWithTag("rail-toggle").performClick()
        compose.settledUntil(5_000) { exists("rail-scrim") }
        shoot("rail_expanded_tablet")
    }
    private fun book() {
        open(Window.FoldableWide, Window.FoldableWide.folded(uprightHinge(widthPx, heightPx), vertical = true))
        shoot("book_foldable")
    }
    private fun tabletop() {
        open(Window.Foldable, Window.Foldable.folded(acrossHinge(widthPx, heightPx), vertical = false))
        shoot("tabletop_foldable")
    }

    @Test @Config(qualifiers = "w841dp-h673dp-xhdpi") fun flatFoldableEnglish() = flatFoldable()
    @Test @Config(qualifiers = "ar-w841dp-h673dp-xhdpi") fun flatFoldableArabic() = flatFoldable()
    @Test @Config(qualifiers = "w1280dp-h800dp-xhdpi") fun flatTabletEnglish() = flatTablet()
    @Test @Config(qualifiers = "ar-w1280dp-h800dp-xhdpi") fun flatTabletArabic() = flatTablet()
    @Test @Config(qualifiers = "w1280dp-h800dp-xhdpi") fun railExpandedEnglish() = railExpanded()
    @Test @Config(qualifiers = "ar-w1280dp-h800dp-xhdpi") fun railExpandedArabic() = railExpanded()
    @Test @Config(qualifiers = "w841dp-h673dp-xhdpi") fun bookEnglish() = book()
    @Test @Config(qualifiers = "ar-w841dp-h673dp-xhdpi") fun bookArabic() = book()
    @Test @Config(qualifiers = "w673dp-h841dp-xhdpi") fun tabletopEnglish() = tabletop()
    @Test @Config(qualifiers = "ar-w673dp-h841dp-xhdpi") fun tabletopArabic() = tabletop()

    private companion object {
        // text is drawn by the host's font rasteriser: a reference recorded on a Mac still matches
        // on CI's Linux within a small share of pixels
        val options = RoborazziOptions(compareOptions = RoborazziOptions.CompareOptions(changeThreshold = 0.02f))
    }
}
