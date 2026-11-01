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
import com.hamzalabs.maktaba.core.session.ConnectProgress
import com.hamzalabs.maktaba.core.session.ConnectStage
import com.hamzalabs.maktaba.core.session.FailReason
import com.hamzalabs.maktaba.core.session.Home
import com.hamzalabs.maktaba.core.ui.HomePicker
import com.hamzalabs.maktaba.core.ui.MaktabaApp
import com.hamzalabs.maktaba.core.ui.MaktabaTheme
import com.hamzalabs.maktaba.core.ui.SignIn
import com.hamzalabs.maktaba.core.ui.ThemeMode
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.MutableStateFlow
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import org.junit.After
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

/**
 * Each connect-progress step on the screen that shows it (story 48.37): finding the home, an
 * address tried, found, signing in, waiting for a code's approval (its countdown), reconnecting
 * on launch, the home busy (its countdown), and a failure on retry (why, the addresses tried,
 * Retry, attempt 2). Dark, English and Arabic, on a phone and a tablet. The clock is fixed, so a
 * countdown reads the same on every run. `recordRoborazziPhoneDebug` writes the references
 * under `src/test/screenshots`; CI's `verifyRoborazziPhoneDebug` fails when one no longer
 * matches.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class ConnectProgressScreenshotTest {
    @get:Rule val compose = createAndroidComposeRule<ComponentActivity>()

    private val server = MockWebServer()
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private val progress = MutableStateFlow<ConnectProgress?>(null)
    private lateinit var session: AppSession

    @Before
    fun start() {
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse = when (request.url.encodedPath) {
                "/api/pairing/device" -> json(CODE)
                // nobody approves: the code waits
                "/api/pairing/device/token" -> json("{}", 202)
                else -> MockResponse.Builder().code(404).build()
            }
        }
        server.start()
        session = AppSession(
            MapStore(), scope, { MemoryTokens() }, { _, tokens -> ApiClient(server.url("/"), tokens) },
            now = { NOW },
            progressState = progress,
        )
    }

    @After
    fun stop() {
        scope.cancel()
        server.close()
    }

    private fun show(content: @androidx.compose.runtime.Composable () -> Unit) {
        compose.setContent { MaktabaTheme(ThemeMode.Dark) { Surface { content() } } }
    }

    private fun capture(name: String) {
        compose.waitForIdle()
        assertTrue("the step is on screen", compose.onAllNodes(hasTestTag("connect-status")).fetchSemanticsNodes().isNotEmpty())
        compose.onRoot().captureRoboImage("src/test/screenshots/$name.png", roborazziOptions = options)
    }

    /** A step of adding a home, on the home picker. */
    private fun onPicker(step: ConnectProgress, name: String) {
        session.use(HOME)
        session.switchHome()
        show { HomePicker(session, MaktabaApp.Player) }
        compose.mainClock.advanceTimeBy(7_000)
        compose.waitUntil(5_000) { compose.onAllNodes(hasText("Darwish home")).fetchSemanticsNodes().isNotEmpty() }
        progress.value = step
        capture(name)
    }

    /** A step over the sign-in (on launch, found, signing in). */
    private fun onSignIn(step: ConnectProgress, name: String) {
        session.use(HOME)
        show { SignIn(session, MaktabaApp.Player) }
        compose.waitUntil(5_000) { compose.onAllNodes(hasTestTag("signin-password-tab")).fetchSemanticsNodes().isNotEmpty() }
        progress.value = step
        capture(name)
    }

    /** Sign-in by code, as it runs: the code waits for approval, its countdown on the fixed clock. */
    private fun awaiting(name: String) {
        session.use(HOME)
        show { SignIn(session, MaktabaApp.Player) }
        compose.onNodeWithTag("signin-code-tab").performClick()
        compose.waitUntil(10_000) { compose.onAllNodes(hasTestTag("signin-code")).fetchSemanticsNodes().isNotEmpty() }
        compose.waitUntil(5_000) { progress.value is ConnectProgress.AwaitingApproval }
        capture(name)
    }

    private val finding = ConnectProgress.Finding()
    private val trying = ConnectProgress.Trying("192.168.1.20:8088", 1, 3)
    private val found = ConnectProgress.Found("Darwish home")
    private val signingIn = ConnectProgress.SigningIn("Darwish home", ConnectStage.Password)
    private val reconnecting = ConnectProgress.Reconnecting("Darwish home")
    private val busy = ConnectProgress.HomeBusy("Darwish home", NOW + 7_000)
    private val failed = ConnectProgress.Failed(
        FailReason.NoAnswer,
        listOf("192.168.1.20:8088", "darwish.example.com", "relay.maktaba.tv/h/darwish"),
        ConnectStage.Connect,
        attempt = 2,
    )

    @Test @Config(qualifiers = PHONE) fun findingPhoneEnglish() = onPicker(finding, "connect_finding_phone_dark_en")
    @Test @Config(qualifiers = "ar-$PHONE") fun findingPhoneArabic() = onPicker(finding, "connect_finding_phone_dark_ar")
    @Test @Config(qualifiers = TABLET) fun findingTabletEnglish() = onPicker(finding, "connect_finding_tablet_dark_en")
    @Test @Config(qualifiers = "ar-$TABLET") fun findingTabletArabic() = onPicker(finding, "connect_finding_tablet_dark_ar")

    @Test @Config(qualifiers = PHONE) fun tryingPhoneEnglish() = onPicker(trying, "connect_trying_phone_dark_en")
    @Test @Config(qualifiers = "ar-$PHONE") fun tryingPhoneArabic() = onPicker(trying, "connect_trying_phone_dark_ar")
    @Test @Config(qualifiers = TABLET) fun tryingTabletEnglish() = onPicker(trying, "connect_trying_tablet_dark_en")
    @Test @Config(qualifiers = "ar-$TABLET") fun tryingTabletArabic() = onPicker(trying, "connect_trying_tablet_dark_ar")

    @Test @Config(qualifiers = PHONE) fun failedPhoneEnglish() = onPicker(failed, "connect_failed_phone_dark_en")
    @Test @Config(qualifiers = "ar-$PHONE") fun failedPhoneArabic() = onPicker(failed, "connect_failed_phone_dark_ar")
    @Test @Config(qualifiers = TABLET) fun failedTabletEnglish() = onPicker(failed, "connect_failed_tablet_dark_en")
    @Test @Config(qualifiers = "ar-$TABLET") fun failedTabletArabic() = onPicker(failed, "connect_failed_tablet_dark_ar")

    @Test @Config(qualifiers = PHONE) fun foundPhoneEnglish() = onSignIn(found, "connect_found_phone_dark_en")
    @Test @Config(qualifiers = "ar-$PHONE") fun foundPhoneArabic() = onSignIn(found, "connect_found_phone_dark_ar")
    @Test @Config(qualifiers = TABLET) fun foundTabletEnglish() = onSignIn(found, "connect_found_tablet_dark_en")
    @Test @Config(qualifiers = "ar-$TABLET") fun foundTabletArabic() = onSignIn(found, "connect_found_tablet_dark_ar")

    @Test @Config(qualifiers = PHONE) fun signingInPhoneEnglish() = onSignIn(signingIn, "connect_signing_in_phone_dark_en")
    @Test @Config(qualifiers = "ar-$PHONE") fun signingInPhoneArabic() = onSignIn(signingIn, "connect_signing_in_phone_dark_ar")
    @Test @Config(qualifiers = TABLET) fun signingInTabletEnglish() = onSignIn(signingIn, "connect_signing_in_tablet_dark_en")
    @Test @Config(qualifiers = "ar-$TABLET") fun signingInTabletArabic() = onSignIn(signingIn, "connect_signing_in_tablet_dark_ar")

    @Test @Config(qualifiers = PHONE) fun awaitingPhoneEnglish() = awaiting("connect_awaiting_phone_dark_en")
    @Test @Config(qualifiers = "ar-$PHONE") fun awaitingPhoneArabic() = awaiting("connect_awaiting_phone_dark_ar")
    @Test @Config(qualifiers = TABLET) fun awaitingTabletEnglish() = awaiting("connect_awaiting_tablet_dark_en")
    @Test @Config(qualifiers = "ar-$TABLET") fun awaitingTabletArabic() = awaiting("connect_awaiting_tablet_dark_ar")

    @Test @Config(qualifiers = PHONE) fun reconnectingPhoneEnglish() = onSignIn(reconnecting, "connect_reconnecting_phone_dark_en")
    @Test @Config(qualifiers = "ar-$PHONE") fun reconnectingPhoneArabic() = onSignIn(reconnecting, "connect_reconnecting_phone_dark_ar")
    @Test @Config(qualifiers = TABLET) fun reconnectingTabletEnglish() = onSignIn(reconnecting, "connect_reconnecting_tablet_dark_en")
    @Test @Config(qualifiers = "ar-$TABLET") fun reconnectingTabletArabic() = onSignIn(reconnecting, "connect_reconnecting_tablet_dark_ar")

    @Test @Config(qualifiers = PHONE) fun busyPhoneEnglish() = onSignIn(busy, "connect_busy_phone_dark_en")
    @Test @Config(qualifiers = "ar-$PHONE") fun busyPhoneArabic() = onSignIn(busy, "connect_busy_phone_dark_ar")
    @Test @Config(qualifiers = TABLET) fun busyTabletEnglish() = onSignIn(busy, "connect_busy_tablet_dark_en")
    @Test @Config(qualifiers = "ar-$TABLET") fun busyTabletArabic() = onSignIn(busy, "connect_busy_tablet_dark_ar")

    private companion object {
        // the windows of StartScreensScreenshotTest: a Pixel 9 and a large tablet
        const val PHONE = "w411dp-h891dp-xxhdpi"
        const val TABLET = "w1280dp-h800dp-xhdpi"

        /** The fixed clock: 2026-11-01 10:00 UTC. */
        const val NOW = 1_793_527_200_000L

        val HOME = Home(url = "http://192.168.1.20:8088", name = "Darwish home")

        // no expiry from the home: ten minutes on this device's clock (10:00)
        val CODE = """{"code":"ABCD1234","device_code":"scratch-device-secret","verify_url":"http://192.168.1.20:8088/pair?code=ABCD-1234","interval":2}"""

        fun json(body: String, code: Int = 200): MockResponse =
            MockResponse.Builder().code(code).setHeader("Content-Type", "application/json").body(body).build()

        val options = RoborazziOptions(compareOptions = RoborazziOptions.CompareOptions(changeThreshold = 0.02f))
    }
}
