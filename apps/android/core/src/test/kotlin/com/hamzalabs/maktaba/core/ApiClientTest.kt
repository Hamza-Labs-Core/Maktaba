package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.AuthTokens
import com.hamzalabs.maktaba.core.api.ErrorKind
import com.hamzalabs.maktaba.core.api.errorKind
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.runBlocking
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import okhttp3.Request
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Before
import org.junit.Test
import java.util.Base64
import java.util.Collections
import java.util.concurrent.atomic.AtomicInteger

/** A JWT the client can read the expiry of (its signature is not checked on the device). */
fun jwt(expSec: Long, tag: String = "t"): String {
    val enc = Base64.getUrlEncoder().withoutPadding()
    val head = enc.encodeToString("""{"alg":"RS256"}""".toByteArray())
    val body = enc.encodeToString("""{"sub":"u1","exp":$expSec,"tag":"$tag"}""".toByteArray())
    return "$head.$body.sig"
}

/** The tokens in memory. */
class MemoryTokens(var accessToken: String? = null, var refreshToken: String? = null) :
    com.hamzalabs.maktaba.core.session.TokenStore {
    override val access get() = accessToken
    override val refresh get() = refreshToken
    override fun save(tokens: AuthTokens) {
        accessToken = tokens.accessToken
        refreshToken = tokens.refreshToken
    }
    override fun clear() {
        accessToken = null
        refreshToken = null
    }
}

class ApiClientTest {
    private lateinit var server: MockWebServer
    private val seen: MutableList<RecordedRequest> = Collections.synchronizedList(mutableListOf())
    private val refreshes = AtomicInteger()
    private val nowSec = 1_800_000_000L
    private val fresh = jwt(nowSec + 900, "fresh")
    private val stale = jwt(nowSec + 900, "stale")

    /** Answers by path; the default home accepts only [fresh]. */
    private var route: (RecordedRequest) -> MockResponse = { r -> defaultRoute(r) }

    private fun json(code: Int, body: String) =
        MockResponse.Builder().code(code).setHeader("Content-Type", "application/json").body(body).build()

    private fun defaultRoute(r: RecordedRequest): MockResponse = when (r.url.encodedPath) {
        "/api/auth/refresh" -> {
            refreshes.incrementAndGet()
            Thread.sleep(50)
            json(200, """{"access_token":"$fresh","refresh_token":"r2"}""")
        }
        else -> if (r.headers["Authorization"] == "Bearer $fresh") {
            json(200, """{"items":[{"id":"l1","name":"Talks"}],"next":null}""")
        } else {
            json(401, """{"title":"unauthorized"}""")
        }
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

    private fun client(tokens: MemoryTokens) = ApiClient(server.url("/"), tokens, now = { nowSec * 1000 })

    @Test
    fun signInIsANativeClientAndKeepsTheTokens() = runBlocking {
        route = { json(200, """{"access_token":"$fresh","refresh_token":"r1","user":{"id":"u1","username":"amina","is_admin":true}}""") }
        val tokens = MemoryTokens()
        val user = client(tokens).signIn("amina", "pw")
        assertEquals("amina", user?.username)
        assertEquals(true, user?.isAdmin)
        assertEquals(fresh, tokens.access)
        assertEquals("r1", tokens.refresh)
        val r = seen.single()
        assertEquals("native", r.headers["X-Maktaba-Client"])
        assertNull("sign-in sends no token", r.headers["Authorization"])
        assertTrue(r.body!!.utf8().contains("\"username\":\"amina\""))
    }

    @Test
    fun theVideoListAsksNewestFirstWithASortTheHomeAccepts() {
        val q = ApiClient.videoListQuery("lib1", null, "c2")
        assertEquals(mapOf("limit" to "60", "sort" to "created_at", "library" to "lib1", "cursor" to "c2"), q)
        assertEquals(mapOf("limit" to "60", "sort" to "created_at"), ApiClient.videoListQuery(null, "", null))
        val r = client(MemoryTokens()).request("GET", "/api/videos", q)
        assertEquals("created_at", r.url.queryParameter("sort"))
        assertEquals("native", r.header("X-Maktaba-Client"))
    }

    @Test
    fun aSearchSendsNoSortSoTheHomeRanksIt() {
        // best match, then the most recently added: the home's order for a search
        assertEquals(
            mapOf("limit" to "60", "q" to "quran", "library" to "lib1", "cursor" to "c2"),
            ApiClient.videoListQuery("lib1", "quran", "c2"),
        )
    }

    @Test
    fun a401RefreshesOnceAndRetries() = runBlocking {
        val tokens = MemoryTokens(stale, "r1")
        val libs = client(tokens).libraries()
        assertEquals(listOf("Talks"), libs.map { it.name })
        assertEquals(fresh, tokens.access)
        assertEquals("r2", tokens.refresh)
        assertEquals(1, refreshes.get())
        val refresh = seen.single { it.url.encodedPath == "/api/auth/refresh" }
        assertTrue(refresh.body!!.utf8().contains("\"refresh_token\":\"r1\""))
        assertNull(refresh.headers["Authorization"])
    }

    @Test
    fun requestsThat401TogetherShareOneRefresh() = runBlocking {
        val tokens = MemoryTokens(stale, "r1")
        val c = client(tokens)
        val all = (1..8).map { async(kotlinx.coroutines.Dispatchers.IO) { c.libraries() } }.awaitAll()
        assertTrue(all.all { it.size == 1 })
        assertEquals("one refresh for all of them", 1, refreshes.get())
    }

    @Test
    fun aRefusedRefreshSignsOut() = runBlocking {
        route = { r -> if (r.url.encodedPath == "/api/auth/refresh") json(401, "{}") else json(401, "{}") }
        val tokens = MemoryTokens(stale, "r1")
        try {
            client(tokens).libraries()
            fail("expected SignedOut")
        } catch (e: ApiException.SignedOut) {
            // expected
        }
        assertNull(tokens.access)
        assertNull(tokens.refresh)
    }

    @Test
    fun anUnreachableHomeDoesNotSignOut() = runBlocking {
        val tokens = MemoryTokens(jwt(nowSec + 10), "r1")
        server.close()
        try {
            client(tokens).playbackToken()
            fail("expected a transport error")
        } catch (e: ApiException.Transport) {
            // expected
        }
        assertEquals("r1", tokens.refresh)
    }

    @Test
    fun playbackTokenRefreshesOnlyNearItsEnd() = runBlocking {
        val long = jwt(nowSec + 600, "long")
        assertEquals(long, client(MemoryTokens(long, "r1")).playbackToken(120))
        assertEquals(0, refreshes.get())
        val short = MemoryTokens(jwt(nowSec + 30, "short"), "r1")
        assertEquals(fresh, client(short).playbackToken(120))
        assertEquals(1, refreshes.get())
    }

    @Test
    fun waitUntilPlayableWaitsForTheFirstRendition() = runBlocking {
        val rendition = AtomicInteger()
        route = { r ->
            when (r.url.encodedPath) {
                "/stream/s1/master.m3u8" -> json(200, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nv0/index.m3u8\n")
                "/stream/s1/v0/index.m3u8" ->
                    if (rendition.incrementAndGet() < 3) json(404, "not yet") else json(200, "#EXTM3U\n")
                else -> json(500, "")
            }
        }
        val tokens = MemoryTokens(fresh, "r1")
        client(tokens).waitUntilPlayable(server.url("/stream/s1/master.m3u8?sig=x"), intervalMs = 1)
        assertEquals(3, rendition.get())
        assertTrue("every /stream/ request carries the token", seen.all { it.headers["Authorization"] == "Bearer $fresh" })
    }

    @Test
    fun waitUntilPlayableGivesUp() = runBlocking {
        route = { r ->
            if (r.url.encodedPath.endsWith("master.m3u8")) json(200, "#EXTM3U\nv0.m3u8\n") else json(404, "missing")
        }
        try {
            client(MemoryTokens(fresh, "r1")).waitUntilPlayable(server.url("/stream/s1/master.m3u8"), timeoutMs = 5, intervalMs = 1)
            fail("expected not ready")
        } catch (e: ApiException.NotReady) {
            assertEquals(404, e.code)
            assertEquals(ErrorKind.NotReady, e.errorKind())
        }
    }

    @Test
    fun waitUntilPlayableWaitsForTheMasterTooWithBackoff() = runBlocking {
        val master = AtomicInteger()
        val notYet = """{"type":"https://maktaba.invalid/problems/manifest-not-found","title":"no manifest","status":404}"""
        route = { r ->
            when (r.url.encodedPath) {
                "/stream/s1/master.m3u8" ->
                    if (master.incrementAndGet() < 4) json(404, notYet) else json(200, "#EXTM3U\nv0/index.m3u8\n")
                "/stream/s1/v0/index.m3u8" -> json(200, "#EXTM3U\n")
                else -> json(500, "")
            }
        }
        val started = System.nanoTime()
        client(MemoryTokens(fresh, "r1")).waitUntilPlayable(server.url("/stream/s1/master.m3u8"), intervalMs = 10, maxIntervalMs = 40)
        val tookMs = (System.nanoTime() - started) / 1_000_000
        assertEquals(4, master.get())
        assertTrue("waited 10 + 20 + 40 ms between the asks, not more: $tookMs", tookMs >= 70)
    }

    @Test
    fun waitUntilPlayableDoesNotRetryARefusal() = runBlocking {
        val asks = AtomicInteger()
        route = { r ->
            if (r.url.encodedPath.endsWith("master.m3u8")) json(200, "#EXTM3U\nv0.m3u8\n")
            else { asks.incrementAndGet(); json(403, """{"type":"https://maktaba.dev/problems/forbidden","status":403}""") }
        }
        try {
            client(MemoryTokens(fresh, "r1")).waitUntilPlayable(server.url("/stream/s1/master.m3u8"), intervalMs = 1)
            fail("expected a 403")
        } catch (e: ApiException.Status) {
            assertEquals(403, e.code)
            assertEquals(ErrorKind.NoAccess, e.errorKind())
        }
        assertEquals("asked once", 1, asks.get())
    }

    @Test
    fun aBusyHomeSaysWhenToComeBack() = runBlocking {
        route = { MockResponse.Builder().code(503).setHeader("Retry-After", "7").body("""{"title":"busy","status":503}""").build() }
        try {
            client(MemoryTokens(fresh, "r1")).libraries()
            fail("expected a 503")
        } catch (e: ApiException.Status) {
            assertEquals(7L, e.retryAfterSec)
            assertEquals(ErrorKind.Busy, e.errorKind())
            assertTrue(e.message!!, !e.message!!.contains("{"))
        }
    }

    @Test
    fun aDirectFileNeedsNoWait() = runBlocking {
        client(MemoryTokens(fresh, "r1")).waitUntilPlayable(server.url("/stream/s1/file.mp4"))
        assertTrue(seen.isEmpty())
    }

    @Test
    fun theStreamClientSendsTheTokenOnlyToThisHomesStream() {
        val tokens = MemoryTokens(stale, "r1")
        route = { r ->
            when {
                r.url.encodedPath == "/api/auth/refresh" -> defaultRoute(r)
                r.url.encodedPath.startsWith("/stream/") && r.headers["Authorization"] != "Bearer $fresh" -> json(401, "")
                else -> json(200, "ok")
            }
        }
        val c = client(tokens)
        val http = c.streamHttp()
        http.newCall(Request.Builder().url(server.url("/stream/s1/seg0.ts")).build()).execute().use {
            assertEquals(200, it.code)
        }
        assertEquals("a 401 refreshed and retried once", 1, refreshes.get())
        http.newCall(Request.Builder().url(server.url("/api/videos")).build()).execute().close()
        val segs = seen.filter { it.url.encodedPath == "/stream/s1/seg0.ts" }
        assertEquals(listOf("Bearer $stale", "Bearer $fresh"), segs.map { it.headers["Authorization"] })
        assertNull("not /stream/: no token", seen.last { it.url.encodedPath == "/api/videos" }.headers["Authorization"])
        val home = server.url("/stream/s1/x.ts")
        assertTrue(c.isStream(home))
        assertTrue("another host's stream gets no token", !c.isStream(home.newBuilder().host("cdn.example.com").build()))
        assertTrue("another port's neither", !c.isStream(home.newBuilder().port(home.port + 1).build()))
        assertTrue("https is another origin", !c.isStream(home.newBuilder().scheme("https").build()))
    }

    @Test
    fun aBusyHomeDoesNotSignOut() = runBlocking {
        route = { r -> if (r.url.encodedPath == "/api/auth/refresh") json(429, "slow down") else json(401, "{}") }
        val tokens = MemoryTokens(stale, "r1")
        try {
            client(tokens).libraries()
            fail("expected a 429")
        } catch (e: ApiException.Status) {
            assertEquals(429, e.code)
        }
        assertEquals("a 429 keeps the tokens", "r1", tokens.refresh)
    }

    @Test
    fun aFastClockDoesNotRefreshOnEveryRequest() = runBlocking {
        // this device runs 20 minutes ahead: every token it gets looks already expired
        val c = ApiClient(server.url("/"), MemoryTokens(jwt(nowSec - 60), "r1"), now = { (nowSec + 1200) * 1000 })
        repeat(5) { c.playbackToken(60) }
        assertEquals("one refresh, then the fresh token is used", 1, refreshes.get())
    }

    @Test
    fun waitingForAStreamRefreshesOnA401AndStaysOnThisHome() = runBlocking {
        route = { r ->
            when {
                r.url.encodedPath == "/api/auth/refresh" -> defaultRoute(r)
                r.headers["Authorization"] != "Bearer $fresh" -> json(401, "")
                r.url.encodedPath.endsWith("master.m3u8") -> json(200, "#EXTM3U\nhttps://cdn.example.com/v0.m3u8\n")
                else -> json(200, "#EXTM3U\n")
            }
        }
        try {
            client(MemoryTokens(stale, "r1")).waitUntilPlayable(server.url("/stream/s1/master.m3u8"), intervalMs = 1)
            fail("a rendition on another host must not get the token")
        } catch (e: ApiException.Status) {
            assertTrue(e.body.contains("cdn.example.com"))
        }
        assertEquals("the master's 401 refreshed once", 1, refreshes.get())
        assertTrue("nothing went to another host", seen.all { it.url.host == server.url("/").host })
    }

    @Test
    fun readsAJwtsExpiry() {
        assertEquals((nowSec + 900) * 1000, ApiClient.jwtExpiry(fresh))
        assertNull(ApiClient.jwtExpiry("not-a-jwt"))
        assertNull(ApiClient.jwtExpiry("a.b.c"))
    }

    @Test
    fun findsAPlaylistsFirstUri() {
        assertEquals("v0/index.m3u8", ApiClient.firstUri("#EXTM3U\n\n#EXT-X-STREAM-INF:BANDWIDTH=1\n  v0/index.m3u8 \nv1.m3u8"))
        assertNull(ApiClient.firstUri("#EXTM3U\n#EXT-X-ENDLIST\n"))
    }

    @Test
    fun aRedirectToAnotherOriginDropsTheToken() = runBlocking {
        val elsewhere = MockWebServer()
        val there = Collections.synchronizedList(mutableListOf<RecordedRequest>())
        elsewhere.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                there += request
                return json(200, """{"items":[],"next":null}""")
            }
        }
        elsewhere.start()
        try {
            route = { r ->
                when (r.url.encodedPath) {
                    // same origin, another path: the token goes along
                    "/api/libraries" -> MockResponse.Builder().code(302).setHeader("Location", "/api/libraries2").build()
                    "/api/libraries2" -> MockResponse.Builder().code(302).setHeader("Location", elsewhere.url("/api/libraries").toString()).build()
                    else -> json(404, "{}")
                }
            }
            client(MemoryTokens(fresh, "r1")).libraries()
            assertEquals("Bearer $fresh", seen[0].headers["Authorization"])
            assertEquals("a same-origin redirect keeps it", "Bearer $fresh", seen[1].headers["Authorization"])
            assertEquals(1, there.size)
            assertNull("another origin never gets the token", there.single().headers["Authorization"])
        } finally {
            elsewhere.close()
        }
    }

    @Test
    fun anOriginIsSchemeHostAndPort() {
        val a = okhttp3.HttpUrl.Builder().scheme("http").host("h").port(8088).build()
        assertTrue(com.hamzalabs.maktaba.core.api.SameOriginCredentials.sameOrigin(a, a.newBuilder().encodedPath("/x").build()))
        assertTrue(!com.hamzalabs.maktaba.core.api.SameOriginCredentials.sameOrigin(a, a.newBuilder().port(8089).build()))
        assertTrue(!com.hamzalabs.maktaba.core.api.SameOriginCredentials.sameOrigin(a, a.newBuilder().scheme("https").port(8088).build()))
    }
}
