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
    /** Called once when it is disposed (its players count the videos open: story 13.19). */
    private val onDisposed: () -> Unit = {},
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
    @Volatile private var lastCaption: Triple<String?, Boolean, Float>? = null
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
            // the Player's own subtitles are drawn as captions; a file's own stay off
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
            lastCaption?.let { (s, rtl, scale) -> overlay(c, s, rtl, scale) }
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
        // the track chosen before this player (a reopen keeps it)
        events.audioIndex.takeIf { it > 0 }?.let { ids.getOrNull(it) }?.let { lib.mpv_set_property_string(c, "aid", it) }
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

    /**
     * Stops it, on its own thread: the caller (the UI's) never waits. mpv is set up on [ops],
     * so its end waits for that, then for the frames and the events to stop, without a limit,
     * before the renderer and then the handle are freed (render.h's order).
     */
    override fun dispose() {
        if (closed) return
        closed = true
        onDisposed()
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

/**
 * libmpv's players (story 13.18): each video in its own [MpvVideo]. Story 13.19: what this
 * machine plays ([report], the probe's) goes with each session as `device_caps`; the videos
 * open are counted, so the probe waits while one plays ([playing]) and the audio device's
 * channels are measured again once the last one closes ([closed]).
 */
class MpvPlayers(
    private val lib: LibMpv,
    private val fontsDir: File?,
    private val report: DeviceReport? = null,
    private val closed: () -> Unit = {},
) : Players {
    private val open = java.util.concurrent.atomic.AtomicInteger()

    override val profile: String get() = MPV_PROFILE

    /** A video is open (a watch screen): the probe waits between its clips meanwhile. */
    val playing: Boolean get() = open.get() > 0

    override fun deviceCaps(): kotlinx.serialization.json.JsonObject? = report?.json()

    override suspend fun open(url: String, fromMs: Long, autoplay: Boolean, events: VideoEvents): Video {
        open.incrementAndGet()
        return MpvVideo(lib, url, fromMs, autoplay, events, fontsDir) {
            if (open.decrementAndGet() == 0) closed()
        }
    }
}

/** The streaming service's profile for libmpv (streaming/internal/capability/matrix.go). */
const val MPV_PROFILE = "desktop-mpv"
