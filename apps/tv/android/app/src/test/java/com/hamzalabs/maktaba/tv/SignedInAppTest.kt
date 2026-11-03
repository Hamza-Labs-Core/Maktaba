package com.hamzalabs.maktaba.tv

import androidx.activity.ComponentActivity
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.performSemanticsAction
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.AuthTokens
import com.hamzalabs.maktaba.core.api.Capabilities
import com.hamzalabs.maktaba.core.api.Feature
import com.hamzalabs.maktaba.core.api.User
import com.hamzalabs.maktaba.core.session.TokenStore
import com.hamzalabs.maktaba.tv.ui.SignedInApp
import com.hamzalabs.maktaba.tv.ui.TvTab
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaTvTheme
import com.hamzalabs.maktaba.tv.ui.tvTabs
import kotlinx.coroutines.flow.MutableStateFlow
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import java.util.Base64
import java.util.Collections

/**
 * Story 51.11 T12: the TV's tab bar with Live TV off on the home. A viewer has no Live TV tab,
 * even with channels listed; an admin keeps it, and the tab says Live TV is off with the way to
 * turn it on. Against a MockWebServer home; the app's log goes to the report (robolectric.logging).
 */
@RunWith(AndroidJUnit4::class)
@Config(qualifiers = "w960dp-h540dp-xhdpi")
class SignedInAppTest {
    @get:Rule val compose = createAndroidComposeRule<ComponentActivity>()

    private lateinit var server: MockWebServer
    private val paths: MutableList<String> = Collections.synchronizedList(mutableListOf())
    private val features = MutableStateFlow(Capabilities.AllOn)

    private class Tokens(var a: String?, var r: String?) : TokenStore {
        override val access get() = a
        override val refresh get() = r
        override fun save(tokens: AuthTokens) { a = tokens.accessToken; r = tokens.refreshToken }
        override fun clear() { a = null; r = null }
    }

    private val token: String = Base64.getUrlEncoder().withoutPadding().let { enc ->
        enc.encodeToString("""{"alg":"RS256"}""".toByteArray()) + "." +
            enc.encodeToString("""{"exp":${System.currentTimeMillis() / 1000 + 86_400}}""".toByteArray()) + ".sig"
    }

    private fun json(code: Int, body: String) =
        MockResponse.Builder().code(code).setHeader("Content-Type", "application/json").body(body).build()

    @Before
    fun start() {
        server = MockWebServer()
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                paths += request.url.encodedPath
                return when (request.url.encodedPath) {
                    "/api/channels/now" -> json(200, """{"server_time":"2026-11-01T18:00:00Z","channels":[{"channel":{"id":"c1","number":1,"name":"One"}}]}""")
                    else -> json(404, "{}")
                }
            }
        }
        server.start()
    }

    @After
    fun stop() = server.close()

    private fun signedIn() {
        val app = ApplicationProvider.getApplicationContext<MaktabaTVApp>()
        val client = ApiClient(server.url("/"), Tokens(token, "r1"))
        compose.setContent {
            MaktabaTvTheme { SignedInApp(app, User(id = "u1", username = "layla"), client, features) }
        }
    }

    private fun shows(text: String) = compose.onAllNodes(hasText(text), useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()

    @Test
    fun withEverythingOnAChannelShowsTheLiveTvTab() {
        signedIn()
        compose.waitUntil(5_000) { compose.waitForIdle(); shows("Live TV") }
    }

    @Test
    fun aViewerHasNoLiveTvTabWhileItIsOff() {
        features.value = Capabilities(off = setOf(Feature.LIVE_TV))
        signedIn()
        compose.waitUntil(5_000) { compose.waitForIdle(); shows("Home") }
        compose.waitForIdle()
        assertFalse("no Live TV tab", shows("Live TV"))
        assertFalse("nothing asks for channels while Live TV is off", "/api/channels/now" in paths)
    }

    @Test
    fun anAdminKeepsTheTabAndReadsWhy() {
        features.value = Capabilities(off = setOf(Feature.LIVE_TV), admin = true)
        signedIn()
        compose.waitUntil(5_000) { compose.waitForIdle(); shows("Live TV") }
        // a TV tab is chosen when it takes focus (the D-pad)
        compose.onNodeWithText("Live TV").performSemanticsAction(SemanticsActions.RequestFocus)
        compose.waitUntil(5_000) { compose.waitForIdle(); shows("Live TV is off on this home.") }
        compose.onNodeWithTag("feature-turn-on").assertExists()
        assertTrue(shows("Turn it on in Components"))
    }

    @Test
    fun theTabListFollowsTheRule() {
        val viewer = Capabilities(off = setOf(Feature.LIVE_TV))
        val admin = Capabilities(off = setOf(Feature.LIVE_TV), admin = true)
        assertEquals(listOf(TvTab.Home, TvTab.Libraries, TvTab.Search, TvTab.Account), tvTabs(hasLive = true, viewer))
        assertEquals(TvTab.entries.toList(), tvTabs(hasLive = false, admin))
        assertEquals(TvTab.entries.toList(), tvTabs(hasLive = true))
    }
}
