package com.hamzalabs.maktaba.player.libraries

import androidx.activity.ComponentActivity
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.github.takahirom.roborazzi.RoborazziOptions
import com.github.takahirom.roborazzi.captureRoboImage
import com.hamzalabs.maktaba.player.harness.FakeHome
import com.hamzalabs.maktaba.player.harness.Window
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

/**
 * The Libraries section's screenshots (NFR-20): dark, English and Arabic; the libraries, a
 * library and a series on a phone, and the list beside a library's page on a tablet, against
 * the fake home with drawn posters. `recordRoborazziPhoneDebug` writes the references under
 * `src/test/screenshots`; CI's `verifyRoborazziPhoneDebug` fails when one no longer matches.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class LibrariesScreenshotTest {
    @get:Rule(order = 0) val home = FakeHome()
    @get:Rule(order = 1) val compose = createAndroidComposeRule<ComponentActivity>()

    @Before fun routes() = home.answerLibraries()

    private fun waitForTag(tag: String) =
        compose.waitUntil(5_000) { compose.onAllNodes(hasTestTag(tag)).fetchSemanticsNodes().isNotEmpty() }

    private val lang get() = if (compose.activity.resources.configuration.locales[0].language == "ar") "ar" else "en"

    private fun shoot(name: String) {
        compose.waitForIdle()
        compose.onRoot().captureRoboImage("src/test/screenshots/libraries_${name}_dark_$lang.png", roborazziOptions = options)
    }

    private fun root() {
        compose.setLibraries(home)
        waitForTag("library-l2")
        shoot("root_phone")
    }

    private fun library() {
        compose.setLibraries(home)
        waitForTag("library-l1")
        compose.onNodeWithTag("library-l1").performClick()
        waitForTag("video-v2")
        shoot("library_phone")
    }

    private fun series() {
        compose.setLibraries(home)
        waitForTag("tab-series")
        compose.onNodeWithTag("tab-series").performClick()
        waitForTag("series-s1")
        compose.onNodeWithTag("series-s1").performClick()
        waitForTag("episode-v3")
        shoot("series_phone")
    }

    private fun tablet() {
        compose.setLibraries(home, Window.Tablet)
        waitForTag("video-v2")
        shoot("listdetail_tablet")
    }

    @Test @Config(qualifiers = "w411dp-h891dp-xxhdpi") fun rootEnglish() = root()
    @Test @Config(qualifiers = "ar-w411dp-h891dp-xxhdpi") fun rootArabic() = root()
    @Test @Config(qualifiers = "w411dp-h891dp-xxhdpi") fun libraryEnglish() = library()
    @Test @Config(qualifiers = "ar-w411dp-h891dp-xxhdpi") fun libraryArabic() = library()
    @Test @Config(qualifiers = "w411dp-h891dp-xxhdpi") fun seriesEnglish() = series()
    @Test @Config(qualifiers = "ar-w411dp-h891dp-xxhdpi") fun seriesArabic() = series()
    @Test @Config(qualifiers = "w1280dp-h800dp-xhdpi") fun tabletEnglish() = tablet()
    @Test @Config(qualifiers = "ar-w1280dp-h800dp-xhdpi") fun tabletArabic() = tablet()

    private companion object {
        // text is drawn by the host's font rasteriser: a reference recorded on a Mac still matches
        // on CI's Linux within a small share of pixels
        val options = RoborazziOptions(compareOptions = RoborazziOptions.CompareOptions(changeThreshold = 0.02f))
    }
}
