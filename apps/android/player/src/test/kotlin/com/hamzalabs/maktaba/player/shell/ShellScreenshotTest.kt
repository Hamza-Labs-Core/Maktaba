package com.hamzalabs.maktaba.player.shell

import androidx.activity.ComponentActivity
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.github.takahirom.roborazzi.RoborazziOptions
import com.github.takahirom.roborazzi.captureRoboImage
import com.hamzalabs.maktaba.player.harness.FakeHome
import com.hamzalabs.maktaba.player.harness.assertBarLabelsAligned
import com.hamzalabs.maktaba.player.harness.info
import com.hamzalabs.maktaba.player.harness.Window
import com.hamzalabs.maktaba.player.harness.setShell
import com.hamzalabs.maktaba.player.harness.settledUntil
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

/**
 * The shell's screenshots (NFR-20): dark, English and Arabic, on a phone (the bar), a foldable
 * open flat (the rail) and a large tablet (the collapsible rail), every section on and Libraries shown,
 * against the fake home. `recordRoborazziDebug` writes the references under
 * `src/test/screenshots`; CI's `verifyRoborazziDebug` fails when one no longer matches.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class ShellScreenshotTest {
    @get:Rule(order = 0) val home = FakeHome()
    @get:Rule(order = 1) val compose = createAndroidComposeRule<ComponentActivity>()

    private fun shoot(window: Window, name: String, fontScale: Float = 1f) {
        // the shell's chrome is what these show: Libraries with no library (no posters to load)
        home.answer("/api/libraries/summary") { FakeHome.json("""{"items":[]}""") }
        compose.setShell(home, window = window, fontScale = fontScale)
        compose.onNodeWithTag("section-Libraries").performClick()
        val empty = compose.activity.getString(com.hamzalabs.maktaba.player.R.string.libraries_empty)
        compose.settledUntil(5_000) { compose.onAllNodes(hasText(empty)).fetchSemanticsNodes().isNotEmpty() }
        compose.waitForIdle()
        if (navigationFor(window.info()) == ShellNavigation.Bar) {
            compose.assertBarLabelsAligned(
                PlayerSection.entries.associate { "section-${it.name}" to compose.activity.getString(it.label) },
                selected = "section-Libraries",
            )
        }
        compose.onRoot().captureRoboImage("src/test/screenshots/$name.png", roborazziOptions = options)
    }

    // closed foldables (cover screens): the bar's five labels on ~65-80 dp each

    @Test @Config(qualifiers = "w344dp-h882dp-420dpi")
    fun foldCoverEnglish() = shoot(Window.FoldCover, "shell_foldcover_dark_en")

    @Test @Config(qualifiers = "ar-w344dp-h882dp-420dpi")
    fun foldCoverArabic() = shoot(Window.FoldCover, "shell_foldcover_dark_ar")

    @Test @Config(qualifiers = "w344dp-h882dp-420dpi")
    fun foldCoverEnglishLargeText() = shoot(Window.FoldCover, "shell_foldcover_dark_en_font130", 1.3f)

    @Test @Config(qualifiers = "ar-w344dp-h882dp-420dpi")
    fun foldCoverArabicLargestText() = shoot(Window.FoldCover, "shell_foldcover_dark_ar_font200", 2f)

    @Test @Config(qualifiers = "w393dp-h916dp-440dpi")
    fun razrFoldCoverEnglish() = shoot(Window.RazrFoldCover, "shell_razrfoldcover_dark_en")

    @Test @Config(qualifiers = "ar-w393dp-h916dp-440dpi")
    fun razrFoldCoverArabicLargeText() = shoot(Window.RazrFoldCover, "shell_razrfoldcover_dark_ar_font130", 1.3f)

    @Test @Config(qualifiers = "w411dp-h923dp-420dpi")
    fun pixelFoldCoverEnglish() = shoot(Window.PixelFoldCover, "shell_pixelfoldcover_dark_en")

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
