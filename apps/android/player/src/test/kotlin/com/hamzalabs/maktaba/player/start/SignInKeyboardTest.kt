package com.hamzalabs.maktaba.player.start

import androidx.activity.ComponentActivity
import androidx.compose.material3.Surface
import androidx.compose.ui.test.assertIsFocused
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performImeAction
import androidx.compose.ui.test.performTextInput
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
 * The sign-in forms from the keyboard (the owner: "sign in need the next field arrow"): the
 * keyboard's Next on a field moves to the next one, and its Go on the last one signs in. The
 * home's and the cloud's sign-ins talk to a MockWebServer that refuses them; what is checked is
 * that the sign-in was sent.
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
}
