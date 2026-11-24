package com.hamzalabs.maktaba.desktop.mpv

import com.hamzalabs.maktaba.core.playback.SubtitleColour
import com.hamzalabs.maktaba.core.playback.SubtitleSize
import com.hamzalabs.maktaba.desktop.AudioOption
import com.hamzalabs.maktaba.desktop.VideoEvents
import com.hamzalabs.maktaba.desktop.watch.FileSubtitle
import com.hamzalabs.maktaba.desktop.watch.SubtitleOption
import com.hamzalabs.maktaba.desktop.watch.SubtitleStyle
import com.sun.jna.Memory
import com.sun.jna.NativeLong
import com.sun.jna.Pointer
import com.sun.jna.ptr.PointerByReference
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.util.Collections
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.LinkedBlockingQueue
import java.util.concurrent.TimeUnit

/**
 * Story 13.20 at mpv's own calls, with a pretend libmpv (no library here; CI's MpvRealTest has
 * the real one): the subtitle options a video opens with, the file's tracks read from its
 * track-list, and the subtitles chosen: a file's track (`sid`), a home's file (`sub-add` with
 * the proxy's URL, then its id) and off (`sid=no`) (T04).
 */
class MpvVideoTest {
    /** What the pretend libmpv was told, and what it answers. */
    private class Fake {
        val options = ConcurrentHashMap<String, String>()
        val calls: MutableList<String> = Collections.synchronizedList(mutableListOf())
        val properties = ConcurrentHashMap<String, String>()
        val events = LinkedBlockingQueue<Int>()
        private val kept: MutableList<Memory> = Collections.synchronizedList(mutableListOf())

        private fun text(s: String) = Memory(s.toByteArray().size + 1L).also { it.setString(0, s, "UTF-8"); kept += it }

        private fun event(id: Int) = Memory(64).also { it.clear(); it.setInt(0, id); kept += it }

        val lib = java.lang.reflect.Proxy.newProxyInstance(LibMpv::class.java.classLoader, arrayOf(LibMpv::class.java)) { _, m, a ->
            when (m.name) {
                "mpv_create" -> Pointer(0x1000)
                "mpv_initialize", "mpv_observe_property", "mpv_render_context_render" -> 0
                "mpv_set_option_string" -> { options[a[1] as String] = a[2] as String; 0 }
                "mpv_set_property_string" -> { calls += "set ${a[1]}=${a[2]}"; 0 }
                "mpv_command" -> {
                    @Suppress("UNCHECKED_CAST")
                    val args = (a[1] as Array<String?>).filterNotNull()
                    calls += args.joinToString(" ")
                    if (args.firstOrNull() == "quit") events.put(MpvConst.EVENT_SHUTDOWN)
                    if (args.firstOrNull() == "sub-add") properties["sid"] = "9"
                    0
                }
                "mpv_get_property_string" -> properties[a[1] as String]?.let(::text)
                "mpv_wait_event" -> event(events.poll(5, TimeUnit.SECONDS) ?: MpvConst.EVENT_NONE)
                "mpv_render_context_create" -> { (a[0] as PointerByReference).value = Pointer(0x2000); 0 }
                "mpv_render_context_update" -> 0L
                "mpv_client_api_version" -> NativeLong(2L shl 16)
                "mpv_error_string" -> "error"
                else -> null
            }
        } as LibMpv
    }

    private val fake = Fake()
    private val tracks: MutableList<List<FileSubtitle>> = Collections.synchronizedList(mutableListOf())
    private var video: MpvVideo? = null

    private fun events(style: SubtitleStyle = SubtitleStyle(), chosen: SubtitleOption? = null) = object : VideoEvents {
        override val volume = 1f
        override val muted = false
        override val subtitleStyle = style
        override val subtitleChoice = chosen
        override fun ready(durationMs: Long?, audio: List<AudioOption>) {}
        override fun subtitleTracks(tracks: List<FileSubtitle>) {
            this@MpvVideoTest.tracks += tracks
        }
        override fun position(ms: Long) {}
        override fun playing(on: Boolean) {}
        override fun failed(why: String) {}
        override fun ended() {}
    }

    private fun open(e: VideoEvents): MpvVideo = MpvVideo(fake.lib, "http://127.0.0.1:1/x/stream/direct/v1", 0, true, e, null).also { video = it }

    private fun until(what: String, ok: () -> Boolean) {
        val end = System.currentTimeMillis() + 10_000
        while (!ok()) {
            if (System.currentTimeMillis() > end) throw AssertionError("never: $what; calls ${fake.calls}")
            Thread.sleep(10)
        }
    }

    @After
    fun tearDown() {
        video?.dispose()
    }

    /** A track-list: an audio track, an Arabic SRT, a forced English SRT, a PGS, and a file added before. */
    private fun trackList() {
        val list = listOf(
            mapOf("type" to "audio", "id" to "1", "lang" to "ara"),
            mapOf("type" to "sub", "id" to "1", "lang" to "ara", "codec" to "subrip", "forced" to "no", "external" to "no"),
            mapOf("type" to "sub", "id" to "2", "lang" to "eng", "title" to "Signs", "codec" to "subrip", "forced" to "yes", "external" to "no"),
            mapOf("type" to "sub", "id" to "3", "codec" to "hdmv_pgs_subtitle", "forced" to "no", "external" to "no"),
            mapOf("type" to "sub", "id" to "4", "lang" to "en", "codec" to "webvtt", "forced" to "no", "external" to "yes"),
        )
        fake.properties["track-list/count"] = list.size.toString()
        list.forEachIndexed { i, t -> t.forEach { (k, v) -> fake.properties["track-list/$i/$k"] = v } }
    }

    @Test
    fun theSubtitlesOptionsFollowTheSettings() {
        open(events(SubtitleStyle(SubtitleSize.Large, SubtitleColour.Yellow)))
        until("set up") { fake.calls.any { it.startsWith("loadfile") } }
        assertEquals("1.3", fake.options["sub-scale"])
        assertEquals("#FFE14D", fake.options["sub-color"])
        assertEquals("#000000", fake.options["sub-border-color"])
        assertEquals("2", fake.options["sub-border-size"])
        assertEquals("scale", fake.options["sub-ass-override"])
        assertEquals("IBM Plex Sans Arabic", fake.options["sub-font"])
        assertEquals("yes", fake.options["embeddedfonts"])
        // until the Player chooses, only a forced track in the audio's language
        assertEquals("auto", fake.options["sid"])
        assertEquals("no", fake.options["subs-fallback"])
        assertEquals("yes", fake.options["subs-fallback-forced"])
        assertEquals("forced", fake.options["subs-with-matching-audio"])
    }

    @Test
    fun theFilesOwnTracksAreReadWhenItOpens() {
        trackList()
        open(events())
        until("set up") { fake.calls.any { it.startsWith("loadfile") } }
        fake.events.put(MpvConst.EVENT_FILE_LOADED)
        until("tracks read") { tracks.isNotEmpty() }
        assertEquals(
            listOf(
                FileSubtitle("1", null, "ara", "subrip"),
                FileSubtitle("2", "Signs", "eng", "subrip", forced = true),
                FileSubtitle("3", null, null, "hdmv_pgs_subtitle"),
            ),
            tracks.single(),
        )
        assertTrue(tracks.single()[2].picture)
    }

    @Test
    fun t04_aFilesTrackAHomesFileAndOff() {
        val v = open(events())
        until("set up") { fake.calls.any { it.startsWith("loadfile") } }
        assertTrue(v.selectSubtitle(SubtitleOption(null, "ar", SubtitleOption.Kind.File, trackId = "1")))
        until("sid 1") { "set sid=1" in fake.calls }
        val home = SubtitleOption(null, "en", SubtitleOption.Kind.Home, url = "http://127.0.0.1:1/x/stream/s1/subs/sidecar/en.vtt?sig=abc")
        assertTrue(v.selectSubtitle(home))
        until("added") { fake.calls.any { it.startsWith("sub-add") } }
        assertEquals("sub-add http://127.0.0.1:1/x/stream/s1/subs/sidecar/en.vtt?sig=abc select en en", fake.calls.first { it.startsWith("sub-add") })
        assertTrue(v.selectSubtitle(null))
        until("off") { fake.calls.last() == "set sid=no" }
        // the home's file again: by the id mpv gave it, not added twice
        v.selectSubtitle(home)
        until("by its id") { fake.calls.last() == "set sid=9" }
        assertEquals(1, fake.calls.count { it.startsWith("sub-add") })
        // an HLS rendition's cues are the Player's to draw
        assertFalse(v.selectSubtitle(SubtitleOption("Arabic", "ar", SubtitleOption.Kind.Stream)))
    }

    @Test
    fun aReopenedVideoShowsTheSubtitlesChosenBefore() {
        trackList()
        open(events(chosen = SubtitleOption(null, "ar", SubtitleOption.Kind.File, trackId = "3", picture = true)))
        until("set up") { fake.calls.any { it.startsWith("loadfile") } }
        fake.events.put(MpvConst.EVENT_FILE_LOADED)
        until("shown again") { "set sid=3" in fake.calls }
        // a picture track is drawn as authored: no scale of the Player's
        assertTrue(fake.calls.toString(), "set sub-scale=1.0" in fake.calls)
    }

    @Test
    fun aTextTrackTakesTheSizeAgainAfterAPicture() {
        val v = open(events(SubtitleStyle(SubtitleSize.Large)))
        until("set up") { fake.calls.any { it.startsWith("loadfile") } }
        v.selectSubtitle(SubtitleOption(null, null, SubtitleOption.Kind.File, trackId = "3", picture = true))
        v.selectSubtitle(SubtitleOption(null, "ar", SubtitleOption.Kind.File, trackId = "1"))
        until("text") { "set sid=1" in fake.calls }
        assertEquals(listOf("set sub-scale=1.0", "set sid=3", "set sub-scale=1.3", "set sid=1"), fake.calls.filter { it.startsWith("set ") })
    }

    @Test
    fun theOsdCaptionIsDrawnInTheColourGiven() {
        val v = open(events())
        until("set up") { fake.calls.any { it.startsWith("loadfile") } }
        v.caption("Hello", rtl = false, scale = 1.3f, colour = SubtitleColour.Yellow)
        until("drawn") { fake.calls.any { it.startsWith("osd-overlay 1 ass-events") } }
        val c = fake.calls.first { it.startsWith("osd-overlay 1 ass-events") }
        assertTrue(c, c.contains("\\1c&H4DE1FF&") && c.contains("\\fs42"))
    }
}
