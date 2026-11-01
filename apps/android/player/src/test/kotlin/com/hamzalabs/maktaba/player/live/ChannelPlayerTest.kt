package com.hamzalabs.maktaba.player.live

import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.LiveChannel
import com.hamzalabs.maktaba.core.api.LiveFailure
import com.hamzalabs.maktaba.core.api.LiveSource
import com.hamzalabs.maktaba.core.api.LiveStream
import com.hamzalabs.maktaba.core.api.RemoteBeat
import com.hamzalabs.maktaba.core.api.RemoteTuned
import com.hamzalabs.maktaba.core.api.RemoteTv
import com.hamzalabs.maktaba.core.api.TvInfo
import com.hamzalabs.maktaba.core.api.TvRemote
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.OkHttpClient
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The live player's logic (FR-23) on a fake clock: where a channel plays from, the remote
 * session's tune, heartbeat and end, the LAN retries while the encoder warms, channel steps and
 * "Back to live".
 */
@OptIn(ExperimentalCoroutinesApi::class)
class ChannelPlayerTest {
    /** The remote session's calls, recorded; away from home when [viaRelay]. */
    private class FakeRemote(var viaRelay: Boolean = false) : RemoteTv {
        val tuned = mutableListOf<String>()
        val beats = mutableListOf<String>()
        val ended = mutableListOf<String>()
        var infos = 0
        var refuse: Exception? = null
        private var n = 0

        override suspend fun info() = TvInfo(remote = TvRemote(available = true, viaRelay = viaRelay)).also { infos++ }

        override suspend fun tune(channelId: String, device: String): RemoteTuned {
            refuse?.let { throw it }
            tuned += channelId
            val s = "s-${++n}"
            return RemoteTuned(s, "live", "https://customer-abc.cloudflarestream.com/tok-$s/manifest/video.m3u8")
        }

        override suspend fun heartbeat(sessionId: String): RemoteBeat {
            beats += sessionId
            return RemoteBeat("live")
        }

        override suspend fun end(sessionId: String) {
            ended += sessionId
        }
    }

    private val lineup = listOf(
        LiveChannel("c1", 1, "Quran"),
        LiveChannel("c2", 2, "Seerah"),
        LiveChannel("c10", 10, "Nasheed"),
    )

    private fun lan(id: String) = "http://home.test/stream/channel/$id/index.m3u8".toHttpUrl()

    private fun TestScope.player(remote: FakeRemote, engine: FakeLiveEngine, waited: MutableList<String> = mutableListOf()) = ChannelPlayer(
        remote = remote,
        lanUrl = ::lan,
        scope = backgroundScope,
        engine = engine,
        device = "test",
        waitPlayable = { waited += it.toString() },
        lanHttp = { OkHttpClient() },
        relayHttp = { OkHttpClient() },
        background = this,
    )

    @Test
    fun atHomeItPlaysTheWebPortsHlsOnceThePlaylistAnswers() = runTest {
        val remote = FakeRemote()
        val engine = FakeLiveEngine(fetch = false)
        val waited = mutableListOf<String>()
        val p = player(remote, engine, waited)
        p.open("c1")
        runCurrent()
        assertEquals(listOf(lan("c1").toString()), waited)
        assertEquals(listOf(lan("c1")), engine.played)
        assertTrue(p.stream is LiveStream.Lan)
        assertFalse(p.tuning)
        assertTrue("no remote session at home", remote.tuned.isEmpty())
        assertEquals(1, remote.infos)
    }

    @Test
    fun aLanStartThatFailsIsAskedAgainThreeTimesThenSaysWhy() = runTest {
        val engine = FakeLiveEngine(fetch = false).apply { failWith = IllegalStateException("404") }
        val p = player(FakeRemote(), engine)
        p.open("c1")
        runCurrent()
        assertEquals(1, engine.played.size)
        advanceTimeBy(ChannelPlayer.RETRY_MS * 3 + 1)
        runCurrent()
        assertEquals(
            listOf(null, "1", "2", "3"),
            engine.played.map { it.queryParameter("retry") },
        )
        assertTrue(p.problem is IllegalStateException)
    }

    @Test
    fun awayItTunesBeatsEveryMinuteAndEndsTheSessionOnLeave() = runTest {
        val remote = FakeRemote(viaRelay = true)
        val engine = FakeLiveEngine(fetch = false)
        val p = player(remote, engine)
        p.open("c1")
        runCurrent()
        assertEquals(listOf("c1"), remote.tuned)
        assertTrue(engine.played.single().host.endsWith(".cloudflarestream.com"))
        assertEquals("s-1", (p.stream as LiveStream.Relay).sessionId)

        advanceTimeBy(LiveSource.HEARTBEAT_MS - 1)
        runCurrent()
        assertTrue("not before a minute", remote.beats.isEmpty())
        advanceTimeBy(2)
        runCurrent()
        assertEquals(listOf("s-1"), remote.beats)
        advanceTimeBy(LiveSource.HEARTBEAT_MS)
        runCurrent()
        assertEquals(listOf("s-1", "s-1"), remote.beats)

        p.leave()
        runCurrent()
        assertEquals(listOf("s-1"), remote.ended)
        assertEquals(1, engine.releases)
        advanceTimeBy(LiveSource.HEARTBEAT_MS * 3)
        runCurrent()
        assertEquals("no beat after leaving", 2, remote.beats.size)
    }

    @Test
    fun anotherChannelEndsTheSessionBeforeTuningTheNext() = runTest {
        val remote = FakeRemote(viaRelay = true)
        val p = player(remote, FakeLiveEngine(fetch = false))
        p.open("c1")
        runCurrent()
        p.next(lineup)
        runCurrent()
        assertEquals(listOf("c1", "c2"), remote.tuned)
        assertEquals(listOf("s-1"), remote.ended)
        assertEquals("c2", p.channelId)
    }

    @Test
    fun aRefusedTuneSaysItsCode() = runTest {
        val remote = FakeRemote(viaRelay = true).apply { refuse = ApiException.Status(429, "{}") }
        val p = player(remote, FakeLiveEngine(fetch = false))
        p.open("c1")
        runCurrent()
        assertEquals("user_streams_full", (p.problem as LiveFailure).code)
        p.retry()
        assertNull("trying again clears it", p.problem)
    }

    @Test
    fun nextAndPreviousWalkTheLineupByNumberAndWrap() = runTest {
        val engine = FakeLiveEngine(fetch = false)
        val p = player(FakeRemote(), engine)
        p.open("c2")
        p.next(lineup)
        assertEquals("c10", p.channelId)
        p.next(lineup)
        assertEquals("c1", p.channelId)
        p.previous(lineup)
        assertEquals("c10", p.channelId)
        assertTrue(p.tune(2, lineup))
        assertEquals("c2", p.channelId)
        assertFalse("no channel 7", p.tune(7, lineup))
        assertEquals("c2", p.channelId)
        runCurrent()
    }

    @Test
    fun afterAPauseBackToLiveReturnsToTheEdge() = runTest {
        val engine = FakeLiveEngine(fetch = false)
        val p = player(FakeRemote(), engine)
        p.open("c1")
        runCurrent()
        assertFalse(engine.behindLive)
        p.togglePause()
        assertTrue(engine.paused)
        assertTrue(engine.behindLive)
        p.togglePause()
        assertFalse(engine.paused)
        assertTrue("resumed, still behind the edge", engine.behindLive)
        p.backToLive()
        assertFalse(engine.behindLive)
        assertEquals(1, engine.goLives)
    }

    @Test
    fun neighbourIsNullOnlyForAnEmptyLineup() {
        assertNull(neighbour(emptyList(), "c1", 1))
        assertEquals("c1", neighbour(lineup, "unknown", 1)?.id)
        assertEquals("c10", neighbour(lineup.reversed(), "c1", -1)?.id)
    }
}
