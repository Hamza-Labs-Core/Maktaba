package com.hamzalabs.maktaba.player.start

import android.view.ViewGroup
import androidx.activity.ComponentActivity
import androidx.compose.material3.Surface
import androidx.compose.ui.test.assertIsFocused
import androidx.compose.ui.test.getBoundsInRoot
import androidx.compose.ui.test.getUnclippedBoundsInRoot
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performImeAction
import androidx.compose.ui.test.performTextInput
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.core.graphics.Insets
import androidx.core.view.ViewCompat
import androidx.core.view.WindowInsetsCompat
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.core.MapStore
import com.hamzalabs.maktaba.core.MemoryTokens
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.session.AppSession
import com.hamzalabs.maktaba.core.session.Home
import com.hamzalabs.maktaba.core.ui.CloudHomes
import com.hamzalabs.maktaba.core.ui.MaktabaApp
import com.hamzalabs.maktaba.core.ui.MaktabaTheme
import com.hamzalabs.maktaba.core.ui.SignIn
import com.hamzalabs.maktaba.core.ui.ThemeMode
import java.util.Collections
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
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

/**
 * The sign-in forms from the keyboard (each field needs a Next key to the next field): the
 * keyboard's Next on a field moves to the next one, and its Go on the last one signs in. The
 * home's and the cloud's sign-ins talk to a MockWebServer that refuses them; what is checked is
 * that the sign-in was sent.
 *
 * And the focused field stays above the keyboard: in a short window the keyboard (its IME
 * inset, dispatched to the app as Android does when it slides up) rises over the password the
 * focus just moved to, and the form (`StartFrame`, shrunk by the IME padding) scrolls the password up above it.
 */
@RunWith(AndroidJUnit4::class)
@Config(qualifiers = "w411dp-h891dp")
class SignInKeyboardTest {
    @get:Rule val compose = createAndroidComposeRule<ComponentActivity>()

    private val server = MockWebServer()
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private val asked: MutableList<String> = Collections.synchronizedList(mutableListOf())
    private lateinit var session: AppSession

    @Before
    fun start() {
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                asked += "${request.method} ${request.url.encodedPath}"
                return MockResponse.Builder().code(401).setHeader("Content-Type", "application/json")
                    .body("""{"title":"invalid credentials","status":401}""").build()
            }
        }
        server.start()
        session = AppSession(MapStore(), scope, { MemoryTokens() }, { _, tokens -> ApiClient(server.url("/"), tokens) })
    }

    @After
    fun stop() {
        scope.cancel()
        server.close()
    }

    // a wait for the app to settle: Compose idle, then the check (the time is only a ceiling)
    private fun settledUntil(what: String, check: () -> Boolean) {
        compose.waitForIdle()
        runCatching { compose.waitUntil(10_000) { check() } }
        assertTrue(what, check())
    }

    private fun shows(tag: String) =
        settledUntil("$tag shows") { compose.onAllNodes(hasTestTag(tag)).fetchSemanticsNodes().isNotEmpty() }

    private fun sent(request: String) = settledUntil("$request was sent") { request in asked }

    @Test
    fun homeSignInNextThenGo() {
        session.use(Home(url = "http://192.168.1.20:8088", name = "Darwish home"))
        compose.setContent { MaktabaTheme(ThemeMode.Dark) { Surface { SignIn(session, MaktabaApp.Player) } } }
        shows("signin-username")

        compose.onNodeWithTag("signin-username").performClick()
        compose.onNodeWithTag("signin-username").performTextInput("tester")
        compose.onNodeWithTag("signin-username").performImeAction()
        compose.onNodeWithTag("signin-password").assertIsFocused()

        compose.onNodeWithTag("signin-password").performTextInput("not-the-password")
        compose.onNodeWithTag("signin-password").performImeAction()
        sent("POST /api/auth/login")
    }

    /** The cloud's sign-in, its address the mock's (chosen before, so the form starts with it). */
    private fun cloud() {
        session.cloudApi = server.url("/").toString().trimEnd('/')
        compose.setContent { MaktabaTheme(ThemeMode.Dark) { Surface { CloudHomes(MaktabaApp.Player, session, onClose = {}) } } }
    }

    @Test
    fun cloudSignInNextThenGo() {
        cloud()
        shows("cloud-email")

        compose.onNodeWithTag("cloud-email").performClick()
        compose.onNodeWithTag("cloud-email").performTextInput("tester@example.com")
        compose.onNodeWithTag("cloud-email").performImeAction()
        compose.onNodeWithTag("cloud-password").assertIsFocused()

        // without Advanced the password is the last field: its Go signs in
        compose.onNodeWithTag("cloud-password").performTextInput("not-the-password")
        compose.onNodeWithTag("cloud-password").performImeAction()
        sent("POST /v1/auth/login")
    }

    @Test
    fun cloudSignInWithItsAddressNextThenGo() {
        cloud()
        shows("cloud-advanced")
        compose.onNodeWithTag("cloud-advanced").performClick()
        shows("cloud-address")

        compose.onNodeWithTag("cloud-email").performClick()
        compose.onNodeWithTag("cloud-email").performTextInput("tester@example.com")
        compose.onNodeWithTag("cloud-email").performImeAction()
        compose.onNodeWithTag("cloud-password").assertIsFocused()

        // with Advanced the cloud's address comes last: Next from the password, Go from it
        compose.onNodeWithTag("cloud-password").performTextInput("not-the-password")
        compose.onNodeWithTag("cloud-password").performImeAction()
        compose.onNodeWithTag("cloud-address").assertIsFocused()
        compose.onNodeWithTag("cloud-address").performImeAction()
        sent("POST /v1/auth/login")
    }

    // MARK: above the keyboard

    /** Where the node tagged `tag` is drawn in the window, whether or not the form clips it. */
    private fun boundsOf(tag: String) = compose.onNodeWithTag(tag).getUnclippedBoundsInRoot()

    private val window: Dp get() = compose.onRoot().getBoundsInRoot().bottom

    /**
     * The keyboard slides up to `height`: the IME inset dispatched to the app's views, as the
     * system does when the keyboard shows (Compose's `WindowInsets.ime` reads it).
     */
    private fun raiseKeyboard(height: Dp) {
        compose.runOnUiThread {
            val px = (height.value * compose.activity.resources.displayMetrics.density).toInt()
            val insets = WindowInsetsCompat.Builder()
                .setInsets(WindowInsetsCompat.Type.ime(), Insets.of(0, 0, 0, px))
                .setVisible(WindowInsetsCompat.Type.ime(), true)
                .build()
            ViewCompat.dispatchApplyWindowInsets(compose.activity.findViewById<ViewGroup>(android.R.id.content), insets)
        }
        compose.waitForIdle()
    }

    /** `field` lies wholly on the screen above the top of a keyboard `keyboard` high. */
    private fun assertAboveTheKeyboard(field: String, keyboard: Dp) {
        val top = window - keyboard
        settledUntil("$field ends above the keyboard's top at $top, on the screen") {
            val b = boundsOf(field)
            b.bottom > b.top && b.top >= 0.dp && b.bottom <= top
        }
    }

    /**
     * The focus is on `field`; then the keyboard rises over all of it (its top 24 dp above
     * the field's). The form must scroll the field up above the keyboard.
     */
    private fun keptAboveTheRisingKeyboard(field: String) {
        compose.onNodeWithTag(field).assertIsFocused()
        val keyboard = window - boundsOf(field).top + 24.dp
        raiseKeyboard(keyboard)
        assertAboveTheKeyboard(field, keyboard)
    }

    private fun homeSignIn() {
        session.use(Home(url = "http://192.168.1.20:8088", name = "Darwish home"))
        compose.setContent { MaktabaTheme(ThemeMode.Dark) { Surface { SignIn(session, MaktabaApp.Player) } } }
        shows("signin-username")
        compose.onNodeWithTag("signin-username").performClick()
        compose.onNodeWithTag("signin-username").performTextInput("tester")
    }

    @Test
    @Config(qualifiers = "w360dp-h640dp")
    fun homeSignInPasswordRisesAboveTheKeyboard() {
        homeSignIn()
        compose.onNodeWithTag("signin-username").performImeAction()
        keptAboveTheRisingKeyboard("signin-password")
    }

    /** The keyboard is up while the username is typed; Next moves to a password under it. */
    @Test
    @Config(qualifiers = "w360dp-h640dp")
    fun homeSignInNextBringsThePasswordAboveTheKeyboard() {
        homeSignIn()
        // a keyboard whose top is just below the username: the password is under it
        val keyboard = window - boundsOf("signin-username").bottom - 8.dp
        raiseKeyboard(keyboard)
        val under = boundsOf("signin-password")
        assertTrue("the password ($under) starts under the keyboard's top at ${window - keyboard}", under.top > window - keyboard)
        compose.onNodeWithTag("signin-username").performImeAction()
        compose.onNodeWithTag("signin-password").assertIsFocused()
        assertAboveTheKeyboard("signin-password", keyboard)
    }

    @Test
    @Config(qualifiers = "w360dp-h640dp")
    fun cloudSignInPasswordRisesAboveTheKeyboard() {
        cloud()
        shows("cloud-email")

        compose.onNodeWithTag("cloud-email").performClick()
        compose.onNodeWithTag("cloud-email").performTextInput("tester@example.com")
        compose.onNodeWithTag("cloud-email").performImeAction()
        keptAboveTheRisingKeyboard("cloud-password")
    }
}
