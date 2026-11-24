package com.hamzalabs.maktaba.desktop.mpv

import androidx.compose.ui.graphics.toAwtImage
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
 *  - T07 at run time: the library says it was configured without GPL.
 *
 * Story 13.20, on test-media.sh's `subs-ar.mkv` (a black 1280x720 picture; an Arabic SRT, an
 * Arabic ASS whose style names Arial in green, and the committed PGS, each on from 1 s to 29 s)
 * and a sidecar the pretend home serves:
 *  - T05: each, chosen in the menu, is drawn by mpv; the frame is compared on Linux with its
 *    reference (src/test/resources/references/mpv-subs-*.png, reviewed once for Arabic joined
 *    and right to left), as FrameReferencesTest does;
 *  - T06: Large and Yellow: the SRT line yellow and about 1.3 times as tall, the ASS line as
 *    tall too in its own green, the PGS picture as authored.
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

    // ---------- story 13.20: subtitles drawn by mpv ----------

    @get:org.junit.Rule val tmp = org.junit.rules.TemporaryFolder()

    /** mpv's log of the video playing (build/reports/mpv/). */
    private var mpvLog: File? = null

    /** The Player's caption face, from its resources, in a folder libass reads (as Engine's). */
    private fun fonts(): File = tmp.newFolder("fonts").also { d ->
        for (name in listOf("ibm_plex_sans_arabic_regular.ttf", "ibm_plex_sans_arabic_semibold.ttf")) {
            javaClass.classLoader.getResourceAsStream(name)?.use { File(d, name).writeBytes(it.readBytes()) }
        }
    }

    /**
     * subs-ar.mkv played directly from 3 s with [values]' size and colour, the pretend home
     * offering an English sidecar; returns once the menu has the sidecar and the file's three tracks.
     */
    private fun playSubtitled(lib: LibMpv, values: com.hamzalabs.maktaba.core.playback.PlaybackValues): Playback {
        serve("/stream/subs.mkv", file("subs-ar.mkv"))
        home.answer("GET /api/stream/capabilities", """{"client_profiles":["desktop-mpv","desktop-player"]}""")
        home.answer("POST /api/stream/sessions") {
            val n = opened.incrementAndGet()
            TestHome.json("""{"session_id":"s$n","mode":"direct","direct_url":"/stream/subs.mkv","stream_sig":"sig$n"}""")
        }
        home.answer("GET /api/videos/v1/subtitles", """{"items":[{"id":1,"language":"en","format":"srt","source":"external"}]}""")
        home.answer("GET /stream/s1/subs/sidecar/en.vtt") {
            MockResponse.Builder().code(200).addHeader("Content-Type", "text/vtt")
                .body("WEBVTT\n\n00:00:01.000 --> 00:00:29.000\nWelcome to Maktaba\n").build()
        }
        val fonts = fonts()
        // mpv's own log for the run, kept with the job's reports
        mpvLog = File("build/reports/mpv/${System.nanoTime()}.log").absoluteFile.also { it.parentFile.mkdirs() }
        val extra = mapOf("log-file" to mpvLog!!.path, "msg-level" to "all=v")
        val p = Playback(home.client, scope, background, signedOut = {}, settings = { values }, engine = { MpvPlayers(lib, fonts, extra) }, log = { println("log: $it") })
        playback = p
        // from the start: each line is on from 1 s to 29 s
        runBlocking { withContext(main) { p.play(PlayRequest("v1", "v1", startSec = 0)) } }
        val end = System.currentTimeMillis() + 20_000
        while (runBlocking { withContext(main) { p.subtitleOptions.size } } < 4) {
            if (System.currentTimeMillis() > end) throw AssertionError("no subtitles menu: ${runBlocking { withContext(main) { p.subtitleOptions } }}")
            Thread.sleep(20)
        }
        return p
    }

    /** A drawn line: its box in the lower part of the picture, and the mean colour of its brightest pixels. */
    private data class Line(val pixels: Int, val height: Int, val r: Int, val g: Int, val b: Int, val green: Int = 0, val white: Int = 0)

    private fun line(img: java.awt.image.BufferedImage): Line {
        var n = 0
        var top = Int.MAX_VALUE
        var bottom = -1
        var core = 0
        var green = 0
        var white = 0
        var r = 0L
        var g = 0L
        var b = 0L
        for (y in img.height * 6 / 10 until img.height) for (x in 0 until img.width) {
            val p = img.getRGB(x, y)
            val (pr, pg, pb) = Triple(p shr 16 and 255, p shr 8 and 255, p and 255)
            val m = maxOf(pr, pg, pb)
            if (m > 96) {
                n++
                top = minOf(top, y)
                bottom = maxOf(bottom, y)
            }
            if (pg > 180 && pr < 100 && pb < 100) green++
            if (pr > 200 && pg > 200 && pb > 200) white++
            if (m > 200) {
                core++
                r += pr
                g += pg
                b += pb
            }
        }
        return if (core == 0) Line(n, 0, 0, 0, 0, green, white)
        else Line(n, bottom - top + 1, (r / core).toInt(), (g / core).toInt(), (b / core).toInt(), green, white)
    }

    /** Chooses [title]'s subtitle (null: the home's sidecar) and returns the frame once a line is on it. */
    /**
     * How each track's line is told apart on screen, chosen in the order SRT, ASS, PGS (so the
     * frame is never the track's before): the ASS is green, the others are not.
     */
    private fun looks(title: String): (Line) -> Boolean = when (title) {
        "ASS" -> { l -> l.green > 100 }
        else -> { l -> l.pixels > 300 && l.green == 0 }
    }

    private fun drawn(p: Playback, title: String?, shows: (Line) -> Boolean = { it.pixels > 300 }): java.awt.image.BufferedImage {
        runBlocking {
            withContext(main) {
                val o = if (title == null) p.subtitleOptions.first { it.kind == com.hamzalabs.maktaba.desktop.watch.SubtitleOption.Kind.Home }
                else p.subtitleOptions.first { it.name == title }
                p.chooseSubtitle(o)
            }
        }
        val end = System.currentTimeMillis() + 15_000
        var last: Line? = null
        while (System.currentTimeMillis() < end) {
            val v = runBlocking { withContext(main) { p.player } } as? MpvVideo
            v?.resize(1280, 720)
            val f = v?.frame
            if (f != null) {
                val img = f.toAwtImage()
                last = line(img)
                if (shows(last)) return img
            }
            Thread.sleep(50)
        }
        val v = runBlocking { withContext(main) { p.player } } as? MpvVideo
        val state = listOf("time-pos", "sid", "sub-text", "current-tracks/sub/codec", "track-list/count", "vo-configured", "osd-dimensions/w")
            .joinToString(", ") { "$it=${v?.property(it)}" }
        val lines = mpvLog?.takeIf { it.isFile }?.readLines().orEmpty()
            .filter { l -> listOf("sub", "ass", "font", "libass", "error", "Error").any { it in l } }.takeLast(60)
        throw AssertionError("${title ?: "the sidecar"}: no line drawn (last $last); mpv: $state\n" + lines.joinToString("\n"))
    }

    /** As FrameReferencesTest: compared on Linux with src/test/resources/references/[name].png; a missing one is written and fails. */
    private fun reference(name: String, img: java.awt.image.BufferedImage) {
        val build = File("build")
        File(build, "mpv-references").also { it.mkdirs() }.let { javax.imageio.ImageIO.write(img, "png", File(it, "$name.png")) }
        if (!System.getProperty("os.name").orEmpty().startsWith("Linux")) return
        val ref = File("src/test/resources/references/$name.png")
        if (!ref.exists()) {
            File(build, "new-references").also { it.mkdirs() }.let { javax.imageio.ImageIO.write(img, "png", File(it, "$name.png")) }
            throw AssertionError("$name: no committed reference; this run wrote build/new-references/$name.png (look at it: the Arabic joined and right to left; commit it)")
        }
        val want = javax.imageio.ImageIO.read(ref)
        assertTrue("$name: ${img.width}x${img.height}, the reference ${want.width}x${want.height}", want.width == img.width && want.height == img.height)
        var differ = 0
        for (y in 0 until want.height) for (x in 0 until want.width) {
            val a = want.getRGB(x, y)
            val b = img.getRGB(x, y)
            val d = maxOf(kotlin.math.abs((a shr 16 and 255) - (b shr 16 and 255)), kotlin.math.abs((a shr 8 and 255) - (b shr 8 and 255)), kotlin.math.abs((a and 255) - (b and 255)))
            if (d > 16) differ++
        }
        val ratio = differ.toDouble() / (want.width * want.height)
        if (ratio > 0.001) {
            File(build, "reference-diffs").also { it.mkdirs() }.let { javax.imageio.ImageIO.write(img, "png", File(it, "$name.png")) }
            throw AssertionError("$name: ${"%.4f".format(ratio)} of the pixels differ from the reference")
        }
    }

    @Test
    fun s13_20_t05_eachSubtitleIsDrawnByMpv() {
        val lib = lib()
        val p = playSubtitled(lib, com.hamzalabs.maktaba.core.playback.PlaybackValues())
        val names = runBlocking { withContext(main) { p.subtitleOptions.map { it.kind to (it.name ?: it.language) } } }
        println("13.20 T05 menu: $names")
        for (title in listOf("SRT", "ASS", "PGS")) {
            val img = drawn(p, title, looks(title))
            println("13.20 T05 $title: ${line(img)}")
            reference("mpv-subs-${title.lowercase()}", img)
        }
        // the sidecar after the ASS: its own white line shows, and the ASS's green one is gone
        val ass = line(drawn(p, "ASS", looks("ASS")))
        assertTrue("the ASS line is green: $ass", ass.green > 100)
        val sidecar = drawn(p, null) { it.green == 0 && it.white > 300 }
        println("13.20 T05 sidecar: ${line(sidecar)}")
        reference("mpv-subs-sidecar", sidecar)
        assertTrue("the sidecar was asked for through the proxy, signed", home.seen.any { it.url.encodedPath == "/stream/s1/subs/sidecar/en.vtt" && it.url.queryParameter("sig") == "sig1" })
    }

    @Test
    fun s13_20_t06_largeAndYellowApplyToTextWithoutItsOwnStyle() {
        val lib = lib()
        val plain = playSubtitled(lib, com.hamzalabs.maktaba.core.playback.PlaybackValues())
        val srt = line(drawn(plain, "SRT", looks("SRT")))
        val ass = line(drawn(plain, "ASS", looks("ASS")))
        val pgs = line(drawn(plain, "PGS", looks("PGS")))
        runBlocking { withContext(main) { plain.close() } }
        opened.set(0)
        val big = playSubtitled(
            lib,
            com.hamzalabs.maktaba.core.playback.PlaybackValues(
                subtitleSize = com.hamzalabs.maktaba.core.playback.SubtitleSize.Large,
                subtitleColour = com.hamzalabs.maktaba.core.playback.SubtitleColour.Yellow,
            ),
        )
        val srtBig = line(drawn(big, "SRT", looks("SRT")))
        val assBig = line(drawn(big, "ASS", looks("ASS")))
        val pgsBig = line(drawn(big, "PGS", looks("PGS")))
        println("13.20 T06 SRT $srt -> $srtBig; ASS $ass -> $assBig; PGS $pgs -> $pgsBig")
        // the SRT: white, then yellow (#FFE14D: red and green high, blue low), about 1.3 times as tall
        assertTrue("SRT white: $srt", srt.b > 200 && srt.g > 200)
        assertTrue("SRT yellow: $srtBig", srtBig.r > 200 && srtBig.g > 180 && srtBig.b < 140)
        val srtRatio = srtBig.height.toDouble() / srt.height
        assertTrue("SRT larger: $srtRatio", srtRatio in 1.15..1.5)
        // the ASS: its own green both times, larger too (sub-ass-override=scale)
        assertTrue("ASS green: $ass, $assBig", ass.g > 200 && ass.r < 100 && assBig.g > 200 && assBig.r < 100)
        val assRatio = assBig.height.toDouble() / ass.height
        assertTrue("ASS larger: $assRatio", assRatio in 1.15..1.5)
        // the PGS picture as authored: neither size nor colour
        assertTrue("PGS the same size: $pgs, $pgsBig", kotlin.math.abs(pgsBig.height - pgs.height) <= 2)
        assertTrue("PGS white: $pgsBig", pgsBig.b > 200)
    }
}
