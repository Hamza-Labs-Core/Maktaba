package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.ErrorKind
import com.hamzalabs.maktaba.core.api.LiveApi
import com.hamzalabs.maktaba.core.api.LiveChannel
import com.hamzalabs.maktaba.core.api.Programme
import com.hamzalabs.maktaba.core.api.errorKind
import com.hamzalabs.maktaba.core.api.remoteCode
import com.hamzalabs.maktaba.core.api.rewriteToken
import com.hamzalabs.maktaba.core.api.tokenOf
import kotlinx.coroutines.runBlocking
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import okhttp3.HttpUrl.Companion.toHttpUrl
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Before
import org.junit.Test
import java.time.Instant
import java.util.Collections
import java.util.concurrent.atomic.AtomicInteger

/** Live TV's routes (story 48.9), decoded from answers shaped as the home's guide and TV handlers send them. */
class LiveApiTest {
    private lateinit var server: MockWebServer
    private val seen: MutableList<RecordedRequest> = Collections.synchronizedList(mutableListOf())
    private val nowSec = 1_800_000_000L
    private val token = jwt(nowSec + 900, "live")
    private var route: (RecordedRequest) -> MockResponse = { r -> byPath(r) }

    private fun fixture(name: String): String =
        requireNotNull(javaClass.getResource("/live/$name")) { "no fixture $name" }.readText()

    private fun json(code: Int, body: String) =
        MockResponse.Builder().code(code).setHeader("Content-Type", "application/json").body(body).build()

    private fun byPath(r: RecordedRequest): MockResponse = when (r.url.encodedPath) {
        "/api/channels/now" -> json(200, fixture("now.json"))
        "/api/channels/guide" -> json(200, fixture("guide.json"))
        "/api/channels/c1/guide" -> json(200, fixture("channel-guide.json"))
        "/api/tv/info" -> json(200, fixture("tv-info.json"))
        else -> json(404, """{"type":"https://maktaba.dev/problems/not-found","title":"Not Found","status":404}""")
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

    private fun live(path: String = "/", tokens: MemoryTokens = MemoryTokens(token, "r1")) =
        LiveApi(ApiClient(server.url(path), tokens, now = { nowSec * 1000 }))

    @Test
    fun nowDecodesEveryChannelWithWhatIsOnAndNext() = runBlocking {
        val now = live().now()
        assertEquals(2, now.channels.size)
        val quran = now.channels[0]
        assertEquals(1, quran.channel.number)
        assertEquals("قناة القرآن", quran.channel.name)
        assertTrue(quran.onAir)
        val cur = quran.current!!
        assertEquals("تفسير سورة البقرة", cur.title)
        assertTrue(cur.isLive)
        assertEquals(0.5, cur.progress, 0.0)
        assertEquals("البقرة ١٨٣", cur.stripRef)
        assertEquals("S1E45 · الآية ١٨٣", cur.episodeLabel)
        assertEquals("Seerah, part 2", quran.next?.title)
        assertNull(now.channels[1].current)
        assertFalse(now.channels[1].onAir)
        assertEquals(Instant.parse("2026-10-31T18:20:00Z").toEpochMilli(), now.serverTimeMs)

        val r = seen.single()
        assertEquals("GET", r.method)
        assertEquals("Bearer $token", r.headers["Authorization"])
        assertEquals("native", r.headers["X-Maktaba-Client"])
    }

    @Test
    fun guideAsksForTheRangeToTheSecondAndKeepsFillerMarked() = runBlocking {
        val start = Instant.parse("2026-10-31T18:00:00.250Z")
        val g = live().guide(start, start.plusSeconds(3 * 3600))
        val r = seen.single()
        assertEquals("/api/channels/guide", r.url.encodedPath)
        assertEquals("2026-10-31T18:00:00Z", r.url.queryParameter("start"))
        assertEquals("2026-10-31T21:00:00Z", r.url.queryParameter("end"))
        assertEquals(listOf("c1", "c2"), g.channels.map { it.channel.id })
        val blocks = g.channels[0].blocks
        assertEquals(listOf(false, true, false), blocks.map { it.isFiller })
        assertEquals("history", blocks[2].genre)
        assertTrue(g.channels[1].blocks.isEmpty())
    }

    @Test
    fun guideCanBeNarrowedToACategory() = runBlocking {
        val start = Instant.parse("2026-10-31T18:00:00Z")
        live().guide(start, start.plusSeconds(3600), category = "kids")
        assertEquals("kids", seen.single().url.queryParameter("category"))
    }

    @Test
    fun oneChannelsGuideCarriesItsHorizon() = runBlocking {
        val start = Instant.parse("2026-10-31T18:00:00Z")
        val g = live().channelGuide("c1", start, start.plusSeconds(6 * 3600))
        assertEquals("/api/channels/c1/guide", seen.single().url.encodedPath)
        assertEquals("c1", g.channel.id)
        assertEquals("Tafsir", g.blocks.single().title)
        assertEquals("2026-11-02T00:00:00Z", g.horizonUntil)
    }

    @Test
    fun aChannelThisUserMayNotReadIsNotFound() = runBlocking {
        // the home answers 404 for a channel of a library the user may not read (never 403)
        try {
            live().channelGuide("hidden", Instant.EPOCH, Instant.EPOCH.plusSeconds(60))
            fail("expected a refusal")
        } catch (e: ApiException.Status) {
            assertEquals(404, e.code)
            assertEquals(ErrorKind.NotFound, e.errorKind())
        }
    }

    @Test
    fun tvInfoSaysWhetherThisDeviceIsAway() = runBlocking {
        val info = live().info()
        assertEquals("/stream/channel/{id}/index.m3u8", info.hlsPathTemplate)
        assertEquals(true, info.remote?.viaRelay)
        assertEquals(true, info.remote?.available)
        assertEquals(60, info.remote?.tokenTtlMinutes)
    }

    @Test
    fun logoAndStreamUrlsAreUnderTheHomesBasePath() {
        val api = live("/maktaba/")
        val base = server.url("/maktaba/")
        val withFile = LiveChannel(id = "c1", logoUrl = "/api/channels/c1/logo")
        assertEquals(base.resolve("api/channels/c1/logo"), api.logoUrl(withFile))
        val byHand = LiveChannel(id = "c2", logo = "https://cdn.example.org/talks.png")
        assertEquals("https://cdn.example.org/talks.png".toHttpUrl(), api.logoUrl(byHand))
        assertNull(api.logoUrl(LiveChannel(id = "c3")))
        assertNull(api.logoUrl(LiveChannel(id = "c4", logo = "/var/lib/logo.png")))

        val stream = api.streamUrl("c 1")
        assertEquals("${base.encodedPath}stream/channel/c%201/index.m3u8", stream.encodedPath)
        // the player's OkHttp adds the token to this home's /stream/ only
        assertTrue(api.client.isStream(stream))

        val p = Programme(start = "2026-10-31T18:00:00Z", stop = "2026-10-31T18:40:00Z", poster = "/api/videos/v1/poster")
        assertEquals(base.resolve("api/videos/v1/poster"), api.posterUrl(p))
    }

    @Test
    fun aChannelsLogoIsFetchedWithTheToken() = runBlocking {
        route = { MockResponse.Builder().code(200).setHeader("Content-Type", "image/png").body("png").build() }
        val bytes = live().logo("c1")
        assertEquals("png", String(bytes))
        assertEquals("/api/channels/c1/logo", seen.single().url.encodedPath)
        assertEquals("Bearer $token", seen.single().headers["Authorization"])
    }

    @Test
    fun tuneHeartbeatAndEndSpeakTheRemoteRoutes() = runBlocking {
        val tunes = AtomicInteger()
        route = { r ->
            when (r.url.encodedPath) {
                "/api/tv/remote/tune" -> if (tunes.incrementAndGet() == 1) {
                    json(202, """{"session_id":"s-1","state":"starting","retry_after":3}""")
                } else {
                    json(200, fixture("tune-live.json"))
                }
                "/api/tv/remote/sessions/s-1/heartbeat" ->
                    json(200, """{"state":"live","expires_at":"2026-10-31T20:20:00Z","token":"tok-2","hls_url":"https://customer-abc.cloudflarestream.com/tok-2/manifest/video.m3u8"}""")
                "/api/tv/remote/sessions/s-1" -> MockResponse.Builder().code(204).build()
                else -> json(404, "{}")
            }
        }
        val api = live()
        val starting = api.tune("c1", "Pixel 9")
        assertEquals("starting", starting.state)
        assertEquals(3, starting.retryAfter)
        val tuned = api.tune("c1", "Pixel 9")
        assertEquals("live", tuned.state)
        assertEquals("https://customer-abc.cloudflarestream.com/tok-1/manifest/video.m3u8", tuned.hlsUrl)
        val beat = api.heartbeat("s-1")
        assertEquals("tok-2", beat.token)
        api.end("s-1")

        val tune = seen[0]
        assertEquals("POST", tune.method)
        assertEquals("""{"channel_id":"c1","device":"Pixel 9"}""", tune.body?.utf8())
        assertEquals("POST", seen[2].method)
        assertEquals("DELETE", seen[3].method)
        assertEquals("/api/tv/remote/sessions/s-1", seen[3].url.encodedPath)
    }

    @Test
    fun aRefusedTuneCarriesTheHomesCode() = runBlocking {
        route = {
            json(429, """{"type":"https://maktaba.dev/problems/user-streams-full","title":"Too Many Requests","status":429,"code":"user_streams_full"}""")
        }
        try {
            live().tune("c1", "Pixel")
            fail("expected a refusal")
        } catch (e: ApiException.Status) {
            assertEquals("user_streams_full", remoteCode(e))
        }
        assertEquals("remote_not_allowed", remoteCode(ApiException.Status(403, "")))
        assertEquals("http_500", remoteCode(ApiException.Status(500, "")))
        assertEquals("network", remoteCode(ApiException.Transport("down")))
    }

    @Test
    fun aStaleTokenIsRefreshedOnceAndTheRequestSentAgain() = runBlocking {
        val stale = jwt(nowSec + 900, "stale")
        val refreshes = AtomicInteger()
        route = { r ->
            when {
                r.url.encodedPath == "/api/auth/refresh" -> {
                    refreshes.incrementAndGet()
                    json(200, """{"access_token":"$token","refresh_token":"r2"}""")
                }
                r.headers["Authorization"] == "Bearer $token" -> json(200, fixture("now.json"))
                else -> json(401, """{"title":"unauthorized"}""")
            }
        }
        val tokens = MemoryTokens(stale, "r1")
        val now = live(tokens = tokens).now()
        assertEquals(2, now.channels.size)
        assertEquals(1, refreshes.get())
        assertEquals(token, tokens.access)
    }

    @Test
    fun aRefusedRefreshIsSignedOut() = runBlocking {
        route = { json(401, """{"title":"unauthorized"}""") }
        try {
            live().now()
            fail("expected signed out")
        } catch (e: ApiException) {
            assertTrue(e is ApiException.SignedOut)
        }
    }

    @Test
    fun progressAndOnNowFollowTheHomesClock() {
        val p = Programme(start = "2026-10-31T18:00:00Z", stop = "2026-10-31T18:40:00Z")
        val at = { s: String -> Instant.parse(s).toEpochMilli() }
        assertEquals(0.5, p.progressAt(at("2026-10-31T18:20:00Z"))!!, 1e-9)
        assertTrue(p.isAiring(at("2026-10-31T18:00:00Z")))
        assertFalse(p.isAiring(at("2026-10-31T18:40:00Z")))
        assertNull(p.progressAt(at("2026-10-31T17:59:59Z")))
        assertNull(Programme(start = "nope", stop = "2026-10-31T18:40:00Z").progressAt(0))
        assertEquals("E3", Programme(start = "", stop = "", episode = 3).episodeLabel)
        assertNull(Programme(start = "", stop = "").episodeLabel)

        // a phone whose clock is 5 minutes slow still shows the home's time
        val g = com.hamzalabs.maktaba.core.api.NowAnswer(serverTime = "2026-10-31T18:20:00Z")
        val local = at("2026-10-31T18:15:00Z")
        assertEquals(5 * 60_000L, g.clockOffsetMs(local))
        assertEquals(at("2026-10-31T18:21:00Z"), g.serverNow(local + 60_000, answeredAtLocalMs = local))
    }

    @Test
    fun theSignedTokenIsTheCloudflareUrlsFirstSegment() {
        val u = "https://customer-abc.cloudflarestream.com/tok-1/manifest/video.m3u8".toHttpUrl()
        assertEquals("tok-1", tokenOf(u))
        assertEquals(
            "https://customer-abc.cloudflarestream.com/tok-2/manifest/video.m3u8".toHttpUrl(),
            rewriteToken(u, "tok-2"),
        )
        val seg = "https://customer-abc.cloudflarestream.com/tok-1/segments/0001.ts?x=1".toHttpUrl()
        assertEquals("/tok-2/segments/0001.ts", rewriteToken(seg, "tok-2").encodedPath)
        assertEquals("x=1", rewriteToken(seg, "tok-2").encodedQuery)
        // another host, or no token segment, is left alone
        val home = "https://home.example.org/tok-1/manifest/video.m3u8".toHttpUrl()
        assertNull(tokenOf(home))
        assertEquals(home, rewriteToken(home, "tok-2"))
        val bare = "https://customer-abc.cloudflarestream.com/only".toHttpUrl()
        assertEquals(bare, rewriteToken(bare, "tok-2"))
    }
}
