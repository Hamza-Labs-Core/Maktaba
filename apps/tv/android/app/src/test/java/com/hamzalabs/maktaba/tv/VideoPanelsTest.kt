package com.hamzalabs.maktaba.tv

import androidx.activity.ComponentActivity
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.performSemanticsAction
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.AuthTokens
import com.hamzalabs.maktaba.core.session.TokenStore
import com.hamzalabs.maktaba.tv.data.TvApi
import com.hamzalabs.maktaba.tv.ui.screens.VideoScreen
import com.hamzalabs.maktaba.tv.ui.screens.library.LocalTvNav
import com.hamzalabs.maktaba.tv.ui.screens.library.TvNav
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaTvTheme
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
 * Story 54.20 on Android TV: a video's page shows its plugins' panels, the TV's subset (kv,
 * badges, markers; no chips, table or text) and open; a marker plays from its time. With the
 * home's panels off, the page never asks. Against a MockWebServer home.
 */
@RunWith(AndroidJUnit4::class)
@Config(qualifiers = "w960dp-h540dp-xhdpi")
class VideoPanelsTest {
    @get:Rule val compose = createAndroidComposeRule<ComponentActivity>()

    private lateinit var server: MockWebServer
    private val paths: MutableList<String> = Collections.synchronizedList(mutableListOf())
    private val played = mutableListOf<Pair<String, Long>>()

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

    private fun json(body: String) = MockResponse.Builder().code(200).setHeader("Content-Type", "application/json").body(body).build()

    @Before
    fun start() {
        server = MockWebServer()
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                val p = request.url.encodedPath
                paths += p
                return when (p) {
                    "/api/videos/v1" -> json("""{"id":"v1","library_id":"l1","filename":"v1.mkv","title":"The Night Journey","duration_sec":2700,"state":"ready"}""")
                    "/api/panels" -> json(
                        """{"panels":[{"plugin_id":"scenes","plugin":"Scenes","title":"Scenes","blocks":[""" +
                            """{"type":"kv","value":[{"label":"Era","value":"1920s"}]},""" +
                            """{"type":"chips","value":["History"]},""" +
                            """{"type":"markers","value":[{"t_ms":754000,"label":"The road north"}]}]}]}""",
                    )
                    else -> json("{}")
                }
            }
        }
        server.start()
    }

    @After
    fun stop() = server.close()

    private fun show(panels: Boolean) {
        val api = TvApi(ApiClient(server.url("/"), Tokens(token, "r1")))
        compose.setContent {
            MaktabaTvTheme {
                CompositionLocalProvider(LocalTvNav provides TvNav(play = { _, _, _ -> })) {
                    VideoScreen(api, "v1", onPlay = { t, ms -> played += t to ms }, signedOut = {}, panels = panels)
                }
            }
        }
    }

    private fun shows(text: String) = compose.onAllNodes(hasText(text, substring = true), useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()

    @Test
    fun theTvShowsItsSubsetOpenAndAMarkerPlays() {
        show(panels = true)
        compose.waitUntil(5_000) { compose.waitForIdle(); shows("1920s") }
        assertTrue("open on the TV: the marker shows without a tap", shows("The road north"))
        assertFalse("no chips on the TV", shows("History"))
        compose.onNode(hasText("The road north", substring = true) and hasClickAction()).performSemanticsAction(SemanticsActions.OnClick)
        compose.waitForIdle()
        assertEquals(listOf("The Night Journey" to 754_000L), played)
    }

    @Test
    fun withPanelsOffThePageNeverAsks() {
        show(panels = false)
        compose.waitUntil(5_000) { compose.waitForIdle(); shows("The Night Journey") }
        assertFalse(paths.contains("/api/panels"))
        assertFalse(shows("1920s"))
    }
}
