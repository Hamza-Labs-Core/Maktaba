package com.hamzalabs.maktaba.desktop.live

import androidx.compose.ui.graphics.toPixelMap
import com.hamzalabs.maktaba.core.api.LiveApi
import com.hamzalabs.maktaba.core.api.LiveChannel
import com.hamzalabs.maktaba.desktop.StreamProxy
import com.hamzalabs.maktaba.desktop.AudioOption
import com.hamzalabs.maktaba.desktop.TestHome
import com.hamzalabs.maktaba.desktop.VideoEvents
import com.hamzalabs.maktaba.desktop.mpv.LibMpv
import com.hamzalabs.maktaba.desktop.mpv.MpvLoad
import com.hamzalabs.maktaba.desktop.mpv.MpvPlayers
import com.hamzalabs.maktaba.desktop.mpv.MpvVideo
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.asCoroutineDispatcher
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withContext
import mockwebserver3.MockResponse
import okio.Buffer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Assume.assumeTrue
import org.junit.Test
import java.io.File
import java.util.Collections
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit

/**
 * Story 13.22 against the libmpv the Player carries (CI's Desktop Player job sets
 * MAKTABA_LIBMPV_DIR and MAKTABA_TEST_MEDIA, libmpv/test-media.sh's two live HLS among the
 * files; elsewhere these tests are skipped). The fake home serves channels at home, as the web
 * port does, through the stream proxy: live playlists whose window of six segments moves on by
 * one each second while the test runs, as ffmpeg's hls_list_size makes it. Channel one's
 * picture is red and channel two's blue, so the frame on screen says which channel it is.
 *  - T02: a channel opens on libmpv and its first frame shows within 2 s on Linux (the time is
 *    printed on both systems);
 *  - T03: Next channel loads into the same handle: channel one's last frame stays on screen
 *    until channel two's first, within 2 s; Pause then Back to live loads the stream again from
 *    its live edge;
 *  - channels changed fast (several loads at once, while the first stream starts and while one
 *    plays) end on the last one, with its own picture and moving positions;
 *  - T04: the stream stops moving on, then its playlist ends: the channel ended; a channel
 *    whose playlist answers 404, opened or loaded into the playing handle, failed, with Try
 *    again; a playing stream whose home stops answering failed, with Try again.
 */
class ChannelMpvRealTest {
    private val dir = System.getenv("MAKTABA_LIBMPV_DIR")
    private val media = System.getenv("MAKTABA_TEST_MEDIA")?.let(::File)
    private val home = TestHome()
    private val thread = Executors.newSingleThreadExecutor { r -> Thread(r, "test-main").apply { isDaemon = true } }
    private val main = thread.asCoroutineDispatcher()
    private val scope = CoroutineScope(SupervisorJob() + main)
    private val proxy = StreamProxy(home.client)
    private var channel: ChannelPlayer? = null
    private val lineup = listOf(LiveChannel("c1", 1, "One"), LiveChannel("c2", 2, "Two"))
    private val onAir: MutableList<OnAir> = Collections.synchronizedList(mutableListOf())

    init {
        // mpv's own log of each handle, uploaded with the job's reports when it fails
        System.setProperty(MpvVideo.LOG_DIR_PROPERTY, File("build/reports/mpv-logs").absolutePath)
    }

    private fun lib(): LibMpv {
        assumeTrue("MAKTABA_LIBMPV_DIR is not set", dir != null)
        val l = MpvLoad.from(dir)
        assertTrue("libmpv: $l", l is MpvLoad.Loaded)
        return (l as MpvLoad.Loaded).lib
    }

    /** test-media.sh's live segments in [name] (live: red, live2: blue), in order. */
    private fun segments(name: String): List<File> {
        assumeTrue("MAKTABA_TEST_MEDIA is not set", media != null)
        val full = File(media, "$name/full.m3u8").also { assertTrue("$it is missing", it.isFile) }
        return full.readLines().filter { it.endsWith(".ts") }.map { File(full.parentFile, it.substringAfterLast('/')) }
    }

    /**
     * A channel on air: its window moves on each second until [freeze]; [end] closes its
     * playlist; [hang] holds every answer (a home that stopped answering).
     */
    private class OnAir(val count: Int) {
        private val t0 = System.nanoTime()
        @Volatile private var frozen: Int? = null
        @Volatile private var ended = false
        val hang = CountDownLatch(1)
        @Volatile var hanging = false

        /** The newest segment listed now. */
        fun last(): Int = frozen ?: minOf(count - 1, START - 1 + ((System.nanoTime() - t0) / 1_000_000_000L).toInt())

        fun freeze() {
            frozen = last()
        }

        fun end() {
            freeze()
            ended = true
        }

        fun waitIfHanging() {
            if (hanging) hang.await(60, TimeUnit.SECONDS)
        }

        fun playlist(): String {
            val last = last()
            val first = maxOf(0, last - WINDOW + 1)
            return buildString {
                append("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:1\n#EXT-X-MEDIA-SEQUENCE:$first\n")
                for (i in first..last) append("#EXTINF:1.000000,\nseg%03d.ts\n".format(i))
                if (ended) append("#EXT-X-ENDLIST\n")
            }
        }

        companion object {
            const val WINDOW = 6
            const val START = 6
        }
    }

    /** Which channel's segments were asked for, in order ("c1 12"). */
    private val fetched: MutableList<String> = Collections.synchronizedList(mutableListOf())

    private fun onAir(id: String, files: List<File>): OnAir {
        val air = OnAir(files.size).also { onAir += it }
        home.answer("GET /stream/channel/$id/index.m3u8") {
            air.waitIfHanging()
            MockResponse.Builder().code(200).addHeader("Content-Type", "application/vnd.apple.mpegurl")
                .addHeader("Cache-Control", "no-cache").body(air.playlist()).build()
        }
        files.forEachIndexed { i, f ->
            val bytes = f.readBytes()
            home.answer("GET /stream/channel/$id/${f.name}") {
                air.waitIfHanging()
                fetched += "$id $i"
                MockResponse.Builder().code(200).addHeader("Content-Type", "video/mp2t").body(Buffer().apply { write(bytes) }).build()
            }
        }
        return air
    }

    private fun start(lib: LibMpv): ChannelPlayer {
        home.answer("GET /api/tv/info", "{}")
        val c = channelPlayback(home.home(), LiveTvState(LiveApi(home.client)), scope, proxy, MpvPlayers(lib, null))
        channel = c
        return c
    }

    @After
    fun tearDown() {
        onAir.forEach { it.hang.countDown() }
        channel?.let { c -> runBlocking(main) { c.playback.close() } }
        proxy.close()
        home.close()
        thread.shutdown()
    }

    private fun <T> onMain(block: () -> T): T = runBlocking { withContext(main) { block() } }

    /** Waits up to [limitMs] for [ok], the screen's picture given its size meanwhile; the time it took, in ms. */
    private fun until(c: ChannelPlayer, what: String, limitMs: Long = 20_000, ok: (MpvVideo?) -> Boolean): Long {
        val from = System.nanoTime()
        val end = System.currentTimeMillis() + limitMs
        // what the screen went through meanwhile, with mpv's state at each change
        val trace = mutableListOf<String>()
        var last: Any? = null
        var lastVideo: MpvVideo? = null
        while (System.currentTimeMillis() < end) {
            val v = c.video.video as? MpvVideo
            if (v != null) lastVideo = v
            v?.resize(1280, 720)
            val now = c.playback.status to (v?.shown?.load)
            if (now != last) {
                last = now
                trace += "${(System.nanoTime() - from) / 1_000_000} ms: $now; ${v?.state()}"
            }
            if (ok(v)) return (System.nanoTime() - from) / 1_000_000
            Thread.sleep(5)
        }
        throw AssertionError(
            "never: $what (status ${c.playback.status}; mpv ${lastVideo?.state()}; " +
                "asked ${home.seen.map { it.url.encodedPath }.takeLast(16)}; trace ${trace.takeLast(12)})",
        )
    }

    /** Which channel a frame shows: "red" (one), "blue" (two), or "other". */
    private fun colour(s: MpvVideo.Shown): String {
        val px = s.image.toPixelMap(s.image.width / 2, s.image.height / 2, 1, 1)[0, 0]
        return when {
            px.red > 0.5f && px.blue < 0.35f -> "red"
            px.blue > 0.5f && px.red < 0.35f -> "blue"
            else -> "other(${px.red}, ${px.green}, ${px.blue})"
        }
    }

    private val linux = System.getProperty("os.name").startsWith("Linux")

    @Test
    fun t02_t03_aChannelStartsWithinTwoSecondsAndTheNextLoadsIntoTheSameHandle() {
        val lib = lib()
        onAir("c1", segments("live"))
        val two = onAir("c2", segments("live2"))
        val c = start(lib)
        onMain { c.playback.open("c1") }
        val first = until(c, "the first frame") { it?.shown != null }
        println("13.22 T02 channel's first frame (libmpv, live HLS, ${System.getProperty("os.name")}): $first ms")
        assertEquals(ChannelStatus.Playing, c.playback.status)
        assertEquals("channel one's picture", "red", colour((c.video.video as MpvVideo).shown!!))
        if (linux) assertTrue("first frame after $first ms", first < 2_000)

        // Next channel: the same handle, loaded; channel one's last frame stays on screen until
        // channel two's first, never another picture or none
        val v = c.video.video as MpvVideo
        var held = 0
        onMain { c.playback.next(lineup) }
        val next = until(c, "the next channel's first frame") {
            val s = v.shown!!
            val shows = colour(s)
            if (s.load == 0) {
                assertEquals("before channel two's first frame", "red", shows)
                held++
            } else {
                assertEquals("from channel two's first frame", "blue", shows)
            }
            s.load == 1
        }
        println("13.22 T03 next channel's first frame (libmpv, the same handle): $next ms, channel one's frame held over $held looks")
        assertTrue("channel one's last frame was not held", held > 0)
        assertSame("a new handle", v, c.video.video)
        assertEquals("c2", c.playback.channelId)
        if (linux) assertTrue("next channel's first frame after $next ms", next < 2_000)

        // Pause, then Back to live: the stream loaded again into the handle, from its live edge
        onMain { c.playback.pause() }
        Thread.sleep(4_000)
        val edge = two.last()
        val mark = fetched.size
        onMain { c.playback.backToLive() }
        val back = until(c, "the picture back from the live edge") { it!!.shown!!.load == 2 }
        val after = synchronized(fetched) { fetched.drop(mark) }.map { it.substringAfter(' ').toInt() }
        println("13.22 Back to live: the picture back after $back ms; the live edge was segment $edge, then asked for $after")
        // the playlist's last segments asked for again (a new start at the live edge, not the
        // paused stream's next ones), and nothing from where it was paused
        assertTrue("Back to live asked again for no segment at the live edge: $after", after.any { it in edge - 3..edge })
        assertTrue("Back to live started at segment ${after.minOrNull()}, the live edge being $edge", after.min() >= edge - 4)
        assertEquals("blue", colour(v.shown!!))
        assertSame(v, c.video.video)
        assertEquals(ChannelStatus.Playing, c.playback.status)
    }

    @Test
    fun t03_loadsGivenAtOnceEndOnTheLastWithItsPictureAndPositions() {
        val lib = lib()
        onAir("c1", segments("live"))
        onAir("c2", segments("live2"))
        val api = LiveApi(home.client)
        val red = proxy.local(api.streamUrl("c1"))!!
        val blue = proxy.local(api.streamUrl("c2"))!!
        val positions: MutableList<Long> = Collections.synchronizedList(mutableListOf())
        val failed: MutableList<String> = Collections.synchronizedList(mutableListOf())
        val ends = java.util.concurrent.atomic.AtomicInteger()
        val heard = object : VideoEvents {
            override val volume = 1f
            override val muted = false
            override fun ready(durationMs: Long?, audio: List<AudioOption>) {}
            override fun position(ms: Long) {
                positions += ms
            }
            override fun playing(on: Boolean) {}
            override fun failed(why: String) {
                failed += why
            }
            override fun ended() {
                ends.incrementAndGet()
            }
        }
        val v = runBlocking { MpvPlayers(lib, null).openLive(red, heard) } as MpvVideo

        fun settlesOn(load: Int, colour: String) {
            val end = System.currentTimeMillis() + 20_000
            while (v.shown?.load != load) {
                if (System.currentTimeMillis() > end) throw AssertionError("never: load $load's picture (shown ${v.shown?.load}; failed $failed; ${v.state()})")
                Thread.sleep(5)
            }
            assertEquals("load $load's first frame", colour, colour(v.shown!!))
            // its own positions, moving on
            val mark = positions.size
            while (synchronized(positions) { positions.drop(mark) }.let { p -> p.size < 5 || p.last() <= p.first() }) {
                if (System.currentTimeMillis() > end) throw AssertionError("load $load's positions do not move (${positions.drop(mark)}; ${v.state()})")
                Thread.sleep(20)
            }
            assertEquals(colour, colour(v.shown!!))
            assertEquals(load, v.loads)
            assertEquals("a replaced stream's failure", emptyList<String>(), failed.toList())
            assertEquals("a replaced stream's end", 0, ends.get())
        }
        try {
            v.resize(1280, 720)
            // while the first stream starts: its own loadfile may not have run yet
            v.load(blue)
            v.load(red)
            v.load(blue)
            settlesOn(3, "blue")
            // while one plays
            v.load(red)
            v.load(blue)
            v.load(red)
            settlesOn(6, "red")
            println("13.22 loads given at once end on the last: blue at load 3, red at load 6")
        } finally {
            v.dispose()
        }
    }

    @Test
    fun t03_aChannelChangedWhileItOpensEndsOnTheNextOne() {
        val lib = lib()
        onAir("c1", segments("live"))
        onAir("c2", segments("live2"))
        val c = start(lib)
        onMain { c.playback.open("c1") }
        // the player is made, its first frame not shown: the next channel at once
        until(c, "the player") { it != null && c.playback.status == ChannelStatus.Playing }
        onMain { c.playback.next(lineup) }
        val v = c.video.video as MpvVideo
        val ms = until(c, "the next channel's first frame") { it?.shown?.load == 1 }
        println("13.22 a channel changed while it opened: the next one's first frame after $ms ms")
        assertEquals("c2", c.playback.channelId)
        assertSame("a new handle", v, c.video.video)
        assertEquals("blue", colour(v.shown!!))
        // it plays: its time moves on, and nothing stalls or fails
        val from = v.property("time-pos")?.toDoubleOrNull()
        until(c, "the next channel's time moving") { (it?.property("time-pos")?.toDoubleOrNull() ?: 0.0) > (from ?: 0.0) + 0.5 }
        assertEquals("blue", colour(v.shown!!))
        assertEquals(ChannelStatus.Playing, c.playback.status)
    }

    @Test
    fun t04_aStreamWhosePlaylistEndsIsTheChannelEnded() {
        val lib = lib()
        val air = onAir("c1", segments("live"))
        val c = start(lib)
        onMain { c.playback.open("c1") }
        until(c, "the first frame") { it?.shown != null }
        // the stream stops moving on, then its playlist ends
        air.freeze()
        Thread.sleep(2_000)
        air.end()
        val ms = until(c, "the channel ended") { c.playback.status == ChannelStatus.Ended }
        println("13.22 T04 the channel ended $ms ms after its playlist did")
        assertEquals("live.channelEnded", c.playback.endedKey)
        assertNull(c.video.video)
    }

    @Test
    fun t04_aChannelThatAnswers404FailedAndCanBeTriedAgain() {
        val lib = lib()
        segments("live")
        val c = start(lib)
        // no route for c3's playlist: the fake home answers 404
        onMain { c.playback.open("c3") }
        val ms = until(c, "the channel failed") { c.playback.status is ChannelStatus.Failed }
        println("13.22 T04 a 404 failed after $ms ms")
        assertEquals(ChannelStatus.Failed(null, retryable = true), c.playback.status)
    }

    @Test
    fun t04_aChannelLoadedIntoThePlayingHandleThatAnswers404Failed() {
        val lib = lib()
        onAir("c1", segments("live"))
        val c = start(lib)
        onMain { c.playback.open("c1") }
        until(c, "the first frame") { it?.shown != null }
        val v = c.video.video as MpvVideo
        // channel two has no route: its playlist answers 404, loaded into the same handle
        onMain { c.playback.next(lineup) }
        val ms = until(c, "the next channel failed") { c.playback.status is ChannelStatus.Failed }
        println("13.22 T04 a 404 loaded into the playing handle failed after $ms ms")
        assertEquals(1, v.loads)
        assertEquals(ChannelStatus.Failed(null, retryable = true), c.playback.status)
        assertNull(c.video.video)
    }

    @Test
    fun t04_aPlayingStreamWhoseHomeStopsAnsweringFailed() {
        val lib = lib()
        val air = onAir("c1", segments("live"))
        val c = start(lib)
        onMain { c.playback.open("c1") }
        until(c, "the first frame") { it?.shown != null }
        // the home stops answering: what is buffered plays out, then nothing moves for 15 s
        air.hanging = true
        val ms = until(c, "the stalled channel failed", limitMs = 45_000) { c.playback.status is ChannelStatus.Failed }
        println("13.22 T04 a stalled stream failed after $ms ms")
        assertTrue("failed after $ms ms, before a 15 s stall", ms >= ChannelVideo.STALL_MS)
        assertEquals(ChannelStatus.Failed(null, retryable = true), c.playback.status)
    }
}
