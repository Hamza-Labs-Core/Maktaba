package com.hamzalabs.maktaba.tv

import androidx.activity.ComponentActivity
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performSemanticsAction
import androidx.compose.ui.semantics.SemanticsActions
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
import com.hamzalabs.maktaba.tv.ui.screens.CompatScreen
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaTvTheme
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
 * Story 58.01, T05 on Android TV (1080p, the TV's dark theme), English and Arabic: a mock home at
 * level 99 (oldest 99) shows Update this app in Google Play's words for TVs, and a home from
 * before the contract (level 0) shows Update the home to a build that needs level 1. The home is
 * asked nothing but its own answer, and Try again asks it again.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class CompatScreenTest {
    @get:Rule val compose = createAndroidComposeRule<ComponentActivity>()

    private val server = MockWebServer()
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private val asked: MutableList<String> = Collections.synchronizedList(mutableListOf())

    @After
    fun stop() {
        scope.cancel()
        server.close()
    }

    private fun show(discovery: String, app: CompatApp): AppSession {
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
        val session = AppSession(MapStore(), scope, { MemoryTokens() }, compatApp = app)
        runBlocking { session.connect(ConnectLink(null, null, listOf(server.url("/").toString().trimEnd('/'))), HomeProbe()) }
        val block = session.compatBlock.value ?: error("no page")
        compose.setContent { MaktabaTvTheme { CompatScreen(block, onTryAgain = session::recheckCompat, onOtherHome = session::switchHome) } }
        compose.waitForIdle()
        return session
    }

    private fun text(tag: String): String =
        compose.onNodeWithTag(tag, useUnmergedTree = true).fetchSemanticsNode().config
            .getOrNull(SemanticsProperties.Text).orEmpty().joinToString("") { it.text }

    private fun string(id: Int, vararg args: Any) = compose.activity.getString(id, *args)

    private val lang get() = compose.activity.resources.configuration.locales[0].language

    private fun assertFits() {
        val root = compose.onRoot().fetchSemanticsNode().size
        assertEquals("a 1080p TV", 1920 to 1080, root.width to root.height)
        for (tag in listOf("compat-title", "compat-line-0", "compat-try-again", "compat-other-home")) {
            val b = compose.onNodeWithTag(tag, useUnmergedTree = true).fetchSemanticsNode().boundsInRoot
            assertTrue("$tag inside the screen: $b", b.left >= 0f && b.top >= 0f && b.right <= root.width && b.bottom <= root.height && b.width > 0f)
        }
    }

    private fun updateApp() {
        val session = show(
            """{"id":"h","name":"Kitchen","version":"9.0.0","urls":[],"api_level":99,"api_level_oldest":99,"client_floor":{}}""",
            // this build's platform: Google Play's words on the google build, the Appstore's on Fire TV (58.05)
            CompatApp("tv", BuildConfig.TV_PLATFORM, "1.0.0"),
        )
        assertEquals(CompatVerdict.UpdateApp, session.compatBlock.value?.verdict)
        assertEquals(string(CoreR.string.compat_update_app_title), text("compat-title"))
        assertEquals(string(CoreR.string.compat_update_app_why, "Kitchen"), text("compat-line-0"))
        assertEquals(
            string(if (BuildConfig.TV_PLATFORM == "firetv") CoreR.string.compat_update_app_firetv else CoreR.string.compat_update_app_androidtv),
            text("compat-line-1"),
        )
        assertFits()
        assertEquals(listOf("/api/discovery/home"), asked.toList())
        // the Fire TV build's page names the Amazon Appstore (its own references)
        val build = if (BuildConfig.TV_PLATFORM == "firetv") "firetv" else "tv"
        compose.onRoot().captureRoboImage("src/test/screenshots/compat_update_app_${build}_dark_$lang.png", roborazziOptions = options)

        // Try again asks the home again (it is still too new for this build)
        compose.onNodeWithTag("compat-try-again").performSemanticsAction(SemanticsActions.OnClick)
        val until = System.currentTimeMillis() + 5_000
        while (asked.size < 2 && System.currentTimeMillis() < until) Thread.sleep(20)
        assertEquals(listOf("/api/discovery/home", "/api/discovery/home"), asked.toList())
    }

    private fun updateHome() {
        val session = show("""{"id":"h","name":"Kitchen","version":"0.3.100","urls":[]}""", CompatApp("tv", "androidtv", "2.0.0", minApiLevel = 1))
        assertEquals(CompatVerdict.UpdateHome, session.compatBlock.value?.verdict)
        assertEquals(string(CoreR.string.compat_update_home_title), text("compat-title"))
        assertEquals(string(CoreR.string.compat_home_version, "Kitchen", "0.3.100"), text("compat-line-0"))
        assertEquals(string(CoreR.string.compat_update_home_admin, "Kitchen"), text("compat-line-1"))
        assertEquals(string(CoreR.string.compat_update_home_viewer, "Kitchen"), text("compat-line-2"))
        assertFits()
        compose.onRoot().captureRoboImage("src/test/screenshots/compat_update_home_tv_dark_$lang.png", roborazziOptions = options)
    }

    @Test @Config(qualifiers = TV)
    fun updateThisAppEnglish() = updateApp()

    @Test @Config(qualifiers = "ar-$TV")
    fun updateThisAppArabic() = updateApp()

    @Test @Config(qualifiers = TV)
    fun updateTheHomeEnglish() = updateHome()

    @Test @Config(qualifiers = "ar-$TV")
    fun updateTheHomeArabic() = updateHome()

    private companion object {
        const val TV = "w960dp-h540dp-land-television-xhdpi"
        // half scale: a 1080p reference stays well under the repository's 1 MB a file
        val options = RoborazziOptions(
            recordOptions = RoborazziOptions.RecordOptions(resizeScale = 0.5),
            compareOptions = RoborazziOptions.CompareOptions(changeThreshold = 0.02f),
        )
    }
}
