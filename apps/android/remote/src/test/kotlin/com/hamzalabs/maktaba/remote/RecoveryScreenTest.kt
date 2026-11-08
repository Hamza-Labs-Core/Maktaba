package com.hamzalabs.maktaba.remote

import androidx.activity.ComponentActivity
import androidx.compose.material3.Surface
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollToNode
import androidx.compose.ui.test.hasTestTag
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.github.takahirom.roborazzi.RoborazziOptions
import com.github.takahirom.roborazzi.captureRoboImage
import com.hamzalabs.maktaba.core.api.StripKey
import com.hamzalabs.maktaba.core.api.StripLine
import com.hamzalabs.maktaba.core.ui.MaktabaTheme
import com.hamzalabs.maktaba.core.ui.StatusStrip
import com.hamzalabs.maktaba.core.ui.ThemeMode
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.int
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import mockwebserver3.RecordedRequest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import java.time.Instant

/**
 * The Remote's recovery screen (Epic 53 stories 53.04, 53.05) against a made-up console: the
 * groups and parts from `/recovery/api/state` with the app's pass, a part restarted after its
 * confirmation, going back a version confirmed twice, a refusal that says only admins get in,
 * and the status strip's Details opening it instead of the web page. Dark, English and Arabic;
 * the shots are the references under `src/test/screenshots` (and maktaba.dev's Remote page).
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(qualifiers = "w411dp-h891dp-420dpi")
class RecoveryScreenTest {
    @get:Rule(order = 0) val home = FakeRemoteHome()
    @get:Rule(order = 1) val compose = createAndroidComposeRule<ComponentActivity>()

    private val options = RoborazziOptions(compareOptions = RoborazziOptions.CompareOptions(changeThreshold = 0.02f))
    private val actions = mutableListOf<RecordedRequest>()
    private val stateReads = mutableListOf<RecordedRequest>()

    private fun stub(stateCode: Int = 200) {
        // fixed times: the shots stay the same from run to run
        val now = Instant.parse("2026-11-07T09:16:00Z")
        fun t(s: Long) = now.minusSeconds(s).toString()
        home.answer("/api/auth/recovery-pass") { FakeRemoteHome.json("""{"expires_at":"${t(-7 * 86400)}","pass":"p-test"}""") }
        home.answer("/recovery/api/state") { r ->
            synchronized(stateReads) { stateReads += r }
            if (stateCode != 200) FakeRemoteHome.json("""{"error":"unauthorized"}""", stateCode)
            else FakeRemoteHome.json("""
            {"status":{"v":1,"mode":"safe","overall":"down","components":{"database":"down","api":"restarting","streaming":"off","processing":"off","search":"ok","logs":"ok"}},
             "components":[
              {"id":"core.postgres","group":"database","phase":"failed","reason":"crash_loop","state":"down","restarts":3,"cut_off":true,
               "last_exit":"${t(240)}","last_code":1,"last_error":"FATAL: could not open file pg_wal/000000010000000000000002","can_restart":true},
              {"id":"core.api","group":"api","phase":"running","state":"restarting","restarts":5,"last_exit":"${t(60)}","last_code":2,
               "last_error":"db_connect: connection refused","can_restart":true},
              {"id":"search.index","group":"search","phase":"running","state":"ok","restarts":0,"can_restart":true}],
             "disk_free":214748364800,"logs_bytes":52428800,"rollback":"0.3.44","safe_mode":true}""")
        }
        home.answer("/recovery/api/logs") {
            FakeRemoteHome.json("""{"lines":[
              {"ts":"${t(240)}","level":"error","service":"postgres","component":"core.postgres","msg":"raw_line","attrs":{"text":"FATAL: could not open file pg_wal/000000010000000000000002"}},
              {"ts":"${t(60)}","level":"error","service":"api","component":"core.api","msg":"db_connect","err":"connection refused"},
              {"ts":"${t(30)}","level":"warn","service":"supervisor","msg":"child_backoff"}],"truncated":false}""")
        }
        home.answer("/recovery/api/action") { r -> synchronized(actions) { actions += r }; FakeRemoteHome.json("""{"result":"ok"}""") }
    }

    private fun show(stateCode: Int = 200) {
        stub(stateCode)
        compose.setContent {
            MaktabaTheme(ThemeMode.Dark) { Surface { RecoveryScreen(home.remote, onBack = {}, everyMs = 3_600_000) } }
        }
    }

    private fun shot(name: String) {
        compose.waitForIdle()
        compose.onRoot().captureRoboImage("src/test/screenshots/$name.png", roborazziOptions = options)
    }

    private fun scrollTo(tag: String) {
        compose.onNodeWithTag("recovery-rows").performScrollToNode(hasTestTag(tag))
    }

    private fun body(r: RecordedRequest) = Json.parseToJsonElement(r.body!!.utf8()).jsonObject

    @Test
    fun theConsoleStateAndARestart() {
        show()
        compose.settledUntil { compose.has("recovery-part-core.postgres") }
        assertTrue(compose.has("recovery-safe"))
        assertTrue(home.paths.contains("/recovery/api/logs"))
        shot("recovery_dark_en")
        compose.onNodeWithTag("recovery-restart-core.postgres").performClick()
        compose.settledUntil { compose.has("recovery-confirm") }
        compose.onNodeWithTag("recovery-confirm").performClick()
        compose.settledUntil { synchronized(actions) { actions.isNotEmpty() } }
        val sent = synchronized(actions) { actions.single() }
        assertEquals("1", sent.headers["X-Maktaba-Recovery"])
        assertEquals("restart", body(sent)["action"]!!.jsonPrimitive.content)
        assertEquals("core.postgres", body(sent)["component"]!!.jsonPrimitive.content)
        assertEquals(1, body(sent)["confirm"]!!.jsonPrimitive.int)
        compose.settledUntil { compose.has("recovery-done") }
    }

    @Test
    fun goingBackAVersionIsConfirmedTwice() {
        show()
        compose.settledUntil { compose.has("recovery-part-core.postgres") }
        scrollTo("recovery-action-rollback")
        compose.onNodeWithTag("recovery-action-rollback").performClick()
        compose.settledUntil { compose.has("recovery-confirm") }
        compose.onNodeWithTag("recovery-confirm").performClick()
        compose.settledUntil { compose.onAllNodes(hasText("This replaces what is there now. Are you sure?")).fetchSemanticsNodes().isNotEmpty() }
        assertTrue("nothing sent before the second confirmation", synchronized(actions) { actions.isEmpty() })
        compose.onNodeWithTag("recovery-confirm").performClick()
        compose.settledUntil { synchronized(actions) { actions.isNotEmpty() } }
        val sent = synchronized(actions) { actions.single() }
        assertEquals("rollback", body(sent)["action"]!!.jsonPrimitive.content)
        assertEquals(2, body(sent)["confirm"]!!.jsonPrimitive.int)
        // the outcome shows while the list is still scrolled down to the action (not at the list's top)
        compose.settledUntil { compose.has("recovery-done") }
        compose.onNodeWithTag("recovery-done").assertIsDisplayed()
        assertTrue("still scrolled down", compose.has("recovery-action-rollback"))
    }

    @Test @Config(qualifiers = "ar-w411dp-h891dp-420dpi")
    fun inArabic() {
        show()
        compose.settledUntil { compose.has("recovery-part-core.postgres") }
        assertTrue(compose.onAllNodes(hasText("قاعدة البيانات")).fetchSemanticsNodes().isNotEmpty())
        shot("recovery_dark_ar")
    }

    @Test
    fun aRefusalSaysOnlyAdminsAndStopsAsking() {
        show(stateCode = 401)
        compose.settledUntil { compose.has("recovery-denied") }
        shot("recovery_denied_dark_en")
        // the app's pass and then its access token were each tried once; no more reads
        compose.mainClock.advanceTimeBy(60_000)
        compose.waitForIdle()
        assertTrue(synchronized(stateReads) { stateReads.size } <= 2)
    }

    @Test
    fun theStripsDetailsOpensTheAppsOwnScreen() {
        var opened: String? = null
        compose.setContent {
            MaktabaTheme(ThemeMode.Dark) {
                Surface {
                    StatusStrip(listOf(StripLine(StripKey.DATABASE, "/recovery")), "http://home.test", onClose = {},
                        onDetails = { opened = it; true })
                }
            }
        }
        compose.onNodeWithTag("strip.details.database").performClick()
        assertEquals("/recovery", opened)
    }
}
