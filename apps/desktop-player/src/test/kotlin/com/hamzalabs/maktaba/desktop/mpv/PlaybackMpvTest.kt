package com.hamzalabs.maktaba.desktop.mpv

import com.hamzalabs.maktaba.core.api.Capabilities
import com.hamzalabs.maktaba.core.api.Feature
import com.hamzalabs.maktaba.core.playback.PlaybackValues
import com.hamzalabs.maktaba.core.playback.SubtitleColour
import com.hamzalabs.maktaba.core.playback.SubtitleLanguage
import com.hamzalabs.maktaba.core.playback.SubtitleSize
import com.hamzalabs.maktaba.desktop.watch.FileSubtitle
import com.hamzalabs.maktaba.desktop.watch.SubtitleOption
import com.hamzalabs.maktaba.desktop.watch.SubtitleStyle
import com.hamzalabs.maktaba.desktop.AudioOption
import com.hamzalabs.maktaba.desktop.PlayRequest
import com.hamzalabs.maktaba.desktop.Playback
import com.hamzalabs.maktaba.desktop.Players
import com.hamzalabs.maktaba.desktop.TestHome
import com.hamzalabs.maktaba.desktop.Video
import com.hamzalabs.maktaba.desktop.VideoEvents
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.asCoroutineDispatcher
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withContext
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.util.Collections
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicInteger

/**
 * Story 13.18 with a fake libmpv: the profile asked for follows what the home knows (T04), a
 * direct file that does not play opens once more as a transcode (T05), and a direct file's
 * audio track is switched in the player.
 */
class PlaybackMpvTest {
    private val home = TestHome()
    private val thread = Executors.newSingleThreadExecutor { r -> Thread(r, "test-main").apply { isDaemon = true } }
    private val main = thread.asCoroutineDispatcher()
    private val scope = CoroutineScope(SupervisorJob() + main)
    private val background = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val opened = AtomicInteger()

    private class FakeMpv(val events: VideoEvents, val draws: Boolean = true) : Video {
        val selected: MutableList<Int> = Collections.synchronizedList(mutableListOf())
        /** The subtitles it was asked to draw (story 13.20), in order; null: off. */
        val subtitles: MutableList<SubtitleOption?> = Collections.synchronizedList(mutableListOf())
        @Volatile override var started = false
        override fun toggle() {}
        override fun pause() {}
        override fun seek(ms: Long) {}
        override fun sound(volume: Float, muted: Boolean) {}
        @Volatile var disposed = false
        override fun dispose() {
            disposed = true
        }
        override fun selectAudio(index: Int): Boolean {
            selected += index
            return true
        }
        override fun selectSubtitle(option: SubtitleOption?): Boolean {
            if (!draws || option?.kind == SubtitleOption.Kind.Stream) return false
            subtitles += option
            return true
        }
    }

    private val made: MutableList<FakeMpv> = Collections.synchronizedList(mutableListOf())

    /** The probe's report (story 13.19): HEVC 10-bit in hardware, AV1 in software up to 1080p, 5.1 speakers. */
    private val report = DeviceReport().apply {
        probe = ProbeResult(
            "k",
            listOf(
                ClipResult("hevc", 10, decoded = true, hwdec = "vaapi-copy"),
                ClipResult("av1", 10, decoded = true, hwdec = "no", softwareFps = 20.0),
            ),
            900,
        )
        channels = 6
    }

    private val players = object : Players {
        override val profile = MPV_PROFILE
        override val drawsSubtitles = true
        override fun deviceCaps() = report.json()
        override suspend fun open(url: String, fromMs: Long, autoplay: Boolean, events: VideoEvents): Video {
            // as mpv does: an HLS stream's master is read through the proxy
            if (".m3u8" in url) okhttp3.OkHttpClient().newCall(okhttp3.Request.Builder().url(url).build()).execute().close()
            return FakeMpv(events).also { made += it }
        }
    }

    /** JavaFX's, as the older engine (story 13.21): the `desktop-player` profile. */
    private val fxMade: MutableList<FakeMpv> = Collections.synchronizedList(mutableListOf())
    private val fx = Players { _, _, _, events -> FakeMpv(events, draws = false).also { fxMade += it } }

    /** The Player's own settings, where Profile keeps the engine (story 13.21). */
    private val prefs = com.hamzalabs.maktaba.desktop.shell.AppPrefs(com.hamzalabs.maktaba.core.MapStore())

    /** The Player's engines, as Main sets them up: libmpv loaded, the setting read at each video's open. */
    private val engines = Engines(
        emptyMap(),
        load = {
            val lib = java.lang.reflect.Proxy.newProxyInstance(LibMpv::class.java.classLoader, arrayOf(LibMpv::class.java)) { _, _, _ -> null } as LibMpv
            MpvLoad.Loaded(lib, java.io.File("libmpv.so.2"))
        },
        mpv = { players },
        fx = fx,
    ).apply { setting = { prefs.engine } }

    /** Profile → Playback's settings, and what the home has on. */
    @Volatile private var settings = PlaybackValues()
    @Volatile private var features = Capabilities.AllOn

    /** The problem report's lines Playback wrote. */
    private val logged: MutableList<String> = Collections.synchronizedList(mutableListOf())

    private val playback = Playback(
        home.client, scope, background, signedOut = {}, settings = { settings }, features = { features },
        engine = { engines.players() }, log = { logged += it },
    )

    init {
        home.answer("POST /api/stream/sessions") {
            val n = opened.incrementAndGet()
            TestHome.json("""{"session_id":"s$n","mode":"direct","direct_url":"/stream/s$n.mkv","stream_sig":"SIG$n"}""")
        }
        home.answer("GET /api/videos/v1/subtitles", """{"items":[]}""")
    }

    @After
    fun tearDown() {
        runBlocking(main) { playback.close() }
        home.close()
        thread.shutdown()
    }

    private fun <T> onMain(block: () -> T): T = runBlocking { withContext(main) { block() } }

    private fun until(what: String, ok: () -> Boolean) {
        val end = System.currentTimeMillis() + 10_000
        while (!onMain(ok)) {
            if (System.currentTimeMillis() > end) throw AssertionError("never: $what")
            Thread.sleep(20)
        }
    }

    private fun bodies() = home.asked("POST /api/stream/sessions").map { it.body!!.utf8() }

    @Test
    fun aHomeThatKnowsDesktopMpvIsAskedForIt() {
        home.answer("GET /api/stream/capabilities", """{"client_profiles":["desktop-mpv","desktop-player","generic"]}""")
        onMain { playback.play(PlayRequest("v1", "v1", startSec = 0)) }
        until("playing") { playback.player != null }
        assertTrue(bodies().single(), bodies().single().contains("\"client_profile\":\"desktop-mpv\""))
    }

    @Test
    fun t02_theEngineChosenInProfileAppliesToTheNextVideo() {
        home.answer("GET /api/stream/capabilities", """{"client_profiles":["desktop-mpv","desktop-player"]}""")
        onMain { playback.play(PlayRequest("v1", "v1", startSec = 0)) }
        until("v1 playing") { playback.player != null && playback.player === made.singleOrNull() }
        val v1 = made.single()
        // the older engine chosen in Profile while v1 plays: v1's player and session are untouched
        onMain { prefs.chooseEngine(EngineChoice.Older) }
        Thread.sleep(300)
        assertTrue(onMain { playback.player } === v1)
        assertFalse(v1.disposed)
        assertEquals(1, opened.get())
        assertTrue("v1's session was not closed", home.asked("DELETE /api/stream/sessions/s1").isEmpty())
        // a reopen of v1 (a quality cap) keeps v1's engine: libmpv's players and desktop-mpv
        onMain { playback.chooseQuality(com.hamzalabs.maktaba.core.playback.QualityOption(720, 3000)) }
        until("v1 reopened") { made.size == 2 && playback.player === made[1] }
        assertTrue(bodies()[1], bodies()[1].contains("\"client_profile\":\"desktop-mpv\""))
        assertTrue("no JavaFX player for the video playing", fxMade.isEmpty())
        // the next video opens with JavaFX and asks for desktop-player
        onMain { playback.play(PlayRequest("v2", "v2", startSec = 0)) }
        until("v2 playing") { playback.player != null && playback.player === fxMade.singleOrNull() }
        assertTrue(bodies()[2], bodies()[2].contains("\"client_profile\":\"desktop-player\""))
        assertTrue(made[1].disposed)
        // Automatic again: v2 is untouched; the next video is libmpv's, with desktop-mpv
        onMain { prefs.chooseEngine(EngineChoice.Auto) }
        Thread.sleep(300)
        assertEquals(3, opened.get())
        assertFalse(fxMade.single().disposed)
        assertTrue(onMain { playback.player } === fxMade.single())
        onMain { playback.play(PlayRequest("v3", "v3", startSec = 0)) }
        until("v3 playing") { made.size == 3 && playback.player === made[2] }
        assertTrue(bodies()[3], bodies()[3].contains("\"client_profile\":\"desktop-mpv\""))
        assertEquals(1, fxMade.size)
        // the problem report names each video's engine and why
        val log = engines.logLines().joinToString("\n")
        assertTrue(log, log.contains("libmpv, automatic") && log.contains("JavaFX, chosen in Profile"))
    }

    @Test
    fun aFileThatFailsWithTheOlderEngineIsNotForcedToTranscode() {
        // JavaFX's sessions already ask the home to convert: a failure is the watch loop's
        prefs.chooseEngine(EngineChoice.Older)
        home.answer("GET /api/stream/capabilities", """{"client_profiles":["desktop-mpv"]}""")
        onMain { playback.play(PlayRequest("v1", "v1", startSec = 0)) }
        until("playing") { playback.player != null && playback.player === fxMade.singleOrNull() }
        fxMade.single().started = false
        fxMade.single().events.failed("no decoder")
        Thread.sleep(300)
        assertEquals(1, opened.get())
    }

    @Test
    fun anOlderHomeIsAskedForDesktopPlayer() {
        // a home from before story 13.18: its capabilities list no client profiles
        home.answer("GET /api/stream/capabilities", """{"codecs":["h264"],"hwaccel":"none","max_bitrate_kbps":40000,"supported_containers":["mp4"]}""")
        onMain { playback.play(PlayRequest("v1", "v1", startSec = 0)) }
        until("playing") { playback.player != null }
        assertTrue(bodies().single(), bodies().single().contains("\"client_profile\":\"desktop-player\""))
    }

    @Test
    fun aDirectFileThatFailsOpensOnceAsATranscode() {
        home.answer("GET /api/stream/capabilities", """{"client_profiles":["desktop-mpv"]}""")
        onMain { playback.play(PlayRequest("v1", "v1", startSec = 0)) }
        until("playing") { playback.player != null }
        made.single().events.failed("no decoder for this stream")
        until("reopened") { opened.get() == 2 && (playback.player as? FakeMpv) === made.getOrNull(1) }
        val second = bodies()[1]
        assertTrue(second, second.contains("\"force_transcode\":true"))
        // a second failure is not reopened again at once: it is the watch loop's to report
        made[1].events.failed("again")
        Thread.sleep(300)
        assertEquals(2, opened.get())
    }

    /** Story 13.19 T06: the report goes with desktop-mpv, not on the forced transcode's reopen. */
    @Test
    fun t06_theReportGoesWithDesktopMpvButNotOnAForcedTranscode() {
        home.answer("GET /api/stream/capabilities", """{"client_profiles":["desktop-mpv"]}""")
        onMain { playback.play(PlayRequest("v1", "v1", startSec = 0)) }
        until("playing") { playback.player != null }
        val first = bodies().single()
        // the codecs this recorded probe did not reach are listed without limits
        assertTrue(first, first.contains(""""device_caps":{"video":[{"codec":"h264"},{"codec":"hevc","max_height":2160,"bit_depth":10,"hardware":true},{"codec":"vp9"},{"codec":"av1","max_height":1080,"bit_depth":10,"hardware":false},{"codec":"mpeg2video"}],"max_audio_channels":6}"""))
        made.single().events.failed("no decoder for this stream")
        until("reopened") { opened.get() == 2 }
        val second = bodies()[1]
        assertTrue(second, second.contains("\"force_transcode\":true"))
        assertFalse(second, second.contains("device_caps"))
    }

    /** T06: an older home (desktop-player) is sent no report, nor is JavaFX's engine. */
    @Test
    fun t06_noReportWithDesktopPlayer() {
        home.answer("GET /api/stream/capabilities", """{"codecs":["h264"]}""")
        onMain { playback.play(PlayRequest("v1", "v1", startSec = 0)) }
        until("playing") { playback.player != null }
        assertTrue(bodies().single(), bodies().single().contains("\"client_profile\":\"desktop-player\""))
        assertFalse(bodies().single(), bodies().single().contains("device_caps"))
        // JavaFX's players have no report
        assertEquals(null, com.hamzalabs.maktaba.desktop.FxPlayers.deviceCaps())
    }

    /** T06: before the probe has run, a video opens with the profile alone. */
    @Test
    fun beforeTheProbeAVideoOpensWithTheProfileAlone() {
        report.probe = null
        home.answer("GET /api/stream/capabilities", """{"client_profiles":["desktop-mpv"]}""")
        onMain { playback.play(PlayRequest("v1", "v1", startSec = 0)) }
        until("playing") { playback.player != null }
        assertTrue(bodies().single(), bodies().single().contains("\"client_profile\":\"desktop-mpv\""))
        assertFalse(bodies().single(), bodies().single().contains("device_caps"))
    }

    /**
     * Story 13.19 T07: a home whose session body reader refuses device_caps: the stream opens
     * without it on the same open, and the Player sends this home no report again.
     */
    @Test
    fun t07_aHomeThatRefusesTheReportIsAskedAgainWithoutIt() {
        home.answer("GET /api/stream/capabilities", """{"client_profiles":["desktop-mpv"]}""")
        home.answer("POST /api/stream/sessions") { r ->
            if (r.body!!.utf8().contains("device_caps")) {
                TestHome.json("""{"type":"https://maktaba.dev/problems/invalid-json","title":"invalid json","status":400,"detail":"json: unknown field \"device_caps\""}""", 400)
            } else {
                val n = opened.incrementAndGet()
                TestHome.json("""{"session_id":"s$n","mode":"direct","direct_url":"/stream/s$n.mkv"}""")
            }
        }
        onMain { playback.play(PlayRequest("v1", "v1", startSec = 0)) }
        until("playing") { playback.player != null }
        assertEquals(null, playback.problem)
        assertEquals(2, bodies().size)
        assertTrue(bodies()[0].contains("device_caps"))
        assertFalse(bodies()[1].contains("device_caps"))
        // the next video: no report sent to this home
        onMain { playback.play(PlayRequest("v2", "v2", startSec = 0)) }
        until("the second plays") { opened.get() == 2 && made.size == 2 }
        assertEquals(3, bodies().size)
        assertFalse(bodies()[2], bodies()[2].contains("device_caps"))
    }

    @Test
    fun aFailureAfterThePictureStartedIsNotTheFiles() {
        home.answer("GET /api/stream/capabilities", """{"client_profiles":["desktop-mpv"]}""")
        onMain { playback.play(PlayRequest("v1", "v1", startSec = 0)) }
        until("playing") { playback.player != null }
        made.single().started = true
        made.single().events.failed("the relay dropped")
        Thread.sleep(300)
        assertEquals("no transcode for a file that played", 1, opened.get())
    }

    @Test
    fun aDirectFilesAudioTrackIsSwitchedInThePlayer() {
        home.answer("GET /api/stream/capabilities", """{"client_profiles":["desktop-mpv"]}""")
        onMain { playback.play(PlayRequest("v1", "v1", startSec = 0)) }
        until("playing") { playback.player != null }
        val tracks = listOf(AudioOption("English", "en", index = 0), AudioOption("العربية", "ar", index = 1))
        made.single().events.ready(60_000, tracks)
        until("tracks offered") { playback.audioOptions.size == 2 }
        onMain { playback.chooseAudio(tracks[1]) }
        until("switched") { playback.audio == tracks[1] }
        assertEquals(listOf(1), made.single().selected)
        assertEquals("no new session for a track", 1, opened.get())
        // the next player of this video (a renewal, a quality change) plays the same track
        assertEquals(1, made.single().events.audioIndex)
    }

    // ---------- story 13.20: subtitles drawn by mpv ----------

    /** A file's own tracks: an Arabic SRT, an English ASS, a PGS, and a forced SRT. */
    private val fileTracks = listOf(
        FileSubtitle("1", null, "ara", "subrip"),
        FileSubtitle("2", "English", "eng", "ass"),
        FileSubtitle("3", null, null, "hdmv_pgs_subtitle"),
        FileSubtitle("4", "Signs", "eng", "subrip", forced = true),
    )

    /** Plays v1 with libmpv's players; its player reports [tracks] once open. */
    private fun playDirect(tracks: List<FileSubtitle> = fileTracks): FakeMpv {
        home.answer("GET /api/stream/capabilities", """{"client_profiles":["desktop-mpv"]}""")
        onMain { playback.play(PlayRequest("v1", "v1", startSec = 0)) }
        until("playing") { playback.player != null }
        val p = made.last()
        p.events.subtitleTracks(tracks)
        return p
    }

    @Test
    fun t04_theMenuInDirectPlayAndWhatMpvIsAskedToDraw() {
        home.answer(
            "GET /api/videos/v1/subtitles",
            """{"items":[{"id":7,"language":"en","format":"srt","source":"external"},{"id":8,"language":"ar","format":"vtt","source":"generated"},{"id":9,"language":"ar","format":"srt","source":"embedded"}]}""",
        )
        val p = playDirect()
        until("the menu") { playback.subtitleOptions.size == 5 }
        val menu = onMain { playback.subtitleOptions }
        assertEquals(
            listOf(SubtitleOption.Kind.Home, SubtitleOption.Kind.Home, SubtitleOption.Kind.File, SubtitleOption.Kind.File, SubtitleOption.Kind.File),
            menu.map { it.kind },
        )
        // the home's, through the stream proxy, under the session's path, signed with its stream_sig
        val sidecar = menu[0].url!!
        assertTrue(sidecar, sidecar.startsWith("http://127.0.0.1:") && sidecar.endsWith("/stream/s1/subs/sidecar/en.vtt?sig=SIG1"))
        // Maktaba's transcription is served at the same path, by language
        assertTrue(menu[1].url!!, menu[1].url!!.endsWith("/stream/s1/subs/sidecar/ar.vtt?sig=SIG1"))
        assertEquals(listOf("1", "2", "3"), menu.drop(2).map { it.trackId })
        // subtitles off by default: nothing chosen
        assertEquals(emptyList<SubtitleOption?>(), p.subtitles.toList())

        onMain { playback.chooseSubtitle(menu[2]) }
        onMain { playback.chooseSubtitle(menu[0]) }
        onMain { playback.chooseSubtitle(null) }
        assertEquals(listOf("1", null, null), p.subtitles.map { it?.trackId })
        assertEquals(listOf(SubtitleOption.Kind.File, SubtitleOption.Kind.Home, null), p.subtitles.map { it?.kind })
        assertEquals(sidecar, p.subtitles[1]!!.url)
        // the choice is kept for the next video, by language
        assertEquals(true, playback.choice.textOff)
        // S: on again, in the first track
        onMain { playback.toggleSubtitles() }
        assertEquals(menu[0], onMain { playback.subtitle })
        // a reopen's player draws the same (not an HLS rendition's)
        assertEquals(menu[0], p.events.subtitleChoice)
    }

    @Test
    fun subtitlesOnInArabicPickTheFilesArabicTrackWithNoSidecar() {
        settings = PlaybackValues(subtitlesOn = true, subtitleLanguage = SubtitleLanguage.Arabic)
        val p = playDirect(listOf(FileSubtitle("1", null, "eng", "subrip"), FileSubtitle("2", null, "ara", "subrip")))
        until("on") { p.subtitles.isNotEmpty() }
        assertEquals("2", p.subtitles.single()!!.trackId)
        assertEquals("2", onMain { playback.subtitle }!!.trackId)
    }

    @Test
    fun theHomesComesFirstWhenTwoShareALanguage() {
        settings = PlaybackValues(subtitlesOn = true, subtitleLanguage = SubtitleLanguage.Arabic)
        home.answer("GET /api/videos/v1/subtitles", """{"items":[{"id":8,"language":"ar","format":"vtt","source":"generated"}]}""")
        val p = playDirect(listOf(FileSubtitle("1", null, "ara", "subrip")))
        until("on") { p.subtitles.isNotEmpty() }
        assertEquals(SubtitleOption.Kind.Home, p.subtitles.single()!!.kind)
    }

    @Test
    fun aHomeThatCannotListItsSubtitlesLeavesTheFilesAndSaysWhyInTheLog() {
        home.answer("GET /api/videos/v1/subtitles", """{"title":"boom","status":500}""", 500)
        playDirect()
        until("the menu") { playback.subtitleOptions.size == 3 }
        assertTrue(onMain { playback.subtitleOptions }.all { it.kind == SubtitleOption.Kind.File })
        assertTrue(logged.toString(), logged.any { it.startsWith("subtitles: the home's list of v1 was not read") })
    }

    @Test
    fun subtitlesOffOnTheHomeOfferNoTrackAtAll() {
        features = TestHome.caps(Feature.SUBTITLES)
        settings = PlaybackValues(subtitlesOn = true)
        val p = playDirect()
        Thread.sleep(300)
        assertTrue(onMain { playback.subtitleOptions }.isEmpty())
        assertTrue(p.subtitles.isEmpty())
    }

    @Test
    fun theOlderEngineHasTheMenuAsBefore() {
        prefs.chooseEngine(EngineChoice.Older)
        settings = PlaybackValues(subtitlesOn = true)
        home.answer("GET /api/stream/capabilities", """{"client_profiles":["desktop-mpv"]}""")
        onMain { playback.play(PlayRequest("v1", "v1", startSec = 0)) }
        until("playing") { playback.player != null }
        fxMade.single().events.subtitleTracks(fileTracks)
        Thread.sleep(300)
        assertTrue("a direct file with JavaFX offers no subtitles", onMain { playback.subtitleOptions }.isEmpty())
        assertTrue(home.asked("GET /api/videos/v1/subtitles").isEmpty())
    }

    @Test
    fun theSizeAndColourAreTakenWhenAVideoOpens() {
        settings = PlaybackValues(subtitleSize = SubtitleSize.Large, subtitleColour = SubtitleColour.Yellow)
        val p = playDirect()
        until("styled") { playback.subtitleStyle == SubtitleStyle(SubtitleSize.Large, SubtitleColour.Yellow) }
        assertEquals(SubtitleStyle(SubtitleSize.Large, SubtitleColour.Yellow), p.events.subtitleStyle)
        // changed in Profile: the open video keeps its style; the next one takes the new
        settings = PlaybackValues()
        Thread.sleep(200)
        assertEquals(SubtitleColour.Yellow, onMain { playback.subtitleStyle }.colour)
        home.answer("GET /api/videos/v2/subtitles", """{"items":[]}""")
        onMain { playback.play(PlayRequest("v2", "v2", startSec = 0)) }
        until("next styled") { playback.subtitleStyle == SubtitleStyle() }
    }

    @Test
    fun aHomesFileThePlayerCannotAddLeavesTheChoiceBeforeIt() {
        home.answer("GET /api/videos/v1/subtitles", """{"items":[{"id":7,"language":"en","format":"srt","source":"external"}]}""")
        val p = playDirect()
        until("the menu") { playback.subtitleOptions.size == 4 }
        val menu = onMain { playback.subtitleOptions }
        onMain { playback.chooseSubtitle(menu[1]) }
        onMain { playback.chooseSubtitle(menu[0]) }
        p.events.subtitleFailed(menu[0], "loading failed")
        until("back") { playback.subtitle == menu[1] }
        assertEquals("ara", playback.choice.textLanguage)
        assertTrue(logged.toString(), logged.any { it.startsWith("subtitles: en from the home could not be shown: loading failed") })
    }

    @Test
    fun theHomesAddressesAreTheSessionsInUseAfterTheWait() {
        // the file's tracks come late: the addresses are made then, for the session then playing
        home.answer("GET /api/videos/v1/subtitles", """{"items":[{"id":7,"language":"en","format":"srt","source":"external"}]}""")
        home.answer("GET /api/stream/capabilities", """{"client_profiles":["desktop-mpv"]}""")
        onMain { playback.play(PlayRequest("v1", "v1", startSec = 0)) }
        until("playing") { playback.player != null }
        onMain { playback.chooseQuality(com.hamzalabs.maktaba.core.playback.QualityOption(720, 3000)) }
        until("reopened") { made.size == 2 && playback.player === made[1] }
        made[1].events.subtitleTracks(fileTracks)
        until("the menu") { playback.subtitleOptions.size == 4 }
        val url = onMain { playback.subtitleOptions }[0].url!!
        assertTrue(url, url.endsWith("/stream/s2/subs/sidecar/en.vtt?sig=SIG2"))
    }

    @Test
    fun aReopenAsHlsOffersTheMastersSubtitlesInsteadOfTheFilesOwn() {
        home.answer("GET /api/videos/v1/subtitles", """{"items":[{"id":7,"language":"en","format":"srt","source":"external"}]}""")
        home.answer("POST /api/stream/sessions") {
            val n = opened.incrementAndGet()
            if (n == 1) TestHome.json("""{"session_id":"s1","mode":"direct","direct_url":"/stream/s1.mkv","stream_sig":"SIG1"}""")
            else TestHome.json("""{"session_id":"s$n","mode":"transcode","manifest_url":"/stream/s$n/master.m3u8","stream_sig":"SIG$n"}""")
        }
        val master = "#EXTM3U\n#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID=\"subs\",NAME=\"Arabic\",LANGUAGE=\"ar\",URI=\"subs/ar.m3u8\"\n" +
            "#EXT-X-STREAM-INF:BANDWIDTH=3000000,SUBTITLES=\"subs\"\n720p.m3u8\n"
        home.answer("GET /stream/s2/master.m3u8") {
            mockwebserver3.MockResponse.Builder().code(200).setHeader("Content-Type", "application/vnd.apple.mpegurl").body(master).build()
        }
        home.answer("GET /stream/s2/720p.m3u8") {
            mockwebserver3.MockResponse.Builder().code(200).setHeader("Content-Type", "application/vnd.apple.mpegurl")
                .body("#EXTM3U\n#EXTINF:6,\nseg0.ts\n").build()
        }
        val p = playDirect()
        until("the menu") { playback.subtitleOptions.size == 4 }
        val arabic = onMain { playback.subtitleOptions }.first { it.trackId == "1" }
        onMain { playback.chooseSubtitle(arabic) }
        // a quality cap: the home converts, and the stream is HLS
        onMain { playback.chooseQuality(com.hamzalabs.maktaba.core.playback.QualityOption(720, 3000)) }
        until("the master's menu") { playback.subtitleOptions.map { it.kind } == listOf(SubtitleOption.Kind.Stream) }
        assertEquals("ar", onMain { playback.subtitleOptions }.single().language)
        // the file's track left with the file; the person's language picks the master's rendition
        until("Arabic again") { playback.subtitle?.kind == SubtitleOption.Kind.Stream }
        assertEquals(null, made.last().events.subtitleChoice)
        assertTrue(p.subtitles.isNotEmpty())
    }
}
