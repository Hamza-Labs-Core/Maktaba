package com.hamzalabs.maktaba.player.home

import androidx.activity.ComponentActivity
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onRoot
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.github.takahirom.roborazzi.RoborazziOptions
import com.github.takahirom.roborazzi.captureRoboImage
import com.hamzalabs.maktaba.player.harness.FakeHome
import com.hamzalabs.maktaba.player.harness.Window
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

/**
 * Home's screenshots (48.26, NFR-20): dark, English and Arabic, on a phone (the bar) and a large
 * tablet (the drawer), against the mock home, plus the empty home on a phone. `recordRoborazziPhoneDebug`
 * writes the references under `src/test/screenshots`; CI's `verifyRoborazziPhoneDebug` fails
 * when one no longer matches.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class HomeScreenshotTest {
    @get:Rule(order = 0) val home = FakeHome()
    @get:Rule(order = 1) val compose = createAndroidComposeRule<ComponentActivity>()

    private fun shoot(window: Window, name: String) {
        HomeFixtures.install(home)
        compose.setHome(home, window = window)
        compose.waitUntil(5_000) { compose.onAllNodes(hasTestTag("hero")).fetchSemanticsNodes().isNotEmpty() }
        compose.waitUntil(5_000) { compose.onAllNodes(hasTestTag("row-$KEY_CONTINUE")).fetchSemanticsNodes().isNotEmpty() }
        compose.waitForIdle()
        compose.onRoot().captureRoboImage("src/test/screenshots/$name.png", roborazziOptions = options)
    }

    private fun shootEmpty(name: String) {
        HomeFixtures.install(home, homeBody = HomeFixtures.EMPTY_HOME, recs = HomeFixtures.EMPTY_RECS, now = null)
        compose.setHome(home)
        compose.waitUntil(5_000) { compose.onAllNodes(hasTestTag("home-empty")).fetchSemanticsNodes().isNotEmpty() }
        compose.waitForIdle()
        compose.onRoot().captureRoboImage("src/test/screenshots/$name.png", roborazziOptions = options)
    }

    @Test @Config(qualifiers = "w411dp-h891dp-xxhdpi")
    fun phoneEnglish() = shoot(Window.Phone, "home_phone_dark_en")

    @Test @Config(qualifiers = "ar-w411dp-h891dp-xxhdpi")
    fun phoneArabic() = shoot(Window.Phone, "home_phone_dark_ar")

    @Test @Config(qualifiers = "w1280dp-h800dp-xhdpi")
    fun tabletEnglish() = shoot(Window.Tablet, "home_tablet_dark_en")

    @Test @Config(qualifiers = "ar-w1280dp-h800dp-xhdpi")
    fun tabletArabic() = shoot(Window.Tablet, "home_tablet_dark_ar")

    @Test @Config(qualifiers = "w411dp-h891dp-xxhdpi")
    fun emptyPhoneEnglish() = shootEmpty("home_empty_phone_dark_en")

    @Test @Config(qualifiers = "ar-w411dp-h891dp-xxhdpi")
    fun emptyPhoneArabic() = shootEmpty("home_empty_phone_dark_ar")

    private companion object {
        // text is drawn by the host's font rasteriser: a reference recorded on a Mac still matches
        // on CI's Linux within a small share of pixels
        val options = RoborazziOptions(compareOptions = RoborazziOptions.CompareOptions(changeThreshold = 0.02f))
    }
}
