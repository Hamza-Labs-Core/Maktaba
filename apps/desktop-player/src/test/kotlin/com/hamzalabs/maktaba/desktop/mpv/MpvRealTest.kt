package com.hamzalabs.maktaba.desktop.mpv

import com.hamzalabs.maktaba.desktop.PlayRequest
import com.hamzalabs.maktaba.desktop.Playback
import com.hamzalabs.maktaba.desktop.TestHome
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.asCoroutineDispatcher
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withContext
import mockwebserver3.MockResponse
import okio.Buffer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Assume.assumeTrue
import org.junit.Test
import java.io.File
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicInteger

/**
 * Story 13.18 against the libmpv the Player carries, built by libmpv/build.sh (CI's Desktop
 * Player job sets MAKTABA_LIBMPV_DIR to it and MAKTABA_TEST_MEDIA to files ffmpeg made in the
 * job; elsewhere these tests are skipped):
 *  - T01: a 1080p H.264 MKV with AC-3 5.1 plays directly, no playlist asked for, its first
 *    frame within 1.5 s on Linux (the time is printed on both systems);
 *  - T02: a 4K HEVC 10-bit MKV plays directly, and the hardware decoder in use is known;
 *  - T05: a file that does not decode opens once more as a transcode, and plays;
 *  - T07 at run time: the library says it was configured without GPL;
 *  - story 13.19's T04 and T05: the decoder probe on the committed clips, and the audio
 *    device's channels.
 */
class MpvRealTest {
    private val dir = System.getenv("MAKTABA_LIBMPV_DIR")
    private val media = System.getenv("MAKTABA_TEST_MEDIA")?.let(::File)
    private val home = TestHome()
    private val thread = Executors.newSingleThreadExecutor { r -> Thread(r, "test-main").apply { isDaemon = true } }
    private val main = thread.asCoroutineDispatcher()
    private val scope = CoroutineScope(SupervisorJob() + main)
    private val background = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val opened = AtomicInteger()
    private var playback: Playback? = null

    private fun lib(): LibMpv {
        assumeTrue("MAKTABA_LIBMPV_DIR is not set", dir != null)
        val l = MpvLoad.from(dir)
        assertTrue("libmpv: $l", l is MpvLoad.Loaded)
        return (l as MpvLoad.Loaded).lib
    }

    private fun file(name: String): File {
        assumeTrue("MAKTABA_TEST_MEDIA is not set", media != null)
        return File(media, name).also { assertTrue("$it is missing", it.isFile) }
    }

    @After
    fun tearDown() {
        playback?.let { p -> runBlocking(main) { p.close() } }
        home.close()
        thread.shutdown()
    }

    private fun serve(path: String, f: File) = home.answer("GET $path") {
        MockResponse.Builder().code(200).addHeader("Content-Type", "video/x-matroska")
            .body(Buffer().apply { write(f.readBytes()) }).build()
    }

    /** Plays the sessions [paths] answer in turn and returns the time to the first frame, in ms. */
    private fun firstFrameMs(lib: LibMpv, vararg paths: String): Long {
        home.answer("GET /api/stream/capabilities", """{"client_profiles":["desktop-mpv","desktop-player"]}""")
        home.answer("POST /api/stream/sessions") {
            val n = opened.incrementAndGet()
            val path = paths[(n - 1).coerceAtMost(paths.size - 1)]
            TestHome.json("""{"session_id":"s$n","mode":"direct","direct_url":"$path"}""")
        }
        val p = Playback(home.client, scope, background, signedOut = {}, engine = { MpvPlayers(lib, null) })
        playback = p
        val start = System.nanoTime()
        runBlocking { withContext(main) { p.play(PlayRequest("v1", "v1", startSec = 0)) } }
        val end = System.currentTimeMillis() + 20_000
        while (System.currentTimeMillis() < end) {
            val v = runBlocking { withContext(main) { p.player } } as? MpvVideo
            if (v != null) {
                v.resize(1280, 720)
                if (v.frame != null && opened.get() == paths.size) return (System.nanoTime() - start) / 1_000_000
            }
            Thread.sleep(5)
        }
        throw AssertionError("no frame in 20 s; the home was asked ${home.seen.map { it.method + " " + it.url.encodedPath }}")
    }

    private fun bodies() = home.asked("POST /api/stream/sessions").map { it.body!!.utf8() }

    @Test
    fun t01_aFileWithAc3FiveOnePlaysDirectlyAndStartsWithinASecondAndAHalf() {
        val lib = lib()
        serve("/stream/film.mkv", file("h264-ac3-51-1080p.mkv"))
        val ms = firstFrameMs(lib, "/stream/film.mkv")
        println("T01 time to first frame (libmpv, direct, ${System.getProperty("os.name")}): $ms ms")
        assertTrue(bodies().single(), bodies().single().contains("\"client_profile\":\"desktop-mpv\""))
        assertTrue("a playlist was asked for", home.seen.none { it.url.encodedPath.endsWith(".m3u8") })
        if (System.getProperty("os.name").startsWith("Linux")) assertTrue("first frame after $ms ms", ms < 1_500)
    }

    @Test
    fun t02_hevcTenBitPlaysDirectlyAndTheDecoderIsKnown() {
        val lib = lib()
        serve("/stream/hevc.mkv", file("hevc-10bit-2160p.mkv"))
        val ms = firstFrameMs(lib, "/stream/hevc.mkv")
        val v = runBlocking { withContext(main) { playback!!.player } } as MpvVideo
        val end = System.currentTimeMillis() + 5_000
        while (v.hwdec == null && System.currentTimeMillis() < end) Thread.sleep(20)
        println("T02 HEVC 10-bit 2160p: first frame $ms ms, hwdec-current ${v.hwdec}")
        assertTrue("hwdec-current unknown", v.hwdec != null)
    }

    @Test
    fun t05_aFileThatDoesNotDecodeOpensOnceAsATranscode() {
        val lib = lib()
        home.answer("GET /stream/broken.mkv") { MockResponse.Builder().code(200).body("this is not a video").build() }
        serve("/stream/film.mkv", file("h264-ac3-51-1080p.mkv"))
        firstFrameMs(lib, "/stream/broken.mkv", "/stream/film.mkv")
        assertTrue(bodies()[1], bodies()[1].contains("\"force_transcode\":true"))
    }

    /**
     * Story 13.19 T04: the decoder probe on the committed clips. Every codec decodes, mpv's
     * hwdec-current is read for each ("no" on the runners, which have no GPU decoder), the
     * report is built, and it takes under 5 s on the Linux runner (asserted there only; Windows'
     * time and each clip's speed are printed, for both).
     */
    @Test
    fun t1319_04_theProbeDecodesEveryClip() {
        val lib = lib()
        val dir = kotlin.io.path.createTempDirectory("probe").toFile()
        try {
            val clips = Probe.extractClips(File(dir, "clips"))
            val decoder = MpvProbeDecoder(lib)
            try {
                val key = ProbeKey.of(decoder.version)
                val start = System.nanoTime()
                val r = Probe(decoder, clips, ProbeCache(File(dir, "probe.json")), key.toString()).run()
                val ms = (System.nanoTime() - start) / 1_000_000
                val report = DeviceReport().apply { probe = r }
                println("T04 probe (${System.getProperty("os.name")}): $ms ms; ${report.describe()}")
                assertTrue("incomplete: $r", r.complete && r.clips.size == Probe.CLIPS.size)
                for (c in r.clips) {
                    assertTrue("${c.codec} ${c.bitDepth}-bit did not decode", c.decoded)
                    assertTrue("${c.codec}: hwdec-current not read", c.hwdec != null)
                    if (!c.hardware) assertTrue("${c.codec}: no speed", (c.softwareFps ?: 0.0) > 0)
                }
                val caps = DesktopCaps.build(r.clips, null)
                assertEquals(DesktopCaps.CODECS, caps.video.map { it.codec })
                // the 5 s budget is held on the Linux runner only; Windows' time is printed, and
                // both stay within the probe's own 20 s bound
                assertTrue("the probe took $ms ms, over its bound", ms < Probe.BOUND_MS)
                if (System.getProperty("os.name").startsWith("Linux")) assertTrue("the probe took $ms ms on Linux, over 5 s", ms < 5_000)
                for (c in r.clips) println("T04   ${c.codec} ${c.bitDepth}-bit: hwdec ${c.hwdec}, %.1f fps at 2160p".format(c.softwareFps ?: 0.0))
                // run again with the same key: read back, nothing decoded
                val again = System.nanoTime()
                assertEquals(r, Probe(decoder, clips, ProbeCache(File(dir, "probe.json")), key.toString()).run())
                assertTrue((System.nanoTime() - again) / 1_000_000 < 500)
            } finally {
                decoder.close()
            }
        } finally {
            dir.deleteRecursively()
        }
    }

    /**
     * Story 13.19 T05: the audio device's channels, on the runner's audio (none on the Linux
     * runner, the default device on Windows): a count, or the field left out, with no error.
     */
    @Test
    fun t1319_05_theAudioDevicesChannels() {
        val lib = lib()
        val dir = kotlin.io.path.createTempDirectory("probe").toFile()
        try {
            val clips = Probe.extractClips(dir)
            val decoder = MpvProbeDecoder(lib)
            try {
                val start = System.nanoTime()
                val n = decoder.channels(File(clips, Probe.SILENCE), 3_000)
                println("T05 audio channels (${System.getProperty("os.name")}): ${n ?: "no device"} in ${(System.nanoTime() - start) / 1_000_000} ms")
                assertTrue("channels: $n", n == null || n in 1..64)
                val j = DesktopCaps.build(emptyList(), n).toJson()
                assertEquals(n != null, "max_audio_channels" in j)
            } finally {
                decoder.close()
            }
        } finally {
            dir.deleteRecursively()
        }
    }

    @Test
    fun t07_theLibraryIsNotConfiguredGpl() {
        val lib = lib()
        val c = lib.mpv_create()!!
        try {
            // properties are read once the handle is initialized; nothing to show or play here
            lib.mpv_set_option_string(c, "vo", "null")
            lib.mpv_set_option_string(c, "ao", "null")
            assertTrue("mpv_initialize", lib.mpv_initialize(c) >= 0)
            val p = lib.mpv_get_property_string(c, "mpv-configuration")!!
            val conf = p.getString(0, "UTF-8").also { lib.mpv_free(p) }
            println("mpv-configuration: $conf")
            assertTrue(conf, conf.contains("gpl=false"))
        } finally {
            lib.mpv_terminate_destroy(c)
        }
    }
}
