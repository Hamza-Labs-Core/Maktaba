package com.hamzalabs.maktaba.player.start

import androidx.activity.ComponentActivity
import androidx.compose.material3.Surface
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.github.takahirom.roborazzi.RoborazziOptions
import com.github.takahirom.roborazzi.captureRoboImage
import com.hamzalabs.maktaba.core.MapStore
import com.hamzalabs.maktaba.core.MemoryTokens
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.session.AppSession
import com.hamzalabs.maktaba.core.session.Home
import com.hamzalabs.maktaba.core.ui.HomePicker
import com.hamzalabs.maktaba.core.ui.MaktabaApp
import com.hamzalabs.maktaba.core.ui.MaktabaTheme
import com.hamzalabs.maktaba.core.ui.SignIn
import com.hamzalabs.maktaba.core.ui.ThemeMode
import com.hamzalabs.maktaba.core.ui.ThemeStore
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import org.junit.After
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

/**
 * The start screens' screenshots (Story 48.11, FR-1, FR-2, NFR-20): the home picker and sign-in
 * by password and by code, in the theme a first launch gets (no stored choice: dark), English and
 * Arabic, on a phone, a foldable open flat and a large tablet. The home is a scratch one at a
 * fixed address; sign-in by code talks to a MockWebServer standing in for it.
 * `recordRoborazziPhoneDebug` writes the references under `src/test/screenshots`; CI's
 * `verifyRoborazziPhoneDebug` fails when one no longer matches.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class StartScreensScreenshotTest {
    @get:Rule val compose = createAndroidComposeRule<ComponentActivity>()

    private val server = MockWebServer()
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private lateinit var session: AppSession

    @Before
    fun start() {
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse = when (request.url.encodedPath) {
                "/api/pairing/device" -> json(CODE)
                // nobody approves: the code stays on screen
                "/api/pairing/device/token" -> json("{}", 202)
                else -> MockResponse.Builder().code(404).build()
            }
        }
        server.start()
        // the home shows at its fixed address; its requests go to the mock
        // a fixed clock: the code's countdown reads the same on every run
        session = AppSession(MapStore(), scope, { MemoryTokens() }, { _, tokens -> ApiClient(server.url("/"), tokens) }, now = { 1_793_527_200_000L })
    }

    @After
    fun stop() {
        scope.cancel()
        server.close()
    }

    /** The start screens as MainActivity shows them on a first launch: the stored theme (none) over a Surface. */
    private fun show(content: @androidx.compose.runtime.Composable () -> Unit) {
        val mode = ThemeStore(MapStore()).mode.value
        compose.setContent { MaktabaTheme(mode) { Surface { content() } } }
    }

    private fun capture(name: String) {
        compose.waitForIdle()
        compose.onRoot().captureRoboImage("src/test/screenshots/$name.png", roborazziOptions = options)
    }

    private fun picker(name: String) {
        session.use(HOME)
        session.switchHome()
        show { HomePicker(session, MaktabaApp.Player) }
        // past the search for homes on the network (it says when it found none)
        compose.mainClock.advanceTimeBy(7_000)
        compose.waitUntil(5_000) { compose.onAllNodes(hasText("Darwish home")).fetchSemanticsNodes().isNotEmpty() }
        capture(name)
    }

    private fun password(name: String) {
        session.use(HOME)
        show { SignIn(session, MaktabaApp.Player) }
        compose.waitUntil(5_000) { compose.onAllNodes(hasTestTag("signin-password-tab")).fetchSemanticsNodes().isNotEmpty() }
        capture(name)
    }

    private fun code(name: String) {
        session.use(HOME)
        show { SignIn(session, MaktabaApp.Player) }
        compose.onNodeWithTag("signin-code-tab").performClick()
        compose.waitUntil(10_000) { compose.onAllNodes(hasTestTag("signin-code")).fetchSemanticsNodes().isNotEmpty() }
        capture(name)
    }

    @Test @Config(qualifiers = PHONE) fun pickerPhoneEnglish() = picker("start_picker_phone_dark_en")
    @Test @Config(qualifiers = "ar-$PHONE") fun pickerPhoneArabic() = picker("start_picker_phone_dark_ar")
    @Test @Config(qualifiers = FOLDABLE) fun pickerFoldableEnglish() = picker("start_picker_foldable_dark_en")
    @Test @Config(qualifiers = "ar-$FOLDABLE") fun pickerFoldableArabic() = picker("start_picker_foldable_dark_ar")
    @Test @Config(qualifiers = TABLET) fun pickerTabletEnglish() = picker("start_picker_tablet_dark_en")
    @Test @Config(qualifiers = "ar-$TABLET") fun pickerTabletArabic() = picker("start_picker_tablet_dark_ar")

    @Test @Config(qualifiers = PHONE) fun passwordPhoneEnglish() = password("start_password_phone_dark_en")
    @Test @Config(qualifiers = "ar-$PHONE") fun passwordPhoneArabic() = password("start_password_phone_dark_ar")
    @Test @Config(qualifiers = FOLDABLE) fun passwordFoldableEnglish() = password("start_password_foldable_dark_en")
    @Test @Config(qualifiers = "ar-$FOLDABLE") fun passwordFoldableArabic() = password("start_password_foldable_dark_ar")
    @Test @Config(qualifiers = TABLET) fun passwordTabletEnglish() = password("start_password_tablet_dark_en")
    @Test @Config(qualifiers = "ar-$TABLET") fun passwordTabletArabic() = password("start_password_tablet_dark_ar")

    @Test @Config(qualifiers = PHONE) fun codePhoneEnglish() = code("start_code_phone_dark_en")
    @Test @Config(qualifiers = "ar-$PHONE") fun codePhoneArabic() = code("start_code_phone_dark_ar")
    @Test @Config(qualifiers = TABLET) fun codeTabletEnglish() = code("start_code_tablet_dark_en")
    @Test @Config(qualifiers = "ar-$TABLET") fun codeTabletArabic() = code("start_code_tablet_dark_ar")

    private companion object {
        // the windows of ShellScreenshotTest: a Pixel 9, a Pixel 9 Pro Fold open flat, a large tablet
        const val PHONE = "w411dp-h891dp-xxhdpi"
        const val FOLDABLE = "w673dp-h841dp-xhdpi"
        const val TABLET = "w1280dp-h800dp-xhdpi"

        val HOME = Home(url = "http://192.168.1.20:8088", name = "Darwish home")

        // a fixed code and approve address, so the QR is the same on every run
        val CODE = """{"code":"ABCD1234","device_code":"scratch-device-secret","verify_url":"http://192.168.1.20:8088/pair?code=ABCD-1234","interval":2}"""

        fun json(body: String, code: Int = 200): MockResponse =
            MockResponse.Builder().code(code).setHeader("Content-Type", "application/json").body(body).build()

        // text is drawn by the host's font rasteriser: a reference recorded on a Mac still matches
        // on CI's Linux within a small share of pixels
        val options = RoborazziOptions(compareOptions = RoborazziOptions.CompareOptions(changeThreshold = 0.02f))
    }
}
