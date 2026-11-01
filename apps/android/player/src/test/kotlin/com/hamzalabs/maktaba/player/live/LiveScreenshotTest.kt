package com.hamzalabs.maktaba.player.live

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
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

/**
 * Live TV's screenshots (NFR-20): Now, the Guide and the live player, dark, English and Arabic,
 * on a phone and a large tablet, against the fake home at [NOW] in UTC. `recordRoborazziPhoneDebug`
 * writes the references under `src/test/screenshots`; CI's `verifyRoborazziPhoneDebug` checks them.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class LiveScreenshotTest {
    @get:Rule(order = 0) val home = FakeHome()
    @get:Rule(order = 1) val compose = createAndroidComposeRule<ComponentActivity>()

    private fun waitForTag(tag: String) =
        compose.waitUntil(5_000) { compose.onAllNodes(hasTestTag(tag)).fetchSemanticsNodes().isNotEmpty() }

    private fun shoot(window: Window, screen: String) {
        LiveFixtures.install(home)
        compose.setLive(home, FakeLiveEngine(fetch = false), window)
        compose.onNodeWithTag("section-LiveTv").performClick()
        waitForTag("channel-c1")
        when (screen) {
            "guide" -> {
                compose.onNodeWithTag("tab-guide").performClick()
                waitForTag("guide-row-c1")
            }
            "player" -> {
                compose.onNodeWithTag("channel-c1").performClick()
                waitForTag("live-controls")
                waitForTag("live-channel-name")
            }
        }
        compose.waitForIdle()
        val form = if (window == Window.Tablet) "tablet" else "phone"
        val lang = if (compose.activity.resources.configuration.locales[0].language == "ar") "ar" else "en"
        compose.onRoot().captureRoboImage("src/test/screenshots/live_${screen}_${form}_dark_$lang.png", roborazziOptions = options)
    }

    @Test @Config(qualifiers = "w411dp-h891dp-xxhdpi")
    fun nowPhoneEnglish() = shoot(Window.Phone, "now")

    @Test @Config(qualifiers = "ar-w411dp-h891dp-xxhdpi")
    fun nowPhoneArabic() = shoot(Window.Phone, "now")

    @Test @Config(qualifiers = "w1280dp-h800dp-xhdpi")
    fun nowTabletEnglish() = shoot(Window.Tablet, "now")

    @Test @Config(qualifiers = "ar-w1280dp-h800dp-xhdpi")
    fun nowTabletArabic() = shoot(Window.Tablet, "now")

    @Test @Config(qualifiers = "w411dp-h891dp-xxhdpi")
    fun guidePhoneEnglish() = shoot(Window.Phone, "guide")

    @Test @Config(qualifiers = "ar-w411dp-h891dp-xxhdpi")
    fun guidePhoneArabic() = shoot(Window.Phone, "guide")

    @Test @Config(qualifiers = "w1280dp-h800dp-xhdpi")
    fun guideTabletEnglish() = shoot(Window.Tablet, "guide")

    @Test @Config(qualifiers = "ar-w1280dp-h800dp-xhdpi")
    fun guideTabletArabic() = shoot(Window.Tablet, "guide")

    @Test @Config(qualifiers = "w411dp-h891dp-xxhdpi")
    fun playerPhoneEnglish() = shoot(Window.Phone, "player")

    @Test @Config(qualifiers = "ar-w411dp-h891dp-xxhdpi")
    fun playerPhoneArabic() = shoot(Window.Phone, "player")

    @Test @Config(qualifiers = "w1280dp-h800dp-xhdpi")
    fun playerTabletEnglish() = shoot(Window.Tablet, "player")

    @Test @Config(qualifiers = "ar-w1280dp-h800dp-xhdpi")
    fun playerTabletArabic() = shoot(Window.Tablet, "player")

    private companion object {
        val options = RoborazziOptions(compareOptions = RoborazziOptions.CompareOptions(changeThreshold = 0.02f))
    }
}
