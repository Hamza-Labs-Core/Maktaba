package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.LiveFailure
import com.hamzalabs.maktaba.core.api.LiveSource
import com.hamzalabs.maktaba.core.api.LiveStream
import com.hamzalabs.maktaba.core.api.RemoteBeat
import com.hamzalabs.maktaba.core.api.RemoteTuned
import com.hamzalabs.maktaba.core.api.RemoteTv
import com.hamzalabs.maktaba.core.api.TvInfo
import com.hamzalabs.maktaba.core.api.TvRemote
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.currentTime
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.OkHttpClient
import okhttp3.Protocol
import okhttp3.Request
import okhttp3.Response
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test

/**
 * Where a channel plays from, and the remote session kept alive (story 48.9). The clock is the
 * test's virtual time: a heartbeat is due every 60 s of it, and a 202 is asked again after its
 * `retry_after`.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class LiveSourceTest {
    private val lan = { id: String -> "http://home.lan:8088/stream/channel/$id/index.m3u8".toHttpUrl() }
    private fun cf(token: String) = "https://customer-abc.cloudflarestream.com/$token/manifest/video.m3u8"

    /** A home: [viaRelay] away from home; [starting] 202s before each tune is live; [beats] the heartbeats' answers in turn. */
    private class FakeHome(
        var viaRelay: Boolean = true,
        var available: Boolean = true,
        var starting: Int = 0,
        val beats: ArrayDeque<() -> RemoteBeat> = ArrayDeque(),
    ) : RemoteTv {
        val calls = mutableListOf<String>()
        private var sessions = 0
        /** 202s left before the session being tuned is live (null: no tune under way). */
        private var left: Int? = null

        override suspend fun info(): TvInfo {
            calls += "info"
            return TvInfo(remote = TvRemote(available = available, viaRelay = viaRelay, reason = if (available) null else "off"))
        }

        override suspend fun tune(channelId: String, device: String): RemoteTuned {
            calls += "tune $channelId"
            val n = left ?: starting.also { sessions++ }
            val id = "s-$sessions"
            if (n > 0) {
                left = n - 1
                return RemoteTuned(sessionId = id, state = "starting", retryAfter = 3)
            }
            left = null
            return RemoteTuned(sessionId = id, state = "live", hlsUrl = "https://customer-abc.cloudflarestream.com/tok-$sessions/manifest/video.m3u8")
        }

        override suspend fun heartbeat(sessionId: String): RemoteBeat {
            calls += "beat $sessionId"
            return beats.removeFirstOrNull()?.invoke() ?: RemoteBeat(state = "live")
        }

        override suspend fun end(sessionId: String) {
            calls += "end $sessionId"
        }
    }

    private fun TestScope.source(home: FakeHome, onEnded: (LiveStream.Relay) -> Unit = {}) =
        LiveSource(home, lan, backgroundScope, "Pixel 9", onEnded = onEnded)

    @Test
    fun atHomeItPlaysTheWebPortsStreamAndTunesNothing() = runTest {
        val home = FakeHome(viaRelay = false)
        val s = source(home).open("c1")
        assertEquals(LiveStream.Lan("c1", lan("c1")), s)
        assertEquals(listOf("info"), home.calls)
        advanceTimeBy(5 * 60_000)
        assertEquals(listOf("info"), home.calls) // no heartbeat at home
    }

    @Test
    fun awayItTunesAndAsksAgainUntilTheStreamIsLive() = runTest {
        val home = FakeHome(starting = 2)
        val src = source(home)
        val started = currentTime
        val s = src.open("c1") as LiveStream.Relay
        assertEquals("s-1", s.sessionId)
        assertEquals(cf("tok-1").toHttpUrl(), s.url)
        assertEquals("tok-1", src.token.value)
        assertEquals(listOf("info", "tune c1", "tune c1", "tune c1"), home.calls)
        assertEquals(6_000L, currentTime - started) // two 202s, 3 s apart
    }

    @Test
    fun aHeartbeatsNewTokenSwapsWithoutReopeningThePlayer() = runTest {
        val home = FakeHome()
        home.beats += { RemoteBeat(state = "live", token = "tok-2", hlsUrl = cf("tok-2")) }
        home.beats += { RemoteBeat(state = "live") }
        val src = source(home)
        val opened = mutableListOf<LiveStream?>()
        backgroundScope.launch { src.stream.collect { opened += it } }
        runCurrent()
        val s = src.open("c1")
        runCurrent()

        val segment = "https://customer-abc.cloudflarestream.com/tok-1/segments/7.ts".toHttpUrl()
        assertEquals(segment, src.rewrite(segment))

        advanceTimeBy(59_999)
        assertEquals(0, home.calls.count { it.startsWith("beat") })
        advanceTimeBy(2)
        assertEquals(listOf("beat s-1"), home.calls.filter { it.startsWith("beat") })
        assertEquals("tok-2", src.token.value)
        assertEquals(cf("tok-2").toHttpUrl(), src.latestUrl)
        // the player's media item is the same; only its requests carry the new token
        assertEquals(s, src.stream.value)
        assertEquals(listOf(null, s), opened)
        assertEquals("/tok-2/segments/7.ts", src.rewrite(segment).encodedPath)
        assertEquals("/tok-2/segments/7.ts", throughInterceptor(src, segment).encodedPath)
        // a beat with no token keeps the one there is
        advanceTimeBy(60_000)
        assertEquals(2, home.calls.count { it.startsWith("beat") })
        assertEquals("tok-2", src.token.value)
        // the home's own stream URL is never touched
        assertEquals(lan("c1"), src.rewrite(lan("c1")))
    }

    @Test
    fun aBeatThatFailsIsTriedAgainAtTheNext() = runTest {
        val home = FakeHome()
        home.beats += { throw ApiException.Transport("wifi") }
        home.beats += { RemoteBeat(state = "live", token = "tok-9") }
        val src = source(home)
        src.open("c1")
        advanceTimeBy(60_001)
        assertEquals("tok-1", src.token.value)
        advanceTimeBy(60_000)
        assertEquals("tok-9", src.token.value)
    }

    @Test
    fun aSessionTheHomeEndedStopsTheBeat() = runTest {
        val home = FakeHome()
        home.beats += {
            throw ApiException.Status(410, """{"type":"https://maktaba.dev/problems/session-ended","title":"Gone","status":410,"code":"session_ended"}""")
        }
        var ended: LiveStream.Relay? = null
        val src = source(home) { ended = it }
        val s = src.open("c1")
        advanceTimeBy(60_001)
        assertEquals(s, ended)
        assertNull(src.stream.value)
        assertNull(src.token.value)
        advanceTimeBy(10 * 60_000)
        assertEquals(1, home.calls.count { it.startsWith("beat") })
    }

    @Test
    fun anotherChannelEndsTheOldSessionFirst() = runTest {
        val home = FakeHome()
        val src = source(home)
        src.open("c1")
        val s2 = src.open("c2") as LiveStream.Relay
        assertEquals(listOf("info", "tune c1", "end s-1", "info", "tune c2"), home.calls)
        assertEquals("s-2", s2.sessionId)
        assertEquals("tok-2", src.token.value)
        advanceTimeBy(60_001)
        assertEquals(listOf("beat s-2"), home.calls.filter { it.startsWith("beat") })
    }

    @Test
    fun closeEndsTheSessionAndTheBeat() = runTest {
        val home = FakeHome()
        val src = source(home)
        src.open("c1")
        src.close()
        assertEquals("end s-1", home.calls.last())
        assertNull(src.stream.value)
        advanceTimeBy(5 * 60_000)
        assertEquals(0, home.calls.count { it.startsWith("beat") })
    }

    @Test
    fun aStreamThatNeverStartsGivesUpAndEndsTheSession() = runTest {
        val home = FakeHome(starting = 1_000)
        val src = source(home)
        try {
            src.open("c1")
            fail("expected a timeout")
        } catch (e: LiveFailure) {
            assertEquals("remote_timeout", e.code)
            assertTrue(e.retryable)
        }
        assertEquals("end s-1", home.calls.last())
        assertEquals(LiveSource.START_TIMEOUT_MS, currentTime)
        assertNull(src.stream.value)
    }

    @Test
    fun awayWithRemoteViewingOffIsRefused() = runTest {
        val home = FakeHome(available = false)
        try {
            source(home).open("c1")
            fail("expected a refusal")
        } catch (e: LiveFailure) {
            assertEquals("remote_unavailable", e.code)
        }
        assertEquals(listOf("info"), home.calls)
    }

    @Test
    fun aRefusedTuneIsTheHomesCode() = runTest {
        val home = object : RemoteTv {
            override suspend fun info() = TvInfo(remote = TvRemote(available = true, viaRelay = true))
            override suspend fun tune(channelId: String, device: String): RemoteTuned =
                throw ApiException.Status(402, """{"type":"https://maktaba.dev/problems/budget-exhausted","title":"Payment Required","status":402,"code":"budget_exhausted"}""")
            override suspend fun heartbeat(sessionId: String) = RemoteBeat()
            override suspend fun end(sessionId: String) {}
        }
        try {
            LiveSource(home, lan, backgroundScope, "Pixel").open("c1")
            fail("expected a refusal")
        } catch (e: LiveFailure) {
            assertEquals("budget_exhausted", e.code)
            assertEquals(false, e.retryable)
        }
    }

    /** The URL a request leaves with after [LiveSource.interceptor], as the player's OkHttp sends it. */
    private fun throughInterceptor(src: LiveSource, url: HttpUrl): HttpUrl {
        var sent: HttpUrl? = null
        val http = OkHttpClient.Builder()
            .addInterceptor(src.interceptor)
            .addInterceptor { chain ->
                sent = chain.request().url
                Response.Builder().request(chain.request()).protocol(Protocol.HTTP_1_1).code(200).message("OK")
                    .body("".toResponseBody(null)).build()
            }
            .build()
        http.newCall(Request.Builder().url(url).build()).execute().close()
        return sent!!
    }
}
