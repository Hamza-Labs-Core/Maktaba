package com.hamzalabs.maktaba.desktop.mpv

import com.hamzalabs.maktaba.desktop.AudioOption
import com.hamzalabs.maktaba.desktop.Players
import com.hamzalabs.maktaba.desktop.Video
import com.hamzalabs.maktaba.desktop.VideoEvents
import com.sun.jna.Native
import com.sun.jna.Pointer
import java.awt.Canvas
import java.awt.Color
import java.io.File
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit

/**
 * A video played by libmpv (story 13.18) into a native child window: [canvas], a heavyweight
 * AWT canvas that the watch screen embeds where JavaFX's panel was (the controls stay outside
 * it, as they were). mpv starts once the canvas has its native window (its HWND or X11 window
 * id, mpv's `wid`), and stops, waiting for mpv to let go of the window, before the window goes.
 *
 * Every call returns at once: mpv's commands run on this video's own thread, mpv's events are
 * read on another and handed to [events].
 */
class MpvVideo internal constructor(
    private val lib: LibMpv,
    private val url: String,
    private val fromMs: Long,
    private val autoplay: Boolean,
    private val events: VideoEvents,
    private val fontsDir: File?,
) : Video {
    private val lock = Any()
    @Volatile private var ctx: Pointer? = null
    @Volatile private var closed = false
    private var loop: Thread? = null
    private val ops = Executors.newSingleThreadExecutor { r -> Thread(r, "mpv-commands").apply { isDaemon = true } }
    @Volatile private var audioIds: List<String> = emptyList()
    @Volatile private var lastCaption: Triple<String?, Boolean, Float>? = null

    /** The hardware decoder mpv uses now ("no": software), once the picture has started. */
    @Volatile var hwdec: String? = null
        private set

    /** The window mpv draws in. */
    val canvas: Canvas = object : Canvas() {
        init {
            background = Color.BLACK
        }

        override fun addNotify() {
            super.addNotify()
            val wid = Native.getComponentID(this)
            ops.execute { attach(wid) }
        }

        override fun removeNotify() {
            // mpv must be done with the window before it is destroyed
            close()
            super.removeNotify()
        }
    }

    private fun attach(wid: Long) {
        synchronized(lock) {
            if (closed || ctx != null) return
            val c = lib.mpv_create() ?: run {
                events.failed("libmpv could not start.")
                return
            }
            fun opt(name: String, value: String) = lib.mpv_set_option_string(c, name, value)
            opt("wid", wid.toString())
            opt("hwdec", "auto-safe")
            opt("terminal", "no")
            opt("input-default-bindings", "no")
            opt("input-vo-keyboard", "no")
            opt("osc", "no")
            opt("keep-open", "no")
            // the Player's own subtitles are drawn as captions; a file's own stay off until chosen
            opt("sid", "no")
            opt("sub-auto", "no")
            opt("volume", (events.volume * 100).toInt().toString())
            opt("mute", if (events.muted) "yes" else "no")
            opt("pause", if (autoplay) "no" else "yes")
            if (fromMs > 0) opt("start", "+" + (fromMs / 1000.0).toString())
            // captions in Maktaba's face (IBM Plex Sans Arabic), joined and right to left by libass
            if (fontsDir != null) {
                opt("osd-fonts-dir", fontsDir.absolutePath)
                opt("sub-fonts-dir", fontsDir.absolutePath)
            }
            opt("osd-font", CAPTION_FONT)
            opt("sub-font", CAPTION_FONT)
            val err = lib.mpv_initialize(c)
            if (err < 0) {
                lib.mpv_terminate_destroy(c)
                events.failed("libmpv: " + lib.mpv_error_string(err))
                return
            }
            lib.mpv_observe_property(c, 1, "time-pos", MpvConst.FORMAT_DOUBLE)
            lib.mpv_observe_property(c, 2, "pause", MpvConst.FORMAT_FLAG)
            ctx = c
            loop = Thread({ read(c) }, "mpv-events").apply {
                isDaemon = true
                start()
            }
            lib.mpv_command(c, arrayOf("loadfile", url, null))
            lastCaption?.let { (s, rtl, scale) -> overlay(c, s, rtl, scale) }
        }
    }

    private fun read(c: Pointer) {
        while (true) {
            val ev = lib.mpv_wait_event(c, -1.0)
            when (ev.getInt(0)) {
                MpvConst.EVENT_SHUTDOWN -> return
                MpvConst.EVENT_FILE_LOADED -> loaded(c)
                MpvConst.EVENT_PLAYBACK_RESTART -> if (hwdec == null) hwdec = string(c, "hwdec-current")
                MpvConst.EVENT_PROPERTY_CHANGE -> {
                    val p = ev.getPointer(16) ?: continue
                    val data = p.getPointer(16) ?: continue
                    when (p.getPointer(0)?.getString(0)) {
                        "time-pos" -> if (p.getInt(8) == MpvConst.FORMAT_DOUBLE) events.position((data.getDouble(0) * 1000).toLong())
                        "pause" -> if (p.getInt(8) == MpvConst.FORMAT_FLAG) events.playing(data.getInt(0) == 0)
                    }
                }
                MpvConst.EVENT_END_FILE -> {
                    val d = ev.getPointer(16) ?: continue
                    when (d.getInt(0)) {
                        MpvConst.END_EOF -> events.ended()
                        MpvConst.END_ERROR -> events.failed(lib.mpv_error_string(d.getInt(4)))
                    }
                }
            }
        }
    }

    /** The file is open: its length and its audio tracks, in order. */
    private fun loaded(c: Pointer) {
        val duration = string(c, "duration")?.toDoubleOrNull()?.let { (it * 1000).toLong() }
        val count = string(c, "track-list/count")?.toIntOrNull() ?: 0
        val ids = mutableListOf<String>()
        val audio = mutableListOf<AudioOption>()
        for (i in 0 until count) {
            if (string(c, "track-list/$i/type") != "audio") continue
            val id = string(c, "track-list/$i/id") ?: continue
            audio += AudioOption(
                string(c, "track-list/$i/title")?.ifBlank { null },
                string(c, "track-list/$i/lang")?.ifBlank { null },
                index = audio.size,
            )
            ids += id
        }
        audioIds = ids
        events.ready(duration, audio)
    }

    private fun string(c: Pointer, name: String): String? {
        val p = lib.mpv_get_property_string(c, name) ?: return null
        return try {
            p.getString(0, "UTF-8")
        } finally {
            lib.mpv_free(p)
        }
    }

    private fun onMpv(block: (Pointer) -> Unit) {
        if (closed) return
        ops.execute { ctx?.let { if (!closed) block(it) } }
    }

    override fun toggle() = onMpv { lib.mpv_command(it, arrayOf("cycle", "pause", null)) }

    override fun pause() = onMpv { lib.mpv_set_property_string(it, "pause", "yes") }

    override fun seek(ms: Long) = onMpv {
        lib.mpv_command(it, arrayOf("seek", (ms.coerceAtLeast(0) / 1000.0).toString(), "absolute", null))
    }

    override fun sound(volume: Float, muted: Boolean) = onMpv {
        lib.mpv_set_property_string(it, "volume", (volume * 100).toInt().toString())
        lib.mpv_set_property_string(it, "mute", if (muted) "yes" else "no")
    }

    /** A direct file's audio track [index] (in [VideoEvents.ready]'s order), switched in place. */
    override fun selectAudio(index: Int): Boolean {
        val id = audioIds.getOrNull(index) ?: return false
        onMpv { lib.mpv_set_property_string(it, "aid", id) }
        return true
    }

    override fun caption(text: String?, rtl: Boolean, scale: Float) {
        val want = Triple(text, rtl, scale)
        if (want == lastCaption) return
        lastCaption = want
        onMpv { overlay(it, text, rtl, scale) }
    }

    private fun overlay(c: Pointer, text: String?, rtl: Boolean, scale: Float) {
        if (text.isNullOrBlank()) {
            lib.mpv_command(c, arrayOf("osd-overlay", "1", "none", "", "0", "720", "0", "no", null))
            return
        }
        lib.mpv_command(c, arrayOf("osd-overlay", "1", "ass-events", captionAss(text, rtl, scale), "0", "720", "0", "no", null))
    }

    override fun dispose() = close()

    private fun close() {
        val c: Pointer?
        synchronized(lock) {
            if (closed) return
            closed = true
            c = ctx
            ctx = null
        }
        ops.shutdown()
        ops.awaitTermination(2, TimeUnit.SECONDS)
        if (c != null) {
            lib.mpv_command(c, arrayOf("quit", null))
            loop?.join(5_000)
            lib.mpv_terminate_destroy(c)
        }
    }

    companion object {
        const val CAPTION_FONT = "IBM Plex Sans Arabic"

        /**
         * A caption as one ASS event for mpv's OSD: bottom centre, white with an outline, sized
         * as JavaFX's was (a 22nd of the picture's height) times [scale]. ASS's own characters
         * in the text are escaped; a line break is ASS's.
         */
        fun captionAss(text: String, rtl: Boolean, scale: Float): String {
            val size = (720f / 22f * scale).toInt().coerceIn(14, 96)
            val escaped = text
                .replace("\\", "\\⁠")
                .replace("{", "\\{")
                .replace("\r\n", "\n")
                .replace("\n", "\\N")
            // a right-to-left caption starts with RLE so a line beginning with a digit or a
            // Latin word still reads from the right
            val body = if (rtl) "‫" + escaped else escaped
            return "{\\an2\\fs$size\\bord2\\shad1\\1c&HFFFFFF&\\3c&H000000&}$body"
        }
    }
}

/** libmpv's players (story 13.18): each video in its own [MpvVideo]. */
class MpvPlayers(private val lib: LibMpv, private val fontsDir: File?) : Players {
    override val profile: String get() = MPV_PROFILE

    override suspend fun open(url: String, fromMs: Long, autoplay: Boolean, events: VideoEvents): Video =
        MpvVideo(lib, url, fromMs, autoplay, events, fontsDir)
}

/** The streaming service's profile for libmpv (streaming/internal/capability/matrix.go). */
const val MPV_PROFILE = "desktop-mpv"
