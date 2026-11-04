package com.hamzalabs.maktaba.remote

import androidx.activity.ComponentActivity
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.onRoot
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.github.takahirom.roborazzi.RoborazziOptions
import com.github.takahirom.roborazzi.captureRoboImage
import org.junit.Assert.assertFalse
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

/**
 * Needs attention on foldables' cover screens (a report on build 0.3.62: a Razr Fold
 * closed), dark, English and Arabic, at font scales 1.0, 1.3 and 2.0, against groups shaped
 * like a home's. Each shot also checks the bar: one-line labels, lined up, inside the screen; and the
 * rows: no grouping key in a title, no backslash-n. `recordRoborazziDebug` writes the references
 * under `src/test/screenshots`; CI's `verifyRoborazziDebug` fails when one no longer matches.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class AttentionScreenshotTest {
    @get:Rule(order = 0) val home = FakeRemoteHome()
    @get:Rule(order = 1) val compose = createAndroidComposeRule<ComponentActivity>()

    private fun shoot(window: RemoteWindow, name: String, fontScale: Float = 1f) {
        compose.setContent { RemoteForTest(home, window.info, fontScale) }
        compose.settledUntil { compose.has("group-fingerprint:nas_partial_read") }
        compose.waitForIdle()
        compose.assertBarLabelsAligned(Place.entries.associate { "place-${it.name}" to compose.activity.getString(it.short) })
        val shown = compose.onAllNodes(androidx.compose.ui.test.hasTestTag("group-title"), useUnmergedTree = true)
            .fetchSemanticsNodes().flatMap { n -> n.config[androidx.compose.ui.semantics.SemanticsProperties.Text].map { it.text } }
        shown.forEach { t -> assertFalse("a key in the title: $t", looksLikeKey(t) || t.contains("\\n")) }
        compose.onRoot().captureRoboImage("src/test/screenshots/$name.png", roborazziOptions = options)
    }

    @Test @Config(qualifiers = "w344dp-h882dp-420dpi")
    fun foldCoverEnglish() = shoot(RemoteWindow.FoldCover, "attention_foldcover_dark_en")

    @Test @Config(qualifiers = "ar-w344dp-h882dp-420dpi")
    fun foldCoverArabic() = shoot(RemoteWindow.FoldCover, "attention_foldcover_dark_ar")

    @Test @Config(qualifiers = "w344dp-h882dp-420dpi")
    fun foldCoverEnglishLargeText() = shoot(RemoteWindow.FoldCover, "attention_foldcover_dark_en_font130", 1.3f)

    @Test @Config(qualifiers = "ar-w344dp-h882dp-420dpi")
    fun foldCoverArabicLargeText() = shoot(RemoteWindow.FoldCover, "attention_foldcover_dark_ar_font130", 1.3f)

    @Test @Config(qualifiers = "w344dp-h882dp-420dpi")
    fun foldCoverEnglishLargestText() = shoot(RemoteWindow.FoldCover, "attention_foldcover_dark_en_font200", 2f)

    @Test @Config(qualifiers = "w393dp-h916dp-440dpi")
    fun razrFoldCoverEnglish() = shoot(RemoteWindow.RazrFoldCover, "attention_razrfoldcover_dark_en")

    @Test @Config(qualifiers = "ar-w393dp-h916dp-440dpi")
    fun razrFoldCoverArabicLargeText() = shoot(RemoteWindow.RazrFoldCover, "attention_razrfoldcover_dark_ar_font130", 1.3f)

    @Test @Config(qualifiers = "w411dp-h923dp-420dpi")
    fun pixelFoldCoverEnglish() = shoot(RemoteWindow.PixelFoldCover, "attention_pixelfoldcover_dark_en")

    private companion object {
        // text is drawn by the host's font rasteriser: a reference recorded on a Mac still matches
        // on CI's Linux within a small share of pixels
        val options = RoborazziOptions(compareOptions = RoborazziOptions.CompareOptions(changeThreshold = 0.02f))
    }
}
