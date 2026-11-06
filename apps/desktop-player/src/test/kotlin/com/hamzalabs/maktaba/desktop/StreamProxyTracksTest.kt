package com.hamzalabs.maktaba.desktop

import com.hamzalabs.maktaba.core.MemoryTokens
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.jwt
import kotlinx.coroutines.runBlocking
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.OkHttpClient
import okhttp3.Request
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import java.util.Collections

/** The proxy's rules for stories 55.07 (relay token) and 55.08 (audio and subtitles). */
class StreamProxyTracksTest {
    private lateinit var home: MockWebServer
    private lateinit var proxy: StreamProxy
    private val seen: MutableList<RecordedRequest> = Collections.synchronizedList(mutableListOf())
    private val token = jwt(System.currentTimeMillis() / 1000 + 900, "now")

    private val master = """
        #EXTM3U
        #EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME="Arabic",LANGUAGE="ar",DEFAULT=YES,URI="audio/ar.m3u8"
        #EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME="English",LANGUAGE="en",DEFAULT=NO,URI="audio/en.m3u8"
        #EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="subs",NAME="Arabic",LANGUAGE="ar",URI="subs/ar.m3u8"
        #EXT-X-STREAM-INF:BANDWIDTH=4000000,AUDIO="aud",SUBTITLES="subs"
        720p.m3u8
    """.trimIndent()

    @Before
    fun start() {
        home = MockWebServer()
        home.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                seen += request
                val body = when (request.url.encodedPath) {
                    "/stream/s1/master.m3u8" -> master
                    "/stream/s1/subs/ar.m3u8" -> "#EXTM3U\n#EXTINF:60,\nar0.vtt\n#EXTINF:60,\nar1.vtt\n#EXT-X-ENDLIST"
                    "/stream/s1/subs/ar0.vtt" -> "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nبسم الله\n"
                    "/stream/s1/subs/ar1.vtt" -> "WEBVTT\n\n00:01:01.000 --> 00:01:02.000\nالحمد لله\n"
                    else -> return MockResponse.Builder().code(404).build()
                }
                return MockResponse.Builder().setHeader("Content-Type", "application/vnd.apple.mpegurl").body(body).build()
            }
        }
        home.start()
        proxy = StreamProxy(ApiClient(home.url("/"), MemoryTokens(token, "refresh")))
    }

    @After
    fun stop() {
        proxy.close()
        home.close()
    }

    private fun get(url: String) = OkHttpClient().newCall(Request.Builder().url(url).build()).execute().use { it.body.string() }

    @Test
    fun theChosenAudioIsTheOnlyOneJavaFxSees() {
        val url = proxy.local(home.url("/stream/s1/master.m3u8"))!!
        val first = get(url)
        assertEquals(listOf("ar", "en"), proxy.master.value!!.audio.map { it.language })
        assertEquals(2, first.lines().count { it.startsWith("#EXT-X-MEDIA:TYPE=AUDIO") })
        assertFalse("no subtitles for JavaFX", first.contains("TYPE=SUBTITLES"))
        proxy.audio = "English"
        val chosen = get(url).lines().filter { it.startsWith("#EXT-X-MEDIA:TYPE=AUDIO") }
        assertEquals(1, chosen.size)
        assertTrue(chosen[0], chosen[0].contains("LANGUAGE=\"en\"") && chosen[0].contains("DEFAULT=YES"))
        proxy.forget()
        assertNull(proxy.master.value)
    }

    @Test
    fun subtitlesAreFetchedFromTheHomeWithTheToken() = runBlocking {
        get(proxy.local(home.url("/stream/s1/master.m3u8"))!!)
        val cues = proxy.cues(proxy.master.value!!.subtitles.single())
        assertEquals(listOf("بسم الله", "الحمد لله"), cues.map { it.text })
        val vtt = seen.first { it.url.encodedPath.endsWith("ar0.vtt") }
        assertEquals("Bearer $token", vtt.headers["Authorization"])
    }

    @Test
    fun aRelayRequestCarriesTheNewestTokenInItsPath() {
        val old = "https://customer-x.cloudflarestream.com/OLDTOKEN/manifest/video.m3u8".toHttpUrl()
        val local = proxy.relaySession(old)!!
        assertTrue(local, local.startsWith(proxy.origin + "/relay/customer-x.cloudflarestream.com/OLDTOKEN/"))
        val rawPath = local.removePrefix(proxy.origin.substringBeforeLast('/')).substringBefore('?')
        assertEquals(old, proxy.relayRemote(rawPath, null))
        proxy.relayToken = { "NEWTOKEN" }
        assertEquals("https://customer-x.cloudflarestream.com/NEWTOKEN/manifest/video.m3u8", proxy.relayRemote(rawPath, null).toString())
        // a segment under it, and an absolute URL in a relay playlist, come back through the relay
        val seg = rawPath.replace("manifest/video.m3u8", "seg/1.ts")
        assertEquals("https://customer-x.cloudflarestream.com/NEWTOKEN/seg/1.ts", proxy.relayRemote(seg, null).toString())
        val rewritten = proxy.rewriteRelay("#EXTM3U\nhttps://customer-x.cloudflarestream.com/OLDTOKEN/seg/2.ts\n#EXT-X-KEY:METHOD=AES-128,URI=\"https://customer-x.cloudflarestream.com/OLDTOKEN/key\"\nhttps://elsewhere.example/x.ts")
        val lines = rewritten.lines()
        assertTrue(lines[1].startsWith(proxy.origin + "/relay/"))
        assertTrue(lines[2], lines[2].contains("URI=\"${proxy.origin}/relay/"))
        assertEquals("https://elsewhere.example/x.ts", lines[3])
    }

    @Test
    fun theRelayFetchesOnlyTheSessionsOwnHost() {
        val secret = proxy.origin.substringAfterLast('/')
        // before a session, no Cloudflare host at all
        assertNull(proxy.relayLocal("https://customer-x.cloudflarestream.com/T/manifest/video.m3u8".toHttpUrl()))
        assertNull(proxy.relayRemote("/$secret/relay/customer-x.cloudflarestream.com/T/x.ts", null))
        proxy.relaySession("https://customer-x.cloudflarestream.com/T/manifest/video.m3u8".toHttpUrl())!!
        assertNull(proxy.relayLocal("https://customer-y.cloudflarestream.com/T/x.ts".toHttpUrl()))
        assertNull(proxy.relayRemote("/$secret/relay/customer-y.cloudflarestream.com/T/x.ts", null))
        assertEquals("https://customer-x.cloudflarestream.com/T/x.ts", proxy.relayRemote("/$secret/relay/customer-x.cloudflarestream.com/T/x.ts", null).toString())
    }

    @Test
    fun noOtherHostOrPathGetsAToken() {
        assertNull(proxy.relayLocal("https://evil.example/OLD/manifest/video.m3u8".toHttpUrl()))
        assertNull("not https", proxy.relayLocal("http://customer-x.cloudflarestream.com/T/manifest/video.m3u8".toHttpUrl()))
        val secret = proxy.origin.substringAfterLast('/')
        assertNull(proxy.relayRemote("/$secret/relay/evil.example/T/x.ts", null))
        assertNull(proxy.relayRemote("/$secret/relay/evil.example@x.cloudflarestream.com/T/x.ts", null))
        assertNull(proxy.relayRemote("/wrong/relay/customer-x.cloudflarestream.com/T/x.ts", null))
        // a relay path is never a home path
        assertNull(proxy.remote("/$secret/relay/customer-x.cloudflarestream.com/T/x.ts", null))
        // the home's own non-stream paths stay refused
        assertNull(proxy.local(home.url("/api/auth/me")))
        // the home's live HLS at home is a stream path: it gets the token
        assertTrue(proxy.local(home.url("/stream/channel/c1/index.m3u8")) != null)
    }
}
