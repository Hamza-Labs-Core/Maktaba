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
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Story 55.07 T03 and T04, and story 13.22 T01 with recording players (the channel's stream in
 * the engine's player): the clock is the test's virtual time.
 */
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
        var beats = 0
        override suspend fun heartbeat(sessionId: String): RemoteBeat {
            beats++
            return RemoteBeat("live", token = beatToken)
        }
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

    @Test
    fun story1322_t01_oneOpenThenTheSameHandleLoadsTheNextChannelAndTheLiveEdgeAgain() = runTest {
        val players = RecordingPlayers()
        val (cp, video) = channelOnFakes(players)
        cp.open("c1")
        runCurrent()
        val first = video.video
        cp.next(twoChannels)
        runCurrent()
        assertEquals("c2", cp.channelId)
        assertSame("the same handle", first, video.video)
        cp.pause()
        cp.backToLive()
        runCurrent()
        cp.close()
        assertEquals(listOf("open ${channelUrl("c1")}", "loadfile ${channelUrl("c2")} replace", "pause", "loadfile ${channelUrl("c2")} replace", "dispose 1"), players.calls)
        assertEquals(3, cp.opened)
    }

    @Test
    fun story1322_theStreamsEndIsTheChannelEndedAndAFailureOffersTryAgain() = runTest {
        val players = RecordingPlayers()
        val (cp, _) = channelOnFakes(players)
        cp.open("c1")
        runCurrent()
        assertEquals(ChannelStatus.Playing, cp.status)
        players.events.single().ended()
        runCurrent()
        assertEquals(ChannelStatus.Ended, cp.status)
        assertEquals("live.channelEnded", cp.endedKey)
        assertTrue(players.calls.toString(), "dispose 1" in players.calls)
        // Try again is not offered for an end; a failure offers it, and it opens the channel again
        cp.retry()
        runCurrent()
        assertEquals(ChannelStatus.Playing, cp.status)
        players.events.last().failed("loading failed")
        runCurrent()
        assertEquals(ChannelStatus.Failed(null, retryable = true), cp.status)
        cp.retry()
        runCurrent()
        assertEquals(ChannelStatus.Playing, cp.status)
        assertEquals(3, players.events.size)
        // an older player's word counts for nothing
        players.events.first().failed("late")
        runCurrent()
        assertEquals(ChannelStatus.Playing, cp.status)
    }

    @Test
    fun story1322_backToLiveDoesNothingBehindEndedOrTryAgainAndAStopUnpauses() = runTest {
        val players = RecordingPlayers()
        val (cp, _) = channelOnFakes(players)
        cp.open("c1")
        runCurrent()
        cp.pause()
        assertTrue(cp.paused)
        players.events.single().ended()
        runCurrent()
        assertEquals(ChannelStatus.Ended, cp.status)
        assertFalse("a stopped channel is not paused", cp.paused)
        cp.backToLive()
        runCurrent()
        assertEquals(ChannelStatus.Ended, cp.status)
        cp.retry()
        runCurrent()
        players.events.last().failed("loading failed")
        runCurrent()
        cp.backToLive()
        runCurrent()
        assertEquals(ChannelStatus.Failed(null, retryable = true), cp.status)
        // two streams given (the open and Try again's), none by Back to live
        assertEquals(listOf("open ${channelUrl("c1")}", "pause", "dispose 1", "open ${channelUrl("c1")}", "dispose 2"), players.calls)
        assertEquals(2, cp.opened)
    }

    @Test
    fun story1322_pausedWhileTuningTheChannelOpensPaused() = runTest {
        val players = RecordingPlayers()
        val (cp, _) = channelOnFakes(players, Away(starting = 1))
        cp.open("c1")
        runCurrent()
        assertEquals(ChannelStatus.Tuning, cp.status)
        cp.pause()
        assertTrue(cp.paused)
        advanceTimeBy(2_001)
        runCurrent()
        assertEquals(ChannelStatus.Playing, cp.status)
        assertTrue(cp.paused)
        // the player plays what it opens: paused again, as the screen says
        assertEquals(listOf("open ${cf("T1")}", "pause"), players.calls)
        cp.resume()
        assertFalse(cp.paused)
        assertEquals("play", players.calls.last())
        // Pause pressed as Back to live loads: the same
        cp.pause()
        cp.backToLive()
        cp.pause()
        runCurrent()
        assertTrue(cp.paused)
        assertEquals(listOf("loadfile ${cf("T1")} replace", "pause"), players.calls.takeLast(2))
    }

    @Test
    fun story1322_thereIsNothingToPauseBehindANotice() = runTest {
        val players = RecordingPlayers()
        val (cp, _) = channelOnFakes(players)
        cp.open("c1")
        runCurrent()
        players.events.single().failed("loading failed")
        runCurrent()
        cp.pause()
        assertFalse(cp.paused)
    }

    @Test
    fun story1322_aStreamThatEndsOrFailsEndsTheRemoteSession() = runTest {
        for (stop in listOf<(com.hamzalabs.maktaba.desktop.VideoEvents) -> Unit>({ it.ended() }, { it.failed("loading failed") })) {
            val players = RecordingPlayers()
            val remote = Away()
            val (cp, _) = channelOnFakes(players, remote)
            cp.open("c1")
            runCurrent()
            assertEquals(listOf("tune c1"), remote.calls)
            stop(players.events.single())
            runCurrent()
            assertEquals(listOf("tune c1", "end s1"), remote.calls)
            // it beats no more, and Try again tunes afresh
            advanceTimeBy(LiveSource.HEARTBEAT_MS * 2)
            runCurrent()
            assertEquals(0, remote.beats)
            cp.retry()
            runCurrent()
            assertEquals(ChannelStatus.Playing, cp.status)
            assertEquals(listOf("tune c1", "end s1", "tune c1"), remote.calls)
        }
    }

    @Test
    fun story1322_aStreamThatEndsOrFailsIsNotAskedAboutAgain() = runTest {
        for (stop in listOf<(com.hamzalabs.maktaba.desktop.VideoEvents) -> Unit>({ it.ended() }, { it.failed("loading failed") })) {
            val players = RecordingPlayers()
            var asked = 0
            val (cp, _) = channelOnFakes(players, gate = { id ->
                asked++
                val until = java.time.Instant.ofEpochMilli(testScheduler.currentTime + 60_000)
                if (asked == 1) com.hamzalabs.maktaba.core.api.ChannelNow.Playing(channelUrl(id).toHttpUrl(), until)
                else com.hamzalabs.maktaba.core.api.ChannelNow.NotNow(null)
            })
            cp.open("c1")
            runCurrent()
            assertEquals(ChannelStatus.Playing, cp.status)
            stop(players.events.single())
            runCurrent()
            val said = cp.status
            advanceTimeBy(300_000)
            runCurrent()
            assertEquals("the notice stays", said, cp.status)
            assertEquals("the home was asked again", 1, asked)
        }
    }
}
