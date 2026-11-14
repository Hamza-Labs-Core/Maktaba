package com.hamzalabs.maktaba.player.start

import androidx.activity.ComponentActivity
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.github.takahirom.roborazzi.RoborazziOptions
import com.github.takahirom.roborazzi.captureRoboImage
import com.hamzalabs.maktaba.core.MapStore
import com.hamzalabs.maktaba.core.MemoryTokens
import com.hamzalabs.maktaba.core.compat.CompatApp
import com.hamzalabs.maktaba.core.compat.CompatVerdict
import com.hamzalabs.maktaba.core.session.AppSession
import com.hamzalabs.maktaba.core.session.ConnectLink
import com.hamzalabs.maktaba.core.session.HomeProbe
import com.hamzalabs.maktaba.core.ui.MaktabaApp
import com.hamzalabs.maktaba.core.ui.MaktabaTheme
import com.hamzalabs.maktaba.core.ui.SessionGate
import com.hamzalabs.maktaba.core.ui.ThemeMode
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.runBlocking
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import java.util.Collections
import com.hamzalabs.maktaba.core.R as CoreR

/**
 * Story 58.01, T05 on phones: a mock home at level 99 (oldest 99) shows Update this app, and a
 * home from before the contract (level 0) shows Update the home to a build that needs level 1,
 * through the start screens' own gate ([SessionGate]), English and Arabic, dark; nothing else
 * is asked of the home.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(qualifiers = "w411dp-h891dp")
class CompatPageTest {
    @get:Rule val compose = createAndroidComposeRule<ComponentActivity>()

    private val server = MockWebServer()
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private val asked: MutableList<String> = Collections.synchronizedList(mutableListOf())

    @After
    fun stop() {
        scope.cancel()
        server.close()
    }

    private fun home(discovery: String) {
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                asked += request.url.encodedPath
                return if (request.url.encodedPath == "/api/discovery/home") {
                    MockResponse.Builder().code(200).setHeader("Content-Type", "application/json").body(discovery).build()
                } else {
                    MockResponse.Builder().code(404).build()
                }
            }
        }
        server.start()
    }

    private fun start(app: CompatApp): AppSession {
        val session = AppSession(MapStore(), scope, { MemoryTokens() }, compatApp = app)
        runBlocking { session.connect(ConnectLink(null, null, listOf(server.url("/").toString().trimEnd('/'))), HomeProbe()) }
        compose.setContent { MaktabaTheme(ThemeMode.Dark) { SessionGate(session, MaktabaApp.Player) { _, _ -> } } }
        compose.waitForIdle()
        return session
    }

    private fun text(tag: String): String =
        compose.onNodeWithTag(tag, useUnmergedTree = true).fetchSemanticsNode().config
            .getOrNull(SemanticsProperties.Text).orEmpty().joinToString("") { it.text }

    private fun string(id: Int, vararg args: Any) = compose.activity.getString(id, *args)

    private val lang get() = compose.activity.resources.configuration.locales[0].language

    private fun updateApp() {
        home("""{"id":"h","name":"Kitchen","version":"9.0.0","urls":[],"api_level":99,"api_level_oldest":99,"client_floor":{}}""")
        val session = start(CompatApp("player", "android", "1.0.0"))
        assertEquals(CompatVerdict.UpdateApp, session.compatBlock.value?.verdict)
        assertEquals(string(CoreR.string.compat_update_app_title), text("compat-title"))
        assertEquals(string(CoreR.string.compat_update_app_why, "Kitchen"), text("compat-line-0"))
        assertEquals(string(CoreR.string.compat_update_app_android), text("compat-line-1"))
        assertEquals("only the home's answer was asked", listOf("/api/discovery/home"), asked.toList())
        compose.onRoot().captureRoboImage("src/test/screenshots/compat_update_app_dark_$lang.png", roborazziOptions = options)
    }

    private fun updateHome() {
        home("""{"id":"h","name":"Kitchen","version":"0.3.100","urls":[]}""")
        val session = start(CompatApp("player", "android", "2.0.0", minApiLevel = 1))
        assertEquals(CompatVerdict.UpdateHome, session.compatBlock.value?.verdict)
        assertEquals(string(CoreR.string.compat_update_home_title), text("compat-title"))
        assertEquals(string(CoreR.string.compat_home_version, "Kitchen", "0.3.100"), text("compat-line-0"))
        // no one signed in here yet: both the admin's words and everyone else's
        assertEquals(string(CoreR.string.compat_update_home_admin, "Kitchen"), text("compat-line-1"))
        assertEquals(string(CoreR.string.compat_update_home_viewer, "Kitchen"), text("compat-line-2"))
        compose.onRoot().captureRoboImage("src/test/screenshots/compat_update_home_dark_$lang.png", roborazziOptions = options)

        // another home: the page goes, the home picker shows
        compose.onNodeWithTag("compat-other-home").performClick()
        compose.waitForIdle()
        assertTrue(compose.onAllNodes(hasTestTag("compat-page")).fetchSemanticsNodes().isEmpty())
        assertEquals(AppSession.Phase.ChoosingHome, session.phase.value)
    }

    @Test fun updateThisAppEnglish() = updateApp()

    @Test @Config(qualifiers = "ar-w411dp-h891dp")
    fun updateThisAppArabic() = updateApp()

    @Test fun updateTheHomeEnglish() = updateHome()

    @Test @Config(qualifiers = "ar-w411dp-h891dp")
    fun updateTheHomeArabic() = updateHome()

    private companion object {
        val options = RoborazziOptions(compareOptions = RoborazziOptions.CompareOptions(changeThreshold = 0.02f))
    }
}
