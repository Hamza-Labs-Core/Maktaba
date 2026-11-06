package com.hamzalabs.maktaba.desktop.live

import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.LiveSource
import com.hamzalabs.maktaba.core.api.LiveStream
import com.hamzalabs.maktaba.core.api.RemoteBeat
import com.hamzalabs.maktaba.core.api.RemoteTuned
import com.hamzalabs.maktaba.core.api.RemoteTv
import com.hamzalabs.maktaba.core.api.TvInfo
import com.hamzalabs.maktaba.core.api.TvRemote
import com.hamzalabs.maktaba.core.api.rewriteToken
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import okhttp3.HttpUrl.Companion.toHttpUrl
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/** Story 55.07 T03 and T04: the clock is the test's virtual time. */
@OptIn(ExperimentalCoroutinesApi::class)
class ChannelPlaybackTest {
    private fun cf(token: String) = "https://customer-abc.cloudflarestream.com/$token/manifest/video.m3u8"

    /** Away from home: [starting] 202s, then live; each heartbeat brings token [beatToken]; or the tune answers [refuse]. */
    private class Away(var starting: Int = 0, var beatToken: String? = null, var refuse: Int? = null, var problemCode: String = "") : RemoteTv {
        val calls = mutableListOf<String>()
        override suspend fun info() = TvInfo(remote = TvRemote(available = true, viaRelay = true))
        override suspend fun tune(channelId: String, device: String): RemoteTuned {
            calls += "tune $channelId"
            refuse?.let { throw ApiException.Status(it, """{"title":"no","status":$it,"code":"$problemCode"}""") }
            if (starting-- > 0) return RemoteTuned("s1", "starting", retryAfter = 2)
            return RemoteTuned("s1", "live", hlsUrl = "https://customer-abc.cloudflarestream.com/T1/manifest/video.m3u8")
        }
        override suspend fun heartbeat(sessionId: String) = RemoteBeat("live", token = beatToken)
        override suspend fun end(sessionId: String) {
            calls += "end $sessionId"
        }
    }

    private fun TestScope.playback(remote: RemoteTv, shown: MutableList<String>): Pair<ChannelPlayback, LiveSource> {
        val source = LiveSource(remote, { "http://home.lan/stream/channel/$it/index.m3u8".toHttpUrl() }, backgroundScope, "test")
        val cp = ChannelPlayback(source, backgroundScope, local = { s -> s.url.toString() }, show = { shown += it })
        return cp to source
    }

    @Test
    fun startingThenLiveOpensOnceAndANewTokenNeverReopens() = runTest {
        val remote = Away(starting = 1, beatToken = "T2")
        val shown = mutableListOf<String>()
        val (cp, source) = playback(remote, shown)
        cp.open("c1")
        runCurrent()
        assertEquals(ChannelStatus.Tuning, cp.status)
        advanceTimeBy(2_001)
        runCurrent()
        assertEquals(ChannelStatus.Playing, cp.status)
        assertEquals(listOf(cf("T1")), shown)
        // the heartbeat brings a new token: the player is not reopened, the proxy writes it in
        advanceTimeBy(LiveSource.HEARTBEAT_MS + 1)
        runCurrent()
        assertEquals("T2", source.token.value)
        assertEquals(1, cp.opened)
        assertEquals(cf("T2"), rewriteToken(cf("T1").toHttpUrl(), source.token.value!!).toString())
        // leaving ends the session on the home
        cp.close()
        assertTrue(remote.calls.toString(), "end s1" in remote.calls)
    }

    @Test
    fun eachRefusalHasItsSentenceAndRetryOnlyWhenRetryable() = runTest {
        for ((status, code, key) in listOf(
            Triple(429, "user_streams_full", "live.streamsFull"),
            Triple(402, "budget_exhausted", "live.budget"),
            Triple(403, "remote_not_allowed", "live.notAllowed"),
            Triple(404, "no_such_channel", "live.noSuchChannel"),
            Triple(503, "remote_unavailable", "live.remoteUnavailable"),
        )) {
            val (cp, _) = playback(Away(refuse = status, problemCode = code), mutableListOf())
            cp.open("c1")
            runCurrent()
            val s = cp.status as ChannelStatus.Failed
            assertEquals(code, key, s.key)
            assertFalse(code, s.retryable)
        }
        val (cp, _) = playback(Away(refuse = 500), mutableListOf())
        cp.open("c1")
        runCurrent()
        assertTrue("a 5xx can be tried again", (cp.status as ChannelStatus.Failed).retryable)
        assertEquals("live.remoteTimeout", liveFailureKey("remote_timeout"))
    }

    @Test
    fun liveTvTurnedOffSaysItIsNotAvailable() = runTest {
        val (cp, _) = playback(Away(refuse = 409, problemCode = "component_off"), mutableListOf())
        cp.open("c1")
        runCurrent()
        assertEquals(ChannelStatus.Off, cp.status)
    }

    @Test
    fun atHomeItPlaysTheWebPortsStream() = runTest {
        val home = object : RemoteTv {
            override suspend fun info() = TvInfo()
            override suspend fun tune(channelId: String, device: String) = error("no tune at home")
            override suspend fun heartbeat(sessionId: String) = error("no beat at home")
            override suspend fun end(sessionId: String) {}
        }
        val shown = mutableListOf<String>()
        val (cp, source) = playback(home, shown)
        cp.open("c7")
        runCurrent()
        assertEquals(listOf("http://home.lan/stream/channel/c7/index.m3u8"), shown)
        assertTrue(source.stream.value is LiveStream.Lan)
        cp.pause()
        cp.backToLive()
        runCurrent()
        assertEquals(2, cp.opened)
        assertFalse(cp.paused)
    }
}
