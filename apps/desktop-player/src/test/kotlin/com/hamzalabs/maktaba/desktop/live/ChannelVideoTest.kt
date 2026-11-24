package com.hamzalabs.maktaba.desktop.live

import com.hamzalabs.maktaba.core.api.LiveSource
import com.hamzalabs.maktaba.core.api.RemoteTv
import com.hamzalabs.maktaba.core.api.TvInfo
import com.hamzalabs.maktaba.core.api.LiveChannel
import com.hamzalabs.maktaba.desktop.FxPlayers
import com.hamzalabs.maktaba.desktop.Players
import com.hamzalabs.maktaba.desktop.Video
import com.hamzalabs.maktaba.desktop.VideoEvents
import com.hamzalabs.maktaba.desktop.mpv.Engine
import com.hamzalabs.maktaba.desktop.mpv.MpvLoad
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.currentTime
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import okhttp3.HttpUrl.Companion.toHttpUrl
import org.junit.Assert.assertEquals
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder

/**
 * Story 13.22's [ChannelVideo] with fake players: a player that cannot load into itself (JavaFX's)
 * is replaced, a stall is a failure, and with the older engine chosen channels play with JavaFX
 * (T05). T01 and the stream's end and failure are in [ChannelPlaybackTest].
 */
@OptIn(ExperimentalCoroutinesApi::class)
class ChannelVideoTest {
    @get:Rule val tmp = TemporaryFolder()

    @Test
    fun aPlayerThatCannotLoadIsReplacedAsJavaFxsWas() = runTest {
        val players = RecordingPlayers(reuse = false)
        val (cp, _) = channelOnFakes(players)
        cp.open("c1")
        runCurrent()
        cp.next(twoChannels)
        runCurrent()
        cp.pause()
        cp.resume()
        assertEquals(listOf("open ${channelUrl("c1")}", "open ${channelUrl("c2")}", "dispose 1", "pause", "play"), players.calls)
    }

    @Test
    fun aStreamThatStopsMovingForFifteenSecondsHasFailed() = runTest {
        val players = RecordingPlayers()
        val (cp, _) = channelOnFakes(players)
        cp.open("c1")
        runCurrent()
        // not started yet: the home's own answer says whether it fails
        advanceTimeBy(ChannelVideo.STALL_MS * 2)
        assertEquals(ChannelStatus.Playing, cp.status)
        val events = players.events.single()
        events.position(0)
        advanceTimeBy(10_000)
        events.position(10_000)
        // paused: no progress is expected
        cp.pause()
        advanceTimeBy(ChannelVideo.STALL_MS + 2_000)
        assertEquals(ChannelStatus.Playing, cp.status)
        cp.resume()
        advanceTimeBy(ChannelVideo.STALL_MS - 2_000)
        assertEquals(ChannelStatus.Playing, cp.status)
        advanceTimeBy(4_000)
        runCurrent()
        assertEquals(ChannelStatus.Failed(null, retryable = true), cp.status)
    }

    @Test
    fun t05_withTheOlderEngineChosenChannelsPlayWithJavaFx() {
        tmp.newFile(MpvLoad.fileName()).writeText("not opened: JavaFX is chosen first")
        val chosen = MpvLoad.from(tmp.root.absolutePath, mapOf("MAKTABA_PLAYER_ENGINE" to "javafx"))
        assertSame(FxPlayers, Engine.playersFor(chosen) { null })
        assertSame(FxPlayers, Engine.playersFor(MpvLoad.Missing("libmpv-2.dll is not in C:\\Maktaba")) { null })
    }
}

/** Players recording every call, as mpv's commands; [reuse] false: a player that cannot load (JavaFX's). */
internal class RecordingPlayers(private val reuse: Boolean = true) : Players {
    val calls: MutableList<String> = java.util.Collections.synchronizedList(mutableListOf())
    val events: MutableList<VideoEvents> = java.util.Collections.synchronizedList(mutableListOf())
    override suspend fun open(url: String, fromMs: Long, autoplay: Boolean, events: VideoEvents): Video = error("a channel opens live")
    override suspend fun openLive(url: String, events: VideoEvents): Video {
        calls += "open $url"
        this.events += events
        val n = this.events.size
        return object : Video {
            override fun toggle() {}
            override fun pause() {
                calls += "pause"
            }
            override fun play() {
                calls += "play"
            }
            override fun seek(ms: Long) {}
            override fun sound(volume: Float, muted: Boolean) {}
            override fun dispose() {
                calls += "dispose $n"
            }
            override fun load(url: String): Boolean {
                if (!reuse) return false
                calls += "loadfile $url replace"
                return true
            }
        }
    }
}

/** A home's channel at home, as the web port serves it. */
internal fun channelUrl(id: String) = "http://home.lan/stream/channel/$id/index.m3u8"

internal val twoChannels = listOf(LiveChannel("c1", 1, "One"), LiveChannel("c2", 2, "Two"))

/** A channel's playback at home, its stream in [players]' video, wired as the channel screen does. */
@OptIn(ExperimentalCoroutinesApi::class)
internal fun TestScope.channelOnFakes(players: Players): Pair<ChannelPlayback, ChannelVideo> {
    val atHome = object : RemoteTv {
        override suspend fun info() = TvInfo()
        override suspend fun tune(channelId: String, device: String) = error("no tune at home")
        override suspend fun heartbeat(sessionId: String) = error("no beat at home")
        override suspend fun end(sessionId: String) {}
    }
    var cp: ChannelPlayback? = null
    val video = ChannelVideo(players, backgroundScope, onEnded = { cp?.streamEnded() }, onFailed = { cp?.streamFailed() }, nowMs = { currentTime })
    val source = LiveSource(atHome, { channelUrl(it).toHttpUrl() }, backgroundScope, "test")
    val playback = ChannelPlayback(
        source, backgroundScope, local = { it.url.toString() },
        show = video::show, hide = video::hide, pauseVideo = video::pause, playVideo = video::play,
    ).also { cp = it }
    return playback to video
}
