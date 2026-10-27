package com.hamzalabs.maktaba.desktop

import com.hamzalabs.maktaba.core.MemoryTokens
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.jwt
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import okhttp3.OkHttpClient
import okhttp3.Request
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import java.util.Collections

class StreamProxyTest {
    private lateinit var home: MockWebServer
    private lateinit var proxy: StreamProxy
    private val seen: MutableList<RecordedRequest> = Collections.synchronizedList(mutableListOf())
    private val token = jwt(System.currentTimeMillis() / 1000 + 900, "now")
    private val http = OkHttpClient()

    @Before
    fun start() {
        home = MockWebServer()
        home.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                seen += request
                val path = request.url.encodedPath
                return when {
                    path.endsWith(".m3u8") -> MockResponse.Builder().setHeader("Content-Type", "application/vnd.apple.mpegurl")
                        .body("#EXTM3U\n${home.url("/maktaba/stream/s1/720p.m3u8").toString().trimEnd('/')}\nseg0.ts\nhttps://elsewhere.example/seg1.ts\n").build()
                    request.headers["Range"] != null -> MockResponse.Builder().code(206).setHeader("Content-Range", "bytes 0-3/10").body("abcd").build()
                    else -> MockResponse.Builder().setHeader("Content-Type", "video/mp4").body("video-bytes").build()
                }
            }
        }
        home.start()
        // a home served under a base path
        val client = ApiClient(home.url("/maktaba"), MemoryTokens(token, "refresh"))
        proxy = StreamProxy(client)
    }

    @After
    fun stop() {
        proxy.close()
        home.close()
    }

    private fun get(url: String, range: String? = null) = http.newCall(
        Request.Builder().url(url).apply { if (range != null) header("Range", range) }.build(),
    ).execute()

    @Test
    fun sendsTheTokenOnlyToThisHomesStream() {
        val local = proxy.local(home.url("/maktaba/stream/s1/file.mp4?sig=abc"))!!
        assertTrue(local.startsWith("http://127.0.0.1:") || local.startsWith("http://localhost:"))
        get(local).use { r ->
            assertEquals(200, r.code)
            assertEquals("video-bytes", r.body.string())
        }
        val req = seen.single()
        assertEquals("/maktaba/stream/s1/file.mp4", req.url.encodedPath)
        assertEquals("sig=abc", req.url.encodedQuery)
        assertEquals("Bearer $token", req.headers["Authorization"])
        // not this home's stream: no proxy address at all
        assertNull(proxy.local(home.url("/maktaba/api/me")))
    }

    @Test
    fun refusesAnyOtherPath() {
        val origin = proxy.origin
        val wrongSecret = origin.substringBeforeLast('/') + "/nope"
        for (url in listOf(
            "$wrongSecret/stream/s1/file.mp4",
            "$origin/api/auth/me",
            "$origin/stream/../api/auth/me",
            "$origin/stream/%2e%2e/api/auth/me",
        )) {
            get(url).use { assertEquals(url, 404, it.code) }
        }
        assertTrue("the home was asked: ${seen.map { it.url }}", seen.isEmpty())
    }

    @Test
    fun passesARange() {
        get(proxy.local(home.url("/maktaba/stream/s1/file.mp4"))!!, range = "bytes=0-3").use { r ->
            assertEquals(206, r.code)
            assertEquals("bytes 0-3/10", r.header("Content-Range"))
            assertEquals("abcd", r.body.string())
        }
        assertEquals("bytes=0-3", seen.single().headers["Range"])
    }

    @Test
    fun aPlaylistNamingTheHomeComesBackThroughTheProxy() {
        val text = get(proxy.local(home.url("/maktaba/stream/s1/master.m3u8"))!!).use { it.body.string() }
        val lines = text.lines()
        assertEquals("${proxy.origin}/stream/s1/720p.m3u8", lines[1])
        assertEquals("seg0.ts", lines[2])
        // another host keeps its address and never sees the token through the proxy
        assertEquals("https://elsewhere.example/seg1.ts", lines[3])
    }
}
