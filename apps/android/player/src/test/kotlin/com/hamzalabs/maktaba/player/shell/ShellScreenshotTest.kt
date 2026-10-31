package com.hamzalabs.maktaba.player.shell

import androidx.activity.ComponentActivity
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.github.takahirom.roborazzi.RoborazziOptions
import com.github.takahirom.roborazzi.captureRoboImage
import com.hamzalabs.maktaba.player.harness.FakeHome
import com.hamzalabs.maktaba.player.harness.Window
import com.hamzalabs.maktaba.player.harness.setShell
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

/**
 * The shell's screenshots (NFR-20): dark, English and Arabic, on a phone (the bar), a foldable
 * open flat (the rail) and a large tablet (the drawer), every section on and Libraries shown,
 * against the fake home. `recordRoborazziPhoneDebug` writes the references under
 * `src/test/screenshots`; CI's `verifyRoborazziPhoneDebug` fails when one no longer matches.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class ShellScreenshotTest {
    @get:Rule(order = 0) val home = FakeHome()
    @get:Rule(order = 1) val compose = createAndroidComposeRule<ComponentActivity>()

    private fun shoot(window: Window, name: String) {
        compose.setShell(home, window = window)
        compose.onNodeWithTag("section-Libraries").performClick()
        compose.waitUntil(5_000) { compose.onAllNodes(hasText("Lion of the Desert")).fetchSemanticsNodes().isNotEmpty() }
        compose.waitForIdle()
        compose.onRoot().captureRoboImage("src/test/screenshots/$name.png", roborazziOptions = options)
    }

    @Test @Config(qualifiers = "w411dp-h891dp-xxhdpi")
    fun phoneEnglish() = shoot(Window.Phone, "shell_phone_dark_en")

    @Test @Config(qualifiers = "ar-w411dp-h891dp-xxhdpi")
    fun phoneArabic() = shoot(Window.Phone, "shell_phone_dark_ar")

    @Test @Config(qualifiers = "w673dp-h841dp-xhdpi")
    fun foldableEnglish() = shoot(Window.Foldable, "shell_foldable_dark_en")

    @Test @Config(qualifiers = "ar-w673dp-h841dp-xhdpi")
    fun foldableArabic() = shoot(Window.Foldable, "shell_foldable_dark_ar")

    @Test @Config(qualifiers = "w1280dp-h800dp-xhdpi")
    fun tabletEnglish() = shoot(Window.Tablet, "shell_tablet_dark_en")

    @Test @Config(qualifiers = "ar-w1280dp-h800dp-xhdpi")
    fun tabletArabic() = shoot(Window.Tablet, "shell_tablet_dark_ar")

    private companion object {
        // text is drawn by the host's font rasteriser: a reference recorded on a Mac still matches
        // on CI's Linux within a small share of pixels
        val options = RoborazziOptions(compareOptions = RoborazziOptions.CompareOptions(changeThreshold = 0.02f))
    }
}
