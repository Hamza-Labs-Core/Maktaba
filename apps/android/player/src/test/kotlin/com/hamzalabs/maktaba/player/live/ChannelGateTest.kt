package com.hamzalabs.maktaba.player.live

import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.ChannelNow
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
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.Instant

/**
 * Epic 57 story 57.10 on the phone Player: a sign-in under a parental limit asks the home to
 * play a channel. A programme above the limit is "not available right now" until the home's
 * time, then asked again; a playing channel stops when the next programme is above the limit.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class ChannelGateTest {
    private object AtHome : RemoteTv {
        override suspend fun info() = TvInfo(remote = TvRemote(available = true, viaRelay = false))
        override suspend fun tune(channelId: String, device: String): RemoteTuned = error("at home nothing is tuned")
        override suspend fun heartbeat(sessionId: String) = RemoteBeat("live")
        override suspend fun end(sessionId: String) {}
    }

    private val t0 = Instant.parse("2026-11-11T20:00:00Z")
    private fun lan(id: String) = "http://home.test/stream/channel/$id/index.m3u8".toHttpUrl()
    private fun signed(id: String) = "http://home.test/stream/channel/$id/index.m3u8?sig=abc".toHttpUrl()

    private fun TestScope.player(engine: FakeLiveEngine, asked: MutableList<String>, limited: Boolean = true, answer: (Int) -> ChannelNow) = ChannelPlayer(
        remote = AtHome,
        lanUrl = ::lan,
        scope = backgroundScope,
        engine = engine,
        device = "test",
        waitPlayable = {},
        lanHttp = { OkHttpClient() },
        relayHttp = { OkHttpClient() },
        background = this,
        limited = { limited },
        gate = { id ->
            asked += id
            answer(asked.size)
        },
        nowMs = { t0.toEpochMilli() + testScheduler.currentTime },
    )

    @Test
    fun anAccountWithNoLimitIsNotAsked() = runTest {
        val engine = FakeLiveEngine(fetch = false)
        val asked = mutableListOf<String>()
        val p = player(engine, asked, limited = false) { error("not asked") }
        p.open("c1")
        runCurrent()
        assertEquals(listOf(lan("c1")), engine.played)
        assertTrue(asked.isEmpty())
        assertNull(p.notNow)
    }

    @Test
    fun aLimitedAccountPlaysTheSignedAddress() = runTest {
        val engine = FakeLiveEngine(fetch = false)
        val asked = mutableListOf<String>()
        val p = player(engine, asked) { ChannelNow.Playing(signed("c1"), until = null) }
        p.open("c1")
        runCurrent()
        assertEquals(listOf(signed("c1")), engine.played)
        assertFalse(p.tuning)
        assertNull(p.notNow)
        advanceTimeBy(3_600_000)
        assertEquals("nothing is due to change: not asked again", 1, asked.size)
    }

    @Test
    fun aProgrammeAboveTheLimitIsNotNowUntilTheHomesTime() = runTest {
        val back = t0.plusSeconds(600)
        val engine = FakeLiveEngine(fetch = false)
        val asked = mutableListOf<String>()
        val p = player(engine, asked) { n -> if (n == 1) ChannelNow.NotNow(back) else ChannelNow.Playing(signed("c1"), null) }
        p.open("c1")
        runCurrent()
        assertEquals(NotNow(back), p.notNow)
        assertTrue("nothing plays", engine.played.isEmpty())
        assertNull("it is not an error", p.problem)
        assertFalse(p.tuning)
        advanceTimeBy(599_000)
        runCurrent()
        assertEquals("not before the home's time", 1, asked.size)
        advanceTimeBy(2_001)
        runCurrent()
        assertEquals(2, asked.size)
        assertNull(p.notNow)
        assertEquals(listOf(signed("c1")), engine.played)
    }

    @Test
    fun withNoTimeNamedARefusalIsAskedAboutEveryMinute() = runTest {
        val engine = FakeLiveEngine(fetch = false)
        val asked = mutableListOf<String>()
        val p = player(engine, asked) { ChannelNow.NotNow(null) }
        p.open("c1")
        runCurrent()
        assertEquals(NotNow(null), p.notNow)
        advanceTimeBy(60_001)
        runCurrent()
        assertEquals(2, asked.size)
        assertNotNull(p.notNow)
    }

    @Test
    fun aPlayingChannelStopsWhenTheNextProgrammeIsAboveTheLimit() = runTest {
        val change = t0.plusSeconds(300)
        val engine = FakeLiveEngine(fetch = false)
        val asked = mutableListOf<String>()
        val p = player(engine, asked) { n -> if (n == 1) ChannelNow.Playing(signed("c1"), change) else ChannelNow.NotNow(change.plusSeconds(1800)) }
        p.open("c1")
        runCurrent()
        assertNull(p.notNow)
        advanceTimeBy(301_001)
        runCurrent()
        assertEquals(NotNow(change.plusSeconds(1800)), p.notNow)
        assertNull(p.stream)
    }

    @Test
    fun aStreamThatKeepsFailingIsAskedForOnceMoreAndThenShowsItsError() = runTest {
        // the encoder is down while the home still answers that the channel plays
        val engine = FakeLiveEngine(fetch = false)
        val broken = IllegalStateException("the stream does not start")
        engine.failWith = broken
        val asked = mutableListOf<String>()
        val p = player(engine, asked) { ChannelNow.Playing(signed("c1"), until = null) }
        p.open("c1")
        // long enough for every warm-up retry of both opens, and far more
        advanceTimeBy(10 * 60_000)
        runCurrent()
        assertEquals("the home's gate: once to open, once more after the failure", 2, asked.size)
        assertEquals("the error shows, as for an account with no limit", broken, p.problem)
        assertNull(p.stream)
        assertEquals("each open's start and its warm-up retries, then nothing", 2 * (ChannelPlayer.RETRIES + 1), engine.played.size)
    }

    @Test
    fun tryAgainAfterTheErrorAsksTheHomeAgain() = runTest {
        val engine = FakeLiveEngine(fetch = false)
        engine.failWith = IllegalStateException("the stream does not start")
        val asked = mutableListOf<String>()
        val p = player(engine, asked) { ChannelNow.Playing(signed("c1"), until = null) }
        p.open("c1")
        advanceTimeBy(60_000)
        runCurrent()
        assertNotNull(p.problem)
        // the stream is back: the person's Try again plays it
        engine.failWith = null
        p.retry()
        runCurrent()
        assertNull(p.problem)
        assertEquals(3, asked.size)
        assertNotNull(p.stream)
    }

    @Test
    fun changingChannelLeavesTheSlate() = runTest {
        val engine = FakeLiveEngine(fetch = false)
        val asked = mutableListOf<String>()
        val p = player(engine, asked) { n -> if (n == 1) ChannelNow.NotNow(null) else ChannelNow.Playing(signed("c2"), null) }
        p.open("c1")
        runCurrent()
        assertNotNull(p.notNow)
        p.open("c2")
        runCurrent()
        assertNull(p.notNow)
        assertEquals(listOf(signed("c2")), engine.played)
        // the first channel's minute passes: it is not asked about again
        advanceTimeBy(120_000)
        runCurrent()
        assertEquals(listOf("c1", "c2"), asked)
    }

    @Test
    fun aHiddenChannelIsAMissingOne() = runTest {
        val engine = FakeLiveEngine(fetch = false)
        val p = player(engine, mutableListOf()) { throw ApiException.Status(404, """{"title":"not found","status":404}""") }
        p.open("c9")
        runCurrent()
        assertNull(p.notNow)
        assertTrue(p.problem is ApiException.Status)
    }
}
