package com.hamzalabs.maktaba.desktop.mpv

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asComposeImageBitmap
import com.hamzalabs.maktaba.desktop.AudioOption
import com.hamzalabs.maktaba.desktop.Players
import com.hamzalabs.maktaba.desktop.Video
import com.hamzalabs.maktaba.desktop.VideoEvents
import com.hamzalabs.maktaba.core.playback.SubtitleColour
import com.hamzalabs.maktaba.desktop.watch.FileSubtitle
import com.hamzalabs.maktaba.desktop.watch.SubtitleOption
import com.sun.jna.Memory
import com.sun.jna.Pointer
import com.sun.jna.ptr.PointerByReference
import org.jetbrains.skia.Bitmap
import org.jetbrains.skia.ColorAlphaType
import org.jetbrains.skia.ColorType
import org.jetbrains.skia.ImageInfo
import java.io.File
import java.util.concurrent.Executors
import java.util.concurrent.Semaphore
import java.util.concurrent.TimeUnit

/**
 * A video played by libmpv (story 13.18). mpv decodes (in hardware where the machine can, its
 * frames copied back) and draws each frame, the Player's caption included, with its software
 * renderer into memory the Player owns, at the size the watch screen shows it ([resize]);
 * Compose draws that frame ([frame]). The surface decision: an LGPL libmpv has no X11 output,
 * so it cannot draw into a window of the Player's on Linux, and Compose does not share its
 * OpenGL context; the software renderer works the same on Windows and Linux, at the screen's
 * size rather than the file's.
 *
 * Every call returns at once: mpv's commands run on this video's own thread, its events are
 * read on another, its frames drawn on a third.
 */
class MpvVideo internal constructor(
    private val lib: LibMpv,
    private val url: String,
    private val fromMs: Long,
    private val autoplay: Boolean,
    private val events: VideoEvents,
    private val fontsDir: File?,
    /** More of mpv's options, after the Player's (a test's log file). */
    private val extra: Map<String, String> = emptyMap(),
) : Video {
    private val lock = Any()
    @Volatile private var ctx: Pointer? = null
    @Volatile private var render: Pointer? = null
    @Volatile private var closed = false
    private var loop: Thread? = null
    private var drawer: Thread? = null
    private val wake = Semaphore(0)
    private val ops = Executors.newSingleThreadExecutor { r -> Thread(r, "mpv-commands").apply { isDaemon = true } }
    @Volatile private var audioIds: List<String> = emptyList()
    @Volatile private var lastCaption: Triple<String?, Boolean, Pair<Float, SubtitleColour>>? = null
    /** The home's subtitles added (`sub-add`), by their URL: mpv's track id. */
    private val added = java.util.concurrent.ConcurrentHashMap<String, String>()
    /** The `sub-add`s mpv has not answered yet, by their reply number. */
    private val adding = java.util.concurrent.ConcurrentHashMap<Long, SubtitleOption>()
    private val replies = java.util.concurrent.atomic.AtomicLong(1000)
    /** The subtitles asked for last (null: none). */
    @Volatile private var wanted: SubtitleOption? = null
    @Volatile private var size: Pair<Int, Int> = 0 to 0
    // mpv keeps the callback's address: held here for the handle's life
    private val onUpdate = LibMpv.UpdateCallback { wake.release() }

    /** The frame to draw now (null: none yet), at the size last given to [resize]. */
    var frame by mutableStateOf<ImageBitmap?>(null)
        private set

    /** The picture has started (mpv's first playback-restart): a failure after it is not the file's. */
    @Volatile override var started = false
        private set

    /** The hardware decoder mpv uses now ("no": software), once the picture has started. */
    @Volatile var hwdec: String? = null
        private set

    init {
        ops.execute(::start)
    }

    /** The picture's size on screen, in pixels: frames are drawn at it. */
    fun resize(width: Int, height: Int) {
        if (width <= 0 || height <= 0 || size == (width to height)) return
        size = width to height
        wake.release()
    }

    private fun start() {
        synchronized(lock) {
            if (closed) return
            val c = lib.mpv_create() ?: run {
                events.failed("libmpv could not start.")
                return
            }
            fun opt(name: String, value: String) = lib.mpv_set_option_string(c, name, value)
            opt("vo", "libmpv")
            // a hardware decoder whose frames come back to memory, where the machine has one
            opt("hwdec", "auto-copy-safe")
            opt("terminal", "no")
            opt("input-default-bindings", "no")
            opt("osc", "no")
            // the end stays on screen, so a seek back after it plays again; eof-reached says it ended
            opt("keep-open", "yes")
            // story 13.20: the Player chooses the subtitles (selectSubtitle); until it does, mpv
            // shows only a forced track, one in the audio's language, as players do
            opt("sid", "auto")
            opt("slang", "")
            opt("subs-fallback", "no")
            opt("subs-fallback-forced", "yes")
            opt("subs-with-matching-audio", "forced")
            opt("sub-auto", "no")
            // text subtitles without their own styling in the Player's size and colour; ASS
            // keeps its styles but takes the size; picture subtitles are drawn as authored
            val style = events.subtitleStyle
            opt("sub-scale", style.scale.toString())
            opt("sub-color", style.colour.rgb)
            opt("sub-outline-color", "#000000")
            opt("sub-outline-size", "2")
            opt("sub-ass-override", "scale")
            // ASS's own fonts; a raw file's codepage guessed (a sidecar comes as UTF-8)
            opt("embeddedfonts", "yes")
            opt("sub-codepage", "auto")
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
            for ((k, v) in extra) opt(k, v)
            val err = lib.mpv_initialize(c)
            if (err < 0) {
                lib.mpv_terminate_destroy(c)
                events.failed("libmpv: " + lib.mpv_error_string(err))
                return
            }
            val sw = Memory(4).apply { setString(0, "sw") }
            val params = params(MpvConst.RENDER_PARAM_API_TYPE to sw)
            val made = PointerByReference()
            val rerr = lib.mpv_render_context_create(made, c, params)
            if (rerr < 0) {
                lib.mpv_terminate_destroy(c)
                events.failed("libmpv's renderer: " + lib.mpv_error_string(rerr))
                return
            }
            val r = made.value
            lib.mpv_render_context_set_update_callback(r, onUpdate, null)
            lib.mpv_observe_property(c, 1, "time-pos", MpvConst.FORMAT_DOUBLE)
            lib.mpv_observe_property(c, 2, "pause", MpvConst.FORMAT_FLAG)
            lib.mpv_observe_property(c, 3, "eof-reached", MpvConst.FORMAT_FLAG)
            ctx = c
            render = r
            loop = Thread({ read(c) }, "mpv-events").apply {
                isDaemon = true
                start()
            }
            drawer = Thread({ draw(r) }, "mpv-frames").apply {
                isDaemon = true
                start()
            }
            lib.mpv_command(c, arrayOf("loadfile", url, null))
            lastCaption?.let { (s, rtl, look) -> overlay(c, s, rtl, look.first, look.second) }
        }
    }

    /**
     * Draws a frame each time mpv has one, or the screen's size changed, straight into one of
     * three bitmaps in turn, and shows it as it is (no copy): the one being drawn into was shown
     * two frames ago, which Compose has drawn over since.
     */
    private fun draw(r: Pointer) {
        val pool = arrayOfNulls<Bitmap>(3)
        val addr = LongArray(3)
        var next = 0
        var drawn = 0 to 0
        val sizeArg = Memory(8)
        val stride = Memory(8)
        val format = Memory(5).apply { setString(0, "bgr0") }
        while (!closed) {
            wake.tryAcquire(250, TimeUnit.MILLISECONDS)
            if (closed) break
            val flags = lib.mpv_render_context_update(r)
            val (w, h) = size
            if (w <= 0 || h <= 0) continue
            if (flags and MpvConst.RENDER_UPDATE_FRAME == 0L && drawn == (w to h)) continue
            val b = pool[next]?.takeIf { it.width == w && it.height == h } ?: Bitmap().also {
                // a size's bitmaps are left to the collector: the screen may still show one
                it.allocPixels(ImageInfo(w, h, ColorType.BGRA_8888, ColorAlphaType.OPAQUE))
                pool[next] = it
                addr[next] = it.peekPixels()?.use { px -> px.addr } ?: 0L
            }
            val at = addr[next]
            if (at == 0L) continue
            sizeArg.setInt(0, w)
            sizeArg.setInt(4, h)
            stride.setLong(0, b.rowBytes.toLong())
            val p = params(
                MpvConst.RENDER_PARAM_SW_SIZE to sizeArg,
                MpvConst.RENDER_PARAM_SW_FORMAT to format,
                MpvConst.RENDER_PARAM_SW_STRIDE to stride,
                MpvConst.RENDER_PARAM_SW_POINTER to Pointer(at),
            )
            if (lib.mpv_render_context_render(r, p) < 0) continue
            b.notifyPixelsChanged()
            drawn = w to h
            frame = b.asComposeImageBitmap()
            next = (next + 1) % pool.size
        }
    }

    private fun read(c: Pointer) {
        while (true) {
            val ev = lib.mpv_wait_event(c, -1.0)
            when (ev.getInt(0)) {
                MpvConst.EVENT_SHUTDOWN -> return
                MpvConst.EVENT_FILE_LOADED -> loaded(c)
                // mpv_event: error at 4, reply_userdata at 8
                MpvConst.EVENT_COMMAND_REPLY -> addedReply(ev.getLong(8), ev.getInt(4))
                MpvConst.EVENT_PLAYBACK_RESTART -> {
                    started = true
                    if (hwdec == null) hwdec = string(c, "hwdec-current")?.also { Engine.hwdec = it }
                }
                MpvConst.EVENT_PROPERTY_CHANGE -> {
                    val p = ev.getPointer(16) ?: continue
                    val data = p.getPointer(16) ?: continue
                    when (p.getPointer(0)?.getString(0)) {
                        "time-pos" -> if (p.getInt(8) == MpvConst.FORMAT_DOUBLE) events.position((data.getDouble(0) * 1000).toLong())
                        "pause" -> if (p.getInt(8) == MpvConst.FORMAT_FLAG) events.playing(data.getInt(0) == 0)
                        "eof-reached" -> if (p.getInt(8) == MpvConst.FORMAT_FLAG && data.getInt(0) != 0) events.ended()
                    }
                }
                MpvConst.EVENT_END_FILE -> {
                    val d = ev.getPointer(16) ?: continue
                    if (d.getInt(0) == MpvConst.END_ERROR) events.failed(lib.mpv_error_string(d.getInt(4)))
                }
            }
        }
    }

    /** The file is open: its length, its audio tracks and its own subtitle tracks, in order. */
    private fun loaded(c: Pointer) {
        val duration = string(c, "duration")?.toDoubleOrNull()?.let { (it * 1000).toLong() }
        val count = string(c, "track-list/count")?.toIntOrNull() ?: 0
        val ids = mutableListOf<String>()
        val audio = mutableListOf<AudioOption>()
        val subs = mutableListOf<FileSubtitle>()
        var shownByMpv: FileSubtitle? = null
        for (i in 0 until count) {
            val type = string(c, "track-list/$i/type")
            if (type != "audio" && type != "sub") continue
            val id = string(c, "track-list/$i/id") ?: continue
            val title = string(c, "track-list/$i/title")?.ifBlank { null }
            val lang = string(c, "track-list/$i/lang")?.ifBlank { null }
            if (type == "audio") {
                audio += AudioOption(title, lang, index = audio.size)
                ids += id
            } else if (string(c, "track-list/$i/external") != "yes") {
                // the file's own (a home's file added before is not one of them)
                val t = FileSubtitle(id, title, lang, string(c, "track-list/$i/codec"), forced = string(c, "track-list/$i/forced") == "yes")
                subs += t
                if (string(c, "track-list/$i/selected") == "yes") shownByMpv = t
            }
        }
        // a forced picture track mpv picked itself is drawn as authored too
        if (events.subtitleChoice == null && shownByMpv?.picture == true) onMpv { lib.mpv_set_property_string(it, "sub-scale", "1.0") }
        audioIds = ids
        // the track chosen before this player (a reopen keeps it)
        events.audioIndex.takeIf { it > 0 }?.let { ids.getOrNull(it) }?.let { lib.mpv_set_property_string(c, "aid", it) }
        events.ready(duration, audio)
        events.subtitleTracks(subs)
        // the subtitles chosen before this player (a reopen keeps them)
        events.subtitleChoice?.let { o -> onMpv { showSubtitle(it, o) } }
    }

    /** Track [id] on screen ("no": none), a picture track as authored: no scale of the Player's (mpv may scale pictures too). */
    private fun show(c: Pointer, id: String, picture: Boolean) {
        lib.mpv_set_property_string(c, "sub-scale", if (picture) "1.0" else events.subtitleStyle.scale.toString())
        lib.mpv_set_property_string(c, "sid", id)
    }

    /**
     * [o] drawn: a file's track by its id; a home's file added once, then by its id; none:
     * `sid=no`. A home's file is added without waiting (`sub-add` on mpv's own time, so a slow
     * read never holds a pause or a seek back): the track before it stays until mpv has it
     * ([added]), and one mpv could not add is reported, the choice before it kept.
     */
    private fun showSubtitle(c: Pointer, o: SubtitleOption?) {
        wanted = o
        when (o?.kind) {
            SubtitleOption.Kind.File -> o.trackId?.let { show(c, it, o.picture) }
            SubtitleOption.Kind.Home -> {
                val url = o.url ?: return
                val known = added[url]
                if (known != null) {
                    show(c, known, picture = false)
                    return
                }
                val reply = replies.incrementAndGet()
                adding[reply] = o
                // "auto": mpv does not show it itself; it is shown once added, if still wanted
                val err = lib.mpv_command_async(c, reply, arrayOf("sub-add", url, "auto", o.name ?: o.language ?: "", o.language ?: "", null))
                if (err < 0) {
                    adding.remove(reply)
                    events.subtitleFailed(o, lib.mpv_error_string(err))
                }
            }
            else -> show(c, "no", picture = false)
        }
    }

    /** mpv answered `sub-add` [reply] with [err] (below 0: not added). */
    private fun addedReply(reply: Long, err: Int) {
        val o = adding.remove(reply) ?: return
        if (err < 0) {
            events.subtitleFailed(o, lib.mpv_error_string(err))
            return
        }
        onMpv { c ->
            val url = o.url ?: return@onMpv
            val id = externalTrack(c, url)
            if (id == null) {
                events.subtitleFailed(o, "mpv lists no track for it")
                return@onMpv
            }
            added[url] = id
            if (wanted == o) show(c, id, picture = false)
        }
    }

    /** The id of the subtitle track mpv added from [url] (its `external-filename`). */
    private fun externalTrack(c: Pointer, url: String): String? {
        val count = string(c, "track-list/count")?.toIntOrNull() ?: 0
        for (i in 0 until count) {
            if (string(c, "track-list/$i/type") != "sub") continue
            if (string(c, "track-list/$i/external-filename") == url) return string(c, "track-list/$i/id")
        }
        return null
    }

    /** One of mpv's properties now, as a string (a test's look at what mpv is doing). */
    internal fun property(name: String): String? = ctx?.let { string(it, name) }

    /** Subtitles drawn by mpv (story 13.20): a file's own track, a home's file, or none. An HLS rendition is not one. */
    override fun selectSubtitle(option: SubtitleOption?): Boolean {
        if (option?.kind == SubtitleOption.Kind.Stream) return false
        onMpv { showSubtitle(it, option) }
        return true
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
        try {
            ops.execute { ctx?.let { if (!closed) block(it) } }
        } catch (_: java.util.concurrent.RejectedExecutionException) {
            // stopped meanwhile
        }
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

    override fun caption(text: String?, rtl: Boolean, scale: Float, colour: SubtitleColour) {
        val want = Triple(text, rtl, scale to colour)
        if (want == lastCaption) return
        lastCaption = want
        onMpv { overlay(it, text, rtl, scale, colour) }
    }

    private fun overlay(c: Pointer, text: String?, rtl: Boolean, scale: Float, colour: SubtitleColour) {
        if (text.isNullOrBlank()) {
            lib.mpv_command(c, arrayOf("osd-overlay", "1", "none", "", "0", "720", "0", "no", null))
            return
        }
        lib.mpv_command(c, arrayOf("osd-overlay", "1", "ass-events", captionAss(text, rtl, scale, colour), "0", "720", "0", "no", null))
    }

    /**
     * Stops it, on its own thread: the caller (the UI's) never waits. mpv is set up on [ops],
     * so its end waits for that, then for the frames and the events to stop, without a limit,
     * before the renderer and then the handle are freed (render.h's order).
     */
    override fun dispose() {
        if (closed) return
        closed = true
        Thread({
            ops.shutdown()
            ops.awaitTermination(Long.MAX_VALUE, TimeUnit.MILLISECONDS)
            val c: Pointer?
            val r: Pointer?
            synchronized(lock) {
                c = ctx
                r = render
                ctx = null
                render = null
            }
            wake.release()
            drawer?.join()
            if (r != null) lib.mpv_render_context_free(r)
            if (c != null) {
                lib.mpv_command(c, arrayOf("quit", null))
                loop?.join()
                lib.mpv_terminate_destroy(c)
            }
        }, "mpv-end").apply {
            isDaemon = true
            start()
        }
    }

    companion object {
        const val CAPTION_FONT = "IBM Plex Sans Arabic"

        /** mpv_render_param[]: each (type, data), then the end. */
        private fun params(vararg p: Pair<Int, Pointer>): Memory {
            val m = Memory(16L * (p.size + 1))
            m.clear()
            p.forEachIndexed { i, (type, data) ->
                m.setInt(16L * i, type)
                m.setPointer(16L * i + 8, data)
            }
            m.setInt(16L * p.size, MpvConst.RENDER_PARAM_INVALID)
            return m
        }

        /**
         * A caption as one ASS event for mpv's OSD: bottom centre, in [colour] (white or
         * yellow) with a black outline, sized as JavaFX's was (a 22nd of the picture's height)
         * times [scale]. ASS's own characters in the text are escaped; a line break is ASS's.
         */
        fun captionAss(text: String, rtl: Boolean, scale: Float, colour: SubtitleColour = SubtitleColour.White): String {
            val size = (720f / 22f * scale).toInt().coerceIn(14, 96)
            val escaped = text
                .replace("\\", "\\⁠")
                .replace("{", "\\{")
                .replace("\r\n", "\n")
                .replace("\n", "\\N")
            // a right-to-left caption starts with RLE so a line beginning with a digit or a
            // Latin word still reads from the right
            val body = if (rtl) "‫" + escaped else escaped
            return "{\\an2\\fs$size\\bord2\\shad1\\1c&H${assColour(colour.rgb)}&\\3c&H000000&}$body"
        }

        /** `#RRGGBB` as ASS writes a colour: `BBGGRR`. */
        fun assColour(rgb: String): String {
            val h = rgb.removePrefix("#").uppercase()
            return h.substring(4, 6) + h.substring(2, 4) + h.substring(0, 2)
        }
    }
}

/** libmpv's players (story 13.18): each video in its own [MpvVideo]. */
class MpvPlayers(
    private val lib: LibMpv,
    private val fontsDir: File?,
    /** More of mpv's options for each video, asked at each open (a test's log file). */
    private val extra: () -> Map<String, String> = { emptyMap() },
) : Players {
    override val profile: String get() = MPV_PROFILE

    /** mpv draws a file's own subtitle tracks and the home's files itself (story 13.20). */
    override val drawsSubtitles: Boolean get() = true

    override suspend fun open(url: String, fromMs: Long, autoplay: Boolean, events: VideoEvents): Video =
        MpvVideo(lib, url, fromMs, autoplay, events, fontsDir, extra())
}

/** The streaming service's profile for libmpv (streaming/internal/capability/matrix.go). */
const val MPV_PROFILE = "desktop-mpv"
