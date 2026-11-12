package com.hamzalabs.maktaba.desktop.live

import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.ChannelNow
import com.hamzalabs.maktaba.core.api.LiveSource
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
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.Instant

/**
 * Epic 57 story 57.10 on the desktop Player: a sign-in under a parental limit asks the home to
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
    private fun signed(id: String) = "http://home.lan/stream/channel/$id/index.m3u8?sig=abc".toHttpUrl()

    private class Rig(val cp: ChannelPlayback, val shown: MutableList<String>, val asked: MutableList<String>, var hidden: Int = 0)

    private fun TestScope.rig(limited: Boolean = true, clockAheadMs: Long = 0, answer: (Int) -> ChannelNow): Rig {
        val shown = mutableListOf<String>()
        val asked = mutableListOf<String>()
        lateinit var rig: Rig
        val source = LiveSource(AtHome, { "http://home.lan/stream/channel/$it/index.m3u8".toHttpUrl() }, backgroundScope, "test")
        val cp = ChannelPlayback(
            source, backgroundScope,
            local = { s -> s.url.toString() },
            show = { shown += it },
            hide = { rig.hidden++ },
            limited = { limited },
            gate = { id ->
                asked += id
                answer(asked.size)
            },
            nowMs = { t0.toEpochMilli() + clockAheadMs + testScheduler.currentTime },
        )
        rig = Rig(cp, shown, asked)
        return rig
    }

    @Test
    fun anAccountWithNoLimitIsNotAsked() = runTest {
        val r = rig(limited = false) { error("not asked") }
        r.cp.open("c1")
        runCurrent()
        assertEquals(ChannelStatus.Playing, r.cp.status)
        assertEquals(listOf("http://home.lan/stream/channel/c1/index.m3u8"), r.shown)
        assertTrue(r.asked.isEmpty())
    }

    @Test
    fun aLimitedAccountPlaysTheSignedAddress() = runTest {
        val r = rig { ChannelNow.Playing(signed("c1"), until = null) }
        r.cp.open("c1")
        runCurrent()
        assertEquals(ChannelStatus.Playing, r.cp.status)
        assertEquals(listOf(signed("c1").toString()), r.shown)
        // nothing is due to change: the home is not asked again
        advanceTimeBy(3_600_000)
        assertEquals(1, r.asked.size)
    }

    /**
     * The home's answer carries its own time: the wait is counted from it, and the clock the
     * playback was given (a day fast here: by it the home's time passed long ago) is not asked.
     */
    @Test
    fun theWaitIsCountedFromTheHomesOwnTimeInTheAnswer() = runTest {
        val back = t0.plusSeconds(600)
        val r = rig(clockAheadMs = 86_400_000) { n ->
            val clock = com.hamzalabs.maktaba.core.api.HomeClock(t0.toEpochMilli(), com.hamzalabs.maktaba.core.api.monotonicMs())
            if (n == 1) ChannelNow.NotNow(back, clock) else ChannelNow.Playing(signed("c1"), null, clock)
        }
        r.cp.open("c1")
        runCurrent()
        assertEquals(ChannelStatus.NotNow(back), r.cp.status)
        // not every five seconds until the device's clock agrees
        advanceTimeBy(599_000)
        runCurrent()
        assertEquals(1, r.asked.size)
        advanceTimeBy(2_001)
        runCurrent()
        assertEquals(2, r.asked.size)
        assertEquals(ChannelStatus.Playing, r.cp.status)
    }

    @Test
    fun aProgrammeAboveTheLimitIsNotNowUntilTheHomesTime() = runTest {
        val back = t0.plusSeconds(600)
        val r = rig { n -> if (n == 1) ChannelNow.NotNow(back) else ChannelNow.Playing(signed("c1"), null) }
        r.cp.open("c1")
        runCurrent()
        assertEquals(ChannelStatus.NotNow(back), r.cp.status)
        assertTrue("nothing plays", r.shown.isEmpty())
        // not before the home's time
        advanceTimeBy(599_000)
        runCurrent()
        assertEquals(1, r.asked.size)
        advanceTimeBy(2_001)
        runCurrent()
        assertEquals(2, r.asked.size)
        assertEquals(ChannelStatus.Playing, r.cp.status)
        assertEquals(listOf(signed("c1").toString()), r.shown)
    }

    @Test
    fun withNoTimeNamedARefusalIsAskedAboutEveryMinute() = runTest {
        val r = rig { ChannelNow.NotNow(null) }
        r.cp.open("c1")
        runCurrent()
        assertEquals(ChannelStatus.NotNow(null), r.cp.status)
        advanceTimeBy(60_001)
        runCurrent()
        assertEquals(2, r.asked.size)
        assertEquals(ChannelStatus.NotNow(null), r.cp.status)
    }

    @Test
    fun aPlayingChannelStopsWhenTheNextProgrammeIsAboveTheLimit() = runTest {
        val change = t0.plusSeconds(300)
        val r = rig { n -> if (n == 1) ChannelNow.Playing(signed("c1"), change) else ChannelNow.NotNow(change.plusSeconds(1800)) }
        r.cp.open("c1")
        runCurrent()
        assertEquals(ChannelStatus.Playing, r.cp.status)
        advanceTimeBy(301_001)
        runCurrent()
        assertEquals(ChannelStatus.NotNow(change.plusSeconds(1800)), r.cp.status)
        assertEquals("the player was stopped", 1, r.hidden)
    }

    @Test
    fun aRefusalThrownByTheHomeIsNotNowToo() = runTest {
        val r = rig {
            throw ApiException.Status(403, """{"type":"https://maktaba.tv/problems/restricted","title":"not available right now","status":403,"code":"restricted","reason":"programme","retry_at":"2026-11-11T20:30:00Z"}""")
        }
        r.cp.open("c1")
        runCurrent()
        assertEquals(ChannelStatus.NotNow(Instant.parse("2026-11-11T20:30:00Z")), r.cp.status)
    }

    @Test
    fun aHiddenChannelIsAMissingOne() = runTest {
        val r = rig { throw ApiException.Status(404, """{"title":"not found","status":404}""") }
        r.cp.open("c9")
        runCurrent()
        assertEquals(ChannelStatus.Failed(null, retryable = true), r.cp.status)
    }
}
