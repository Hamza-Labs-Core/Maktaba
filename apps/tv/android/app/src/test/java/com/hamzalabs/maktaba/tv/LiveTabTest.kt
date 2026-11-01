package com.hamzalabs.maktaba.tv

import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.AuthTokens
import com.hamzalabs.maktaba.core.session.TokenStore
import com.hamzalabs.maktaba.tv.data.TvApi
import com.hamzalabs.maktaba.tv.ui.TvTab
import com.hamzalabs.maktaba.tv.ui.screens.liveFailureWords
import com.hamzalabs.maktaba.tv.ui.tvTabs
import kotlinx.coroutines.runBlocking
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import java.util.Base64

/** The Live TV tab (48.35): shown only when the home lists a channel this user may watch. */
class LiveTabTest {
    private lateinit var server: MockWebServer
    private val nowSec = 1_800_000_000L
    private var now: () -> MockResponse = { json(200, """{"channels":[]}""") }
    private val paths = mutableListOf<String>()

    private val token: String = Base64.getUrlEncoder().withoutPadding().let { enc ->
        enc.encodeToString("""{"alg":"RS256"}""".toByteArray()) + "." +
            enc.encodeToString("""{"exp":${nowSec + 900}}""".toByteArray()) + ".sig"
    }

    private class Tokens(var a: String?, var r: String?) : TokenStore {
        override val access get() = a
        override val refresh get() = r
        override fun save(tokens: AuthTokens) { a = tokens.accessToken; r = tokens.refreshToken }
        override fun clear() { a = null; r = null }
    }

    private fun json(code: Int, body: String) =
        MockResponse.Builder().code(code).setHeader("Content-Type", "application/json").body(body).build()

    @Before
    fun start() {
        server = MockWebServer()
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                synchronized(paths) { paths += request.url.encodedPath }
                return if (request.url.encodedPath == "/api/channels/now") now() else json(404, "{}")
            }
        }
        server.start()
    }

    @After
    fun stop() = server.close()

    private fun api() = TvApi(ApiClient(server.url("/"), Tokens(token, "r1"), now = { nowSec * 1000 }))

    @Test
    fun `the tab bar has Live TV between Search and Account only with channels`() {
        assertEquals(listOf(TvTab.Home, TvTab.Libraries, TvTab.Search, TvTab.Live, TvTab.Account), tvTabs(hasLive = true))
        assertEquals(listOf(TvTab.Home, TvTab.Libraries, TvTab.Search, TvTab.Account), tvTabs(hasLive = false))
    }

    @Test
    fun `a home listing a channel this user may watch shows the tab`() = runBlocking {
        now = { json(200, """{"server_time":"2026-11-01T18:00:00Z","channels":[{"channel":{"id":"c1","number":1,"name":"One"}}]}""") }
        assertTrue(api().hasChannels())
        assertEquals(listOf("/api/channels/now"), paths)
    }

    @Test
    fun `no channels the user may watch, no tab`() = runBlocking {
        assertFalse(api().hasChannels())
    }

    @Test
    fun `a home without Live TV, or one that fails, hides the tab and nothing else`() = runBlocking {
        now = { json(404, "{}") }
        assertFalse(api().hasChannels())
        now = { json(500, "{}") }
        assertFalse(api().hasChannels())
    }

    @Test
    fun `a refused remote session has its own words, any other failure the error screen's`() {
        assertEquals(R.string.live_remote_budget, liveFailureWords("budget_exhausted"))
        assertEquals(R.string.live_remote_user_full, liveFailureWords("user_streams_full"))
        assertEquals(R.string.live_remote_channels_full, liveFailureWords("remote_channels_full"))
        assertEquals(R.string.live_remote_not_set_up, liveFailureWords("remote_unavailable"))
        assertEquals(R.string.live_remote_not_allowed, liveFailureWords("remote_not_allowed"))
        assertEquals(R.string.live_remote_timeout, liveFailureWords("remote_timeout"))
        assertEquals(R.string.live_not_found, liveFailureWords("no_such_channel"))
        assertNull(liveFailureWords("network"))
        assertNull(liveFailureWords("http_502"))
    }
}
