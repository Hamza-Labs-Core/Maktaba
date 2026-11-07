package com.hamzalabs.maktaba.player.diagnostics

import androidx.activity.ComponentActivity
import androidx.compose.material3.Surface
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onRoot
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.github.takahirom.roborazzi.RoborazziOptions
import com.github.takahirom.roborazzi.captureRoboImage
import com.hamzalabs.maktaba.core.MemoryTokens
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.ui.CrashQuestion
import com.hamzalabs.maktaba.core.ui.MaktabaTheme
import com.hamzalabs.maktaba.core.ui.ReportProblemForm
import com.hamzalabs.maktaba.core.ui.ThemeMode
import okhttp3.HttpUrl.Companion.toHttpUrl
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

/**
 * Epic 53 on an Android phone (stories 53.14 T12, 53.15 T07): the crash-report question, its two
 * buttons the same kind and size with neither chosen, and Report a problem with its screenshot
 * box unticked. Dark, English and Arabic; the references under `src/test/screenshots` are
 * maktaba.dev's Player page's.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class DiagnosticsScreenshotTest {
    @get:Rule val compose = createAndroidComposeRule<ComponentActivity>()

    private val options = RoborazziOptions(compareOptions = RoborazziOptions.CompareOptions(changeThreshold = 0.02f))
    private val client = ApiClient("http://192.168.50.177:8088/".toHttpUrl(), MemoryTokens())

    private fun question(name: String) {
        compose.setContent { MaktabaTheme(ThemeMode.Dark) { Surface { CrashQuestion("Test Home") {} } } }
        compose.waitForIdle()
        val yes = compose.onNodeWithTag("crash-question-yes", useUnmergedTree = true).fetchSemanticsNode().boundsInRoot
        val no = compose.onNodeWithTag("crash-question-no", useUnmergedTree = true).fetchSemanticsNode().boundsInRoot
        assertEquals("the same width", yes.width, no.width, 1f)
        assertEquals("the same height", yes.height, no.height, 1f)
        compose.onRoot().captureRoboImage("src/test/screenshots/$name.png", roborazziOptions = options)
    }

    private fun form(name: String) {
        compose.setContent {
            MaktabaTheme(ThemeMode.Dark) { Surface { ReportProblemForm(client, "Test Home", null, byteArrayOf(1, 2, 3), tv = false) {} } }
        }
        compose.waitForIdle()
        assertFalse(compose.onAllNodes(androidx.compose.ui.test.hasTestTag("report-screenshot-preview")).fetchSemanticsNodes().isNotEmpty())
        compose.onRoot().captureRoboImage("src/test/screenshots/$name.png", roborazziOptions = options)
    }

    @Test @Config(qualifiers = "w411dp-h891dp-420dpi")
    fun questionEnglish() = question("crash_question_phone_dark_en")

    @Test @Config(qualifiers = "ar-w411dp-h891dp-420dpi")
    fun questionArabic() = question("crash_question_phone_dark_ar")

    @Test @Config(qualifiers = "w411dp-h891dp-420dpi")
    fun formEnglish() = form("report_problem_phone_dark_en")

    @Test @Config(qualifiers = "ar-w411dp-h891dp-420dpi")
    fun formArabic() = form("report_problem_phone_dark_ar")
}
