package com.hamzalabs.maktaba.tv

import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.AuthTokens
import com.hamzalabs.maktaba.core.session.TokenStore
import com.hamzalabs.maktaba.tv.data.Rail
import com.hamzalabs.maktaba.tv.data.RailItem
import com.hamzalabs.maktaba.tv.data.TvApi
import kotlinx.coroutines.runBlocking
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.fail
import org.junit.Before
import org.junit.Test
import java.util.Base64
import java.util.Collections

class TvApiTest {
    private lateinit var server: MockWebServer
    private val seen: MutableList<RecordedRequest> = Collections.synchronizedList(mutableListOf())
    private val nowSec = 1_800_000_000L
    private val fresh = jwt(nowSec + 900, "fresh")
    private val stale = jwt(nowSec + 900, "stale")
    private var route: (RecordedRequest) -> MockResponse = { defaultRoute(it) }

    private fun jwt(expSec: Long, tag: String): String {
        val enc = Base64.getUrlEncoder().withoutPadding()
        return enc.encodeToString("""{"alg":"RS256"}""".toByteArray()) + "." +
            enc.encodeToString("""{"exp":$expSec,"tag":"$tag"}""".toByteArray()) + ".sig"
    }

    private class Tokens(var a: String?, var r: String?) : TokenStore {
        override val access get() = a
        override val refresh get() = r
        override fun save(tokens: AuthTokens) { a = tokens.accessToken; r = tokens.refreshToken }
        override fun clear() { a = null; r = null }
    }

    private fun json(code: Int, body: String) =
        MockResponse.Builder().code(code).setHeader("Content-Type", "application/json").body(body).build()

    private fun defaultRoute(r: RecordedRequest): MockResponse = when {
        r.url.encodedPath == "/api/auth/refresh" -> json(200, """{"access_token":"$fresh","refresh_token":"r2"}""")
        r.headers["Authorization"] != "Bearer $fresh" -> json(401, "{}")
        r.url.encodedPath == "/api/videos/v1" ->
            json(200, """{"id":"v1","filename":"a.mp4","title":"A","display_title":"The A","duration_sec":600,"playback_state":{"position_sec":125,"completed":false}}""")
        r.url.encodedPath == "/api/videos/gone" -> json(404, "{}")
        r.url.encodedPath == "/api/videos" -> json(200, """{"items":[{"id":"v1","filename":"a.mp4"}],"next":"c2"}""")
        else -> json(404, "{}")
    }

    @Before
    fun start() {
        server = MockWebServer()
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                seen += request
                return route(request)
            }
        }
        server.start()
    }

    @After
    fun stop() = server.close()

    private fun api(tokens: Tokens) = TvApi(ApiClient(server.url("/"), tokens, now = { nowSec * 1000 }))

    @Test
    fun aDetailCarriesTheNameAndWhereThisPersonStopped() = runBlocking {
        val v = api(Tokens(fresh, "r1")).video("v1")
        assertEquals("The A", v.displayTitle)
        assertEquals(125.0, v.resumeSec!!, 0.0)
        assertEquals("Bearer $fresh", seen.single().headers["Authorization"])
        assertEquals("native", seen.single().headers["X-Maktaba-Client"])
    }

    @Test
    fun aRejectedTokenIsRefreshedOnceAndTheRequestSentAgain() = runBlocking {
        val tokens = Tokens(stale, "r1")
        val page = api(tokens).videos(library = "lib1")
        assertEquals(listOf("v1"), page.items.map { it.id })
        assertEquals(listOf("/api/videos", "/api/auth/refresh", "/api/videos"), seen.map { it.url.encodedPath })
        assertEquals(fresh, tokens.access)
        // the list asks for a sort the home accepts
        assertEquals("created_at", seen.last().url.queryParameter("sort"))
        assertEquals("lib1", seen.last().url.queryParameter("library"))
    }

    @Test
    fun aRefusedRefreshSignsOut() = runBlocking {
        route = { r -> if (r.url.encodedPath == "/api/auth/refresh") json(401, "{}") else json(401, "{}") }
        val tokens = Tokens(stale, "r1")
        try {
            api(tokens).video("v1")
            fail("expected SignedOut")
        } catch (e: ApiException.SignedOut) {
            assertEquals(null, tokens.access)
        }
    }

    @Test
    fun aRailKeepsItsOrderAndDropsVideosThatCannotBeRead() = runBlocking {
        val rail = Rail("continue", items = listOf(RailItem("gone"), RailItem("v1", 300.0, 600.0)))
        val got = api(Tokens(fresh, "r1")).railVideos(rail)
        assertEquals(listOf("v1"), got.map { it.second.id })
        assertEquals(0.5f, got.single().first.progress, 0f)
    }
}
