package com.hamzalabs.maktaba.remote

import androidx.activity.ComponentActivity
import androidx.compose.material3.Surface
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.github.takahirom.roborazzi.RoborazziOptions
import com.github.takahirom.roborazzi.captureRoboImage
import com.hamzalabs.maktaba.core.ui.MaktabaTheme
import com.hamzalabs.maktaba.core.ui.ReportProblemForm
import com.hamzalabs.maktaba.core.ui.ThemeMode
import mockwebserver3.MockResponse
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import java.time.Instant

/**
 * The Remote's diagnostics (Epic 53 stories 53.11 §4, 53.12 §4, 53.15 §5; T06) against
 * made-up groups, lines, boards and reports: each tab reads the web's route and shows it, a
 * group is resolved through the home, and the report form sends no screenshot unless ticked
 * (53.15 T08). Dark, English and Arabic; the shots are the references under
 * `src/test/screenshots` (and maktaba.dev's Remote page).
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(qualifiers = "w411dp-h891dp-420dpi")
class DiagnosticsScreenTest {
    @get:Rule(order = 0) val home = FakeRemoteHome()
    @get:Rule(order = 1) val compose = createAndroidComposeRule<ComponentActivity>()

    private val options = RoborazziOptions(compareOptions = RoborazziOptions.CompareOptions(changeThreshold = 0.02f))
    private val posted = mutableListOf<String>()

    private fun stub() {
        val now = Instant.now()
        fun t(s: Long) = now.minusSeconds(s).toString()
        home.answer("/api/admin/errors") {
            FakeRemoteHome.json("""{"groups":[
              {"fp":"e1","service":"streaming","component":"streaming","title":"A segment of the stream was missing","count":42,"count_24h":7,"users":2,
               "status":"new","last_seen":"${t(300)}","days":[0,1,0,2,0,0,3,1,0,0,5,2,4,7]},
              {"fp":"e2","service":"pipeline","component":"transcription","title":"Transcription took too long","count":5,"count_24h":1,"users":0,
               "status":"seen","last_seen":"${t(7200)}","days":[0,0,0,0,0,0,0,0,0,0,1,2,1,1]}]}""")
        }
        home.answer("/api/admin/errors/e1") {
            FakeRemoteHome.json("""{"group":{"fp":"e1","service":"streaming","component":"streaming","title":"A segment of the stream was missing",
              "count":42,"count_24h":7,"users":2,"status":"new","last_version":"0.3.90"},
              "hours":{"${t(3600 * 30)}":2,"${t(3600 * 5)}":1,"${t(3600)}":4},
              "samples":{"lines":[{"ts":"${t(300)}","level":"error","service":"streaming","node":"home","msg":"segment_missing","version":"0.3.90",
                "request_id":"0192f0c4-1111-7000-8000-000000000001"}],"ids":[991]}}""")
        }
        home.answer("/api/admin/errors/e1/status") { r -> posted += r.body?.utf8().orEmpty(); MockResponse.Builder().code(204).build() }
        home.answer("/api/admin/logs/search") {
            FakeRemoteHome.json("""{"lines":[
              {"ts":"${t(300)}","level":"error","service":"streaming","component":"streaming","msg":"segment_missing","request_id":"0192f0c4-1111-7000-8000-000000000001"},
              {"ts":"${t(301)}","level":"info","service":"api","component":"core.api","msg":"http_request","request_id":"0192f0c4-1111-7000-8000-000000000001"},
              {"ts":"${t(900)}","level":"warn","service":"pipeline","component":"transcription","msg":"model_slow"}],"ids":[991,990,970]}""")
        }
        val pts = (0 until 24).joinToString(",") { "[${now.epochSecond - (23 - it) * 3600},${1 + it % 3}]" }
        home.answer("/api/admin/dashboards/overview") {
            FakeRemoteHome.json("""{"board":"overview","range":"24h","series":[
              {"name":"maktaba_errors_per_hour","labels":"component=streaming","points":[$pts]},
              {"name":"maktaba_component_up","labels":"id=core.api","points":[$pts]}]}""")
        }
        home.answer("/api/reports") {
            FakeRemoteHome.json("""{"reports":[{"id":27,"at":"${t(1800)}","name":"Viewer","app":"android.player","category":"playback","status":"new"},
              {"id":26,"at":"${t(86400)}","name":"Viewer","app":"android.tv","category":"live_tv","status":"looking"}]}""")
        }
    }

    private fun show(tab: DiagTab = DiagTab.Errors) {
        stub()
        compose.setContent { MaktabaTheme(ThemeMode.Dark) { Surface { DiagnosticsScreen(home.remote, onBack = {}, start = tab) } } }
    }

    private fun shot(name: String) {
        compose.waitForIdle()
        compose.onRoot().captureRoboImage("src/test/screenshots/$name.png", roborazziOptions = options)
    }

    @Test
    fun errorsListAGroupAndResolveIt() {
        show()
        compose.settledUntil { compose.has("diag-group-e1") }
        assertTrue(home.paths.contains("/api/admin/errors"))
        shot("diagnostics_errors_dark_en")
        compose.onNodeWithTag("diag-group-e1").performClick()
        compose.settledUntil { compose.has("diag-resolve") }
        shot("diagnostics_error_group_dark_en")
        compose.onNodeWithTag("diag-resolve").performClick()
        compose.settledUntil { posted.isNotEmpty() }
        assertTrue(posted.last().contains("\"status\":\"resolved\"") && posted.last().contains("0.3.90"))
    }

    @Test @Config(qualifiers = "ar-w411dp-h891dp-420dpi")
    fun errorsInArabic() {
        show()
        compose.settledUntil { compose.has("diag-group-e1") }
        shot("diagnostics_errors_dark_ar")
    }

    @Test
    fun logsAndFollowARequest() {
        show(DiagTab.Logs)
        compose.settledUntil { compose.has("diag-logs") && home.paths.contains("/api/admin/logs/search") }
        compose.settledUntil { compose.onAllNodes(androidx.compose.ui.test.hasText("segment_missing")).fetchSemanticsNodes().isNotEmpty() }
        shot("diagnostics_logs_dark_en")
    }

    @Test
    fun dashboards() {
        show(DiagTab.Dashboards)
        compose.settledUntil { compose.has("dash-panel-maktaba_errors_per_hour") }
        shot("diagnostics_dashboards_dark_en")
    }

    @Test @Config(qualifiers = "ar-w411dp-h891dp-420dpi")
    fun dashboardsInArabic() {
        show(DiagTab.Dashboards)
        compose.settledUntil { compose.has("dash-panel-maktaba_errors_per_hour") }
        shot("diagnostics_dashboards_dark_ar")
    }

    @Test
    fun theInbox() {
        show(DiagTab.Reports)
        compose.settledUntil { compose.has("diag-report-27") }
        shot("diagnostics_reports_dark_en")
    }

    @Test
    fun theReportFormSendsNoScreenshotUnlessTicked() {
        stub()
        val bodies = mutableListOf<String>()
        home.answer("/api/reports") { r -> bodies += r.body?.utf8().orEmpty(); FakeRemoteHome.json("""{"id":28}""") }
        var done = false
        compose.setContent {
            MaktabaTheme(ThemeMode.Dark) { Surface { ReportProblemForm(home.remote.client, "Test Home", "0192f0c4", byteArrayOf(1, 2, 3), tv = false) { done = true } } }
        }
        compose.settledUntil { compose.has("report-send") }
        assertFalse("unticked when the form opens", compose.has("report-screenshot-preview"))
        shot("report_form_dark_en")
        compose.onNodeWithTag("report-send").performClick()
        compose.settledUntil { compose.has("report-sent") }
        val body = bodies.single()
        assertTrue(body.contains("name=\"report\""))
        assertFalse(body.contains("name=\"screenshot\""))
        assertTrue(body.contains("\"screenshot_previewed\":false"))
        assertTrue(body.contains("0192f0c4"))
        assertEquals(false, done)
    }

    /** The account menu (core's AccountMenu, the Remote's extra): Diagnostics, Report a problem, My reports, the switch. */
    @Test
    fun theAccountMenuOpensDiagnosticsAndReportAProblem() {
        stub()
        compose.setContent {
            MaktabaTheme(ThemeMode.Dark) {
                Surface {
                    com.hamzalabs.maktaba.core.ui.DiagnosticsHost(home.remote.client, null) {
                        RemoteAccount(home.session, home.user, home.themes, home.remote.client, home.remote)
                    }
                }
            }
        }
        compose.onNode(androidx.compose.ui.test.hasContentDescription(compose.activity.getString(com.hamzalabs.maktaba.core.R.string.account))).performClick()
        compose.settledUntil { compose.has("account-diagnostics") }
        assertTrue("Report a problem, under the host", compose.has("account-report"))
        compose.onNodeWithTag("account-diagnostics").performClick()
        compose.settledUntil { home.remote.diagnostics }
    }
}
