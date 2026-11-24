package com.hamzalabs.maktaba.desktop.mpv

import com.hamzalabs.maktaba.core.playback.DeviceCaps
import com.sun.jna.Pointer
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import java.io.File
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicBoolean

/**
 * One clip the probe plays (story 13.19): [codec] (the profile's name) at [bitDepth], the file
 * in `resources/probe/` (probe-media.sh, outside libmpv/ so the libmpv build cache keeps its key), [height] its picture's (2160, MPEG-2's 1080).
 */
data class ProbeClip(val codec: String, val bitDepth: Int, val file: String, val height: Int = 2160)

/**
 * What the probe found for one clip. [hwdec] is mpv's `hwdec-current` on its first
 * `playback-restart` with `hwdec=auto-copy-safe` ("no": software; null: it did not decode);
 * [softwareFps] the frames per second software decoded it at, scaled to 2160p (null when a
 * hardware decoder took it).
 */
@Serializable
data class ClipResult(
    val codec: String,
    @SerialName("bit_depth") val bitDepth: Int,
    val decoded: Boolean,
    val hwdec: String? = null,
    @SerialName("software_fps") val softwareFps: Double? = null,
) {
    /** A hardware decoder took it: anything but "no". */
    val hardware: Boolean get() = decoded && hwdec != null && hwdec != "no"
}

/** The probe's answers on this machine, kept in `probe.json` under the [key] they were measured with. */
@Serializable
data class ProbeResult(
    val key: String,
    val clips: List<ClipResult>,
    @SerialName("took_ms") val tookMs: Long,
    /** Every clip was played within the probe's bound (an incomplete probe runs again at the next start). */
    val complete: Boolean = true,
)

/** One play of a clip by libmpv: the decoder it used ("no": software), and how many frames in how long. */
data class Played(val hwdec: String, val frames: Int, val nanos: Long)

/** The libmpv side of the probe (a test gives a fake). */
interface ProbeDecoder {
    /** libmpv's and its FFmpeg's versions, for the cache key. */
    val version: String

    /** Plays [clip] once with [hwdec] ("auto-copy-safe", "no"); null: it did not decode within [timeoutMs]. */
    fun play(clip: File, hwdec: String, timeoutMs: Long): Played?

    /** The default audio output's channel count for [clip] (a silent 7.1 clip); null: no device opened. */
    fun channels(clip: File, timeoutMs: Long): Int?

    fun close() {}
}

/**
 * What the probe's results are cached under: the GPUs (each adapter's name and its driver's
 * version), the CPU and libmpv's version. A key that changed (another GPU, a new driver, a new
 * libmpv in an update) probes again.
 */
data class ProbeKey(val gpus: List<String>, val cpu: String, val libmpv: String) {
    override fun toString(): String = "gpu=" + gpus.ifEmpty { listOf("none") }.joinToString(" + ") + "; cpu=$cpu; libmpv=$libmpv"

    companion object {
        /** This machine's key, read from the system (never throws: what cannot be read is "unknown"). */
        fun of(libmpv: String, os: String = System.getProperty("os.name").orEmpty()): ProbeKey = when {
            os.startsWith("Windows") -> ProbeKey(runCatching { windowsGpus() }.getOrDefault(emptyList()), windowsCpu(), libmpv)
            os.startsWith("Linux") -> ProbeKey(runCatching { linuxGpus(File("/sys/class/drm")) }.getOrDefault(emptyList()), linuxCpu(), libmpv)
            else -> ProbeKey(emptyList(), cpuFallback(), libmpv)
        }

        private fun cpuFallback() = System.getProperty("os.arch") + " x" + Runtime.getRuntime().availableProcessors()

        /**
         * Linux: each DRM card's PCI vendor and device ids (`/sys/class/drm/cardN/device`), its
         * kernel driver and that driver's version (the module's own, else the kernel's: an
         * in-tree driver changes with the kernel).
         */
        fun linuxGpus(drm: File, kernel: String? = runCatching { File("/proc/sys/kernel/osrelease").readText().trim() }.getOrNull()): List<String> =
            (drm.listFiles() ?: emptyArray()).filter { Regex("card\\d+").matches(it.name) }.sortedBy { it.name }.mapNotNull { card ->
                val dev = File(card, "device")
                val vendor = runCatching { File(dev, "vendor").readText().trim() }.getOrNull() ?: return@mapNotNull null
                val device = runCatching { File(dev, "device").readText().trim() }.getOrDefault("?")
                val driver = runCatching { File(dev, "driver").canonicalFile.name }.getOrDefault("?")
                val version = runCatching { File(drm.parentFile?.parentFile, "module/$driver/version").readText().trim() }.getOrNull()
                    ?: kernel ?: "?"
                "$vendor:$device $driver $version"
            }

        private fun linuxCpu(): String {
            val info = runCatching { File("/proc/cpuinfo").readLines() }.getOrDefault(emptyList())
            val model = info.firstOrNull { it.startsWith("model name") }?.substringAfter(':')?.trim()
                ?: info.firstOrNull { it.startsWith("CPU part") }?.substringAfter(':')?.trim()?.let { "CPU part $it" }
            return (model ?: System.getProperty("os.arch")) + " x" + Runtime.getRuntime().availableProcessors()
        }

        /** The display adapters' class key: each adapter's DriverDesc and DriverVersion (what DXGI's adapter description and the display driver's version say). */
        private const val DISPLAY_CLASS = "SYSTEM\\CurrentControlSet\\Control\\Class\\{4d36e968-e325-11ce-bfc1-08002be10318}"

        private fun windowsGpus(): List<String> {
            val hk = com.sun.jna.platform.win32.WinReg.HKEY_LOCAL_MACHINE
            return com.sun.jna.platform.win32.Advapi32Util.registryGetKeys(hk, DISPLAY_CLASS).filter { Regex("\\d{4}").matches(it) }.sorted().mapNotNull { sub ->
                val key = "$DISPLAY_CLASS\\$sub"
                runCatching {
                    val name = com.sun.jna.platform.win32.Advapi32Util.registryGetStringValue(hk, key, "DriverDesc")
                    val version = com.sun.jna.platform.win32.Advapi32Util.registryGetStringValue(hk, key, "DriverVersion")
                    "$name $version"
                }.getOrNull()
            }
        }

        private fun windowsCpu(): String = runCatching {
            com.sun.jna.platform.win32.Advapi32Util.registryGetStringValue(
                com.sun.jna.platform.win32.WinReg.HKEY_LOCAL_MACHINE,
                "HARDWARE\\DESCRIPTION\\System\\CentralProcessor\\0", "ProcessorNameString",
            ).trim() + " x" + Runtime.getRuntime().availableProcessors()
        }.getOrElse { System.getenv("PROCESSOR_IDENTIFIER") ?: cpuFallback() }
    }
}

/** `probe.json`: the last probe's results and the key they were measured under. */
class ProbeCache(private val file: File) {
    private val json = Json { ignoreUnknownKeys = true; encodeDefaults = true }

    fun read(): ProbeResult? = runCatching { json.decodeFromString(ProbeResult.serializer(), file.readText()) }.getOrNull()

    fun write(r: ProbeResult) {
        runCatching {
            file.parentFile?.mkdirs()
            val tmp = File(file.parentFile, file.name + ".tmp")
            tmp.writeText(json.encodeToString(ProbeResult.serializer(), r))
            if (!tmp.renameTo(file)) {
                file.delete()
                tmp.renameTo(file)
            }
        }
    }
}

/**
 * The decoder probe (story 13.19), run at start off the UI thread on its own libmpv handle,
 * never a watch screen's. Each clip is played once with `hwdec=auto-copy-safe`: its
 * `hwdec-current` says whether a hardware decoder took it; one it did not is decoded in
 * software, and its speed (frames per second, scaled to 2160p by pixel count) gives the tallest
 * picture it decodes smoothly ([DesktopCaps.heightFor]). The results are cached in [cache]
 * under the [key]: the same key reads them back, another probes again. The probe is bounded at
 * [boundMs] of decoding (what it has then is kept, the rest left out) and waits between clips
 * while [busy] (a watch screen is open).
 */
class Probe(
    private val decoder: ProbeDecoder,
    private val clipsDir: File,
    private val cache: ProbeCache,
    private val key: String,
    private val busy: () -> Boolean = { false },
    private val boundMs: Long = BOUND_MS,
    private val nanoTime: () -> Long = System::nanoTime,
) {
    /** The cached results when their key is this machine's, else a new probe's (cached). */
    fun run(): ProbeResult {
        cache.read()?.takeIf { it.key == key && it.complete }?.let { return it }
        val r = measure()
        cache.write(r)
        return r
    }

    private fun measure(): ProbeResult {
        var spent = 0L
        val results = mutableListOf<ClipResult>()
        var complete = true
        for (clip in CLIPS) {
            while (busy()) Thread.sleep(200)
            val left = boundMs - spent / 1_000_000
            if (left <= 0) {
                complete = false
                break
            }
            val file = File(clipsDir, clip.file)
            val t0 = nanoTime()
            val played = decoder.play(file, "auto-copy-safe", left)
            spent += nanoTime() - t0
            results += when {
                played == null -> ClipResult(clip.codec, clip.bitDepth, decoded = false)
                played.hwdec != "no" -> ClipResult(clip.codec, clip.bitDepth, decoded = true, hwdec = played.hwdec)
                // the hardware did not take it: mpv decoded it in software from its first frame,
                // which is the speed measured (a second play with hwdec=no measures the same)
                else -> ClipResult(clip.codec, clip.bitDepth, decoded = true, hwdec = "no", softwareFps = fpsAt2160(played, clip))
            }
        }
        if (results.size < CLIPS.size) complete = false
        return ProbeResult(key, results, spent / 1_000_000, complete)
    }

    companion object {
        /** The bound on the probe's decoding; on the CI runners it takes under 5 s. */
        const val BOUND_MS = 20_000L

        val CLIPS = listOf(
            ProbeClip("h264", 8, "h264-8.mkv"),
            ProbeClip("hevc", 8, "hevc-8.mkv"),
            ProbeClip("hevc", 10, "hevc-10.mkv"),
            ProbeClip("vp9", 8, "vp9-8.mkv"),
            ProbeClip("vp9", 10, "vp9-10.mkv"),
            ProbeClip("av1", 8, "av1-8.mkv"),
            ProbeClip("av1", 10, "av1-10.mkv"),
            // MPEG-2 stops at 1920x1152 (its High Level): its clip is 1080p
            ProbeClip("mpeg2video", 8, "mpeg2video-8.mkv", height = 1080),
        )

        /** The 0.3 s silent 7.1 clip the audio device's channels are measured with. */
        const val SILENCE = "silence-7.1.mka"

        /** A software play's speed as frames per second at 2160p (the clip's speed scaled by its pixel count). */
        fun fpsAt2160(p: Played, clip: ProbeClip): Double {
            if (p.nanos <= 0 || p.frames <= 0) return 0.0
            val fps = p.frames / (p.nanos / 1e9)
            val scale = (clip.height.toDouble() / 2160).let { it * it }
            return fps * scale
        }

        /** The probe's clips from the Player's resources into [dir] (mpv reads files), each written once. */
        fun extractClips(dir: File): File {
            dir.mkdirs()
            for (name in CLIPS.map { it.file } + SILENCE) {
                val bytes = Probe::class.java.classLoader.getResourceAsStream("probe/$name")?.use { it.readBytes() } ?: continue
                val out = File(dir, name)
                if (out.isFile && out.length() == bytes.size.toLong()) continue
                out.writeBytes(bytes)
            }
            return dir
        }
    }
}

/**
 * The machine's report as it stands (story 13.19): the probe's results once it has run (null
 * before: a video opened meanwhile opens with the profile alone, as story 13.18's do) and the
 * audio device's channels, measured at start and again after each watch screen closes (so a
 * headset or receiver connected since counts at the next video, never in the middle of one).
 */
class DeviceReport {
    @Volatile var probe: ProbeResult? = null
    @Volatile var channels: Int? = null
    @Volatile var probeError: String? = null
    /** The probe's results were read from probe.json (not measured at this start). */
    @Volatile var cached: Boolean = false

    /** The `device_caps` to send (null until the probe has run). */
    fun caps(): DeviceCaps? = probe?.let { DesktopCaps.build(it.clips, channels) }

    fun json(): JsonObject? = caps()?.toJson()

    /** The problem report's line: each codec's result, the channels, the cache key. */
    fun describe(): String {
        val p = probe ?: return "device report: " + (probeError?.let { "probe failed ($it)" } ?: "probe not run yet") +
            ", audio channels: ${channels ?: "unknown"}"
        val codecs = p.clips.joinToString(", ") { c ->
            val name = c.codec + if (c.bitDepth >= 10) " 10-bit" else ""
            when {
                !c.decoded -> "$name: did not decode"
                c.hardware -> "$name: hardware (${c.hwdec}) 2160p"
                else -> "$name: software ${DesktopCaps.heightFor(c.softwareFps ?: 0.0)}p (%.1f fps at 2160p)".format(java.util.Locale.ROOT, c.softwareFps ?: 0.0)
            }
        }
        return "device report: $codecs; audio channels: ${channels ?: "unknown"}; probe ${p.tookMs} ms" +
            (if (cached) " (cached)" else "") + (if (!p.complete) " (incomplete)" else "") + "; cache key: ${p.key}"
    }

    /** [describe] as a line of the problem report's log (its JSON lines, stamped now). */
    fun logLine(now: java.time.Instant = java.time.Instant.now()): String =
        kotlinx.serialization.json.buildJsonObject {
            put("ts", kotlinx.serialization.json.JsonPrimitive(now.toString()))
            put("level", kotlinx.serialization.json.JsonPrimitive("info"))
            put("msg", kotlinx.serialization.json.JsonPrimitive(describe()))
        }.toString()
}

/**
 * Runs the probe and the channel measurement for [report] on a thread of their own: [start]
 * once at start (the channels, then the probe), [watchClosed] after each watch screen closes
 * (the channels again).
 */
class ReportRunner(
    private val report: DeviceReport,
    private val decoder: () -> ProbeDecoder,
    private val clipsDir: () -> File,
    private val cache: ProbeCache,
    private val busy: () -> Boolean,
) {
    private val worker = Executors.newSingleThreadExecutor { r -> Thread(r, "mpv-probe").apply { isDaemon = true; priority = Thread.MIN_PRIORITY } }
    private val started = AtomicBoolean(false)
    @Volatile private var dec: ProbeDecoder? = null

    private fun decoder(): ProbeDecoder = dec ?: decoder().also { dec = it }

    fun start() {
        if (!started.compareAndSet(false, true)) return
        worker.execute {
            try {
                val d = decoder()
                val dir = clipsDir()
                report.channels = d.channels(File(dir, Probe.SILENCE), CHANNELS_TIMEOUT_MS)
                val key = ProbeKey.of(d.version).toString()
                val before = cache.read()
                val r = Probe(d, dir, cache, key, busy).run()
                report.cached = before != null && before == r
                report.probe = r
            } catch (e: Throwable) {
                report.probeError = e.message ?: e.toString()
            }
        }
    }

    fun watchClosed() {
        if (!started.get()) return
        worker.execute {
            runCatching { report.channels = decoder().channels(File(clipsDir(), Probe.SILENCE), CHANNELS_TIMEOUT_MS) }
        }
    }

    companion object {
        const val CHANNELS_TIMEOUT_MS = 3_000L
    }
}

/**
 * The probe on libmpv: a handle of its own with no video or audio output (`vo=null`, `ao=null`,
 * `untimed=yes`, `audio=no`), each clip loaded in turn; and for the channels, a short-lived
 * handle on the system's default output (`audio-channels=auto-safe`, volume 0), never a watch
 * screen's.
 */
class MpvProbeDecoder(private val lib: LibMpv) : ProbeDecoder {
    private val ctx: Pointer = lib.mpv_create() ?: error("libmpv could not start")

    init {
        fun opt(name: String, value: String) = lib.mpv_set_option_string(ctx, name, value)
        opt("vo", "null")
        opt("ao", "null")
        opt("audio", "no")
        opt("untimed", "yes")
        opt("framedrop", "no")
        opt("idle", "yes")
        opt("terminal", "no")
        opt("hwdec", "auto-copy-safe")
        val err = lib.mpv_initialize(ctx)
        if (err < 0) {
            lib.mpv_terminate_destroy(ctx)
            error("libmpv: " + lib.mpv_error_string(err))
        }
    }

    override val version: String by lazy {
        listOfNotNull(string(ctx, "mpv-version"), string(ctx, "ffmpeg-version")?.let { "ffmpeg $it" }).joinToString(" ")
            .ifBlank { "unknown" }
    }

    override fun play(clip: File, hwdec: String, timeoutMs: Long): Played? {
        lib.mpv_set_property_string(ctx, "hwdec", hwdec)
        if (lib.mpv_command(ctx, arrayOf("loadfile", clip.absolutePath, null)) < 0) return null
        val end = System.nanoTime() + timeoutMs * 1_000_000
        var loadedAt = 0L
        var used: String? = null
        var frames = 0
        while (true) {
            val left = (end - System.nanoTime()) / 1e9
            if (left <= 0) {
                // past the bound: stopped, and its end waited for
                lib.mpv_command(ctx, arrayOf("stop", null))
                drainUntilEnd(2.0)
                return null
            }
            val ev = lib.mpv_wait_event(ctx, left)
            when (ev.getInt(0)) {
                MpvConst.EVENT_FILE_LOADED -> {
                    loadedAt = System.nanoTime()
                    frames = string(ctx, "estimated-frame-count")?.toIntOrNull() ?: 24
                }
                MpvConst.EVENT_PLAYBACK_RESTART -> if (used == null) used = string(ctx, "hwdec-current") ?: "no"
                MpvConst.EVENT_END_FILE -> {
                    val reason = ev.getPointer(16)?.getInt(0) ?: MpvConst.END_ERROR
                    // timed from the file's opening to its end: every frame decoded, the
                    // decoder's start counted (a slow start is a slow decoder too)
                    val took = System.nanoTime() - loadedAt
                    val hw = used
                    return if (reason == MpvConst.END_EOF && hw != null && loadedAt > 0) Played(hw, frames, took) else null
                }
                MpvConst.EVENT_SHUTDOWN -> return null
            }
        }
    }

    private fun drainUntilEnd(seconds: Double) {
        val end = System.nanoTime() + (seconds * 1e9).toLong()
        while (System.nanoTime() < end) {
            val id = lib.mpv_wait_event(ctx, 0.2).getInt(0)
            if (id == MpvConst.EVENT_END_FILE || id == MpvConst.EVENT_SHUTDOWN) return
        }
    }

    override fun channels(clip: File, timeoutMs: Long): Int? {
        val c = lib.mpv_create() ?: return null
        try {
            fun opt(name: String, value: String) = lib.mpv_set_option_string(c, name, value)
            opt("vo", "null")
            opt("video", "no")
            opt("audio-channels", "auto-safe")
            opt("volume", "0")
            opt("keep-open", "yes")
            opt("idle", "yes")
            opt("terminal", "no")
            if (lib.mpv_initialize(c) < 0) return null
            lib.mpv_command(c, arrayOf("loadfile", clip.absolutePath, null))
            val end = System.nanoTime() + timeoutMs * 1_000_000
            while (true) {
                val left = (end - System.nanoTime()) / 1e9
                if (left <= 0) return null
                when (lib.mpv_wait_event(c, left).getInt(0)) {
                    MpvConst.EVENT_AUDIO_RECONFIG, MpvConst.EVENT_PLAYBACK_RESTART ->
                        string(c, "audio-out-params/channel-count")?.toIntOrNull()?.takeIf { it > 0 }?.let { return it }
                    MpvConst.EVENT_END_FILE, MpvConst.EVENT_SHUTDOWN ->
                        return string(c, "audio-out-params/channel-count")?.toIntOrNull()?.takeIf { it > 0 }
                }
            }
        } finally {
            lib.mpv_terminate_destroy(c)
        }
    }

    override fun close() = lib.mpv_terminate_destroy(ctx)

    private fun string(c: Pointer, name: String): String? {
        val p = lib.mpv_get_property_string(c, name) ?: return null
        return try {
            p.getString(0, "UTF-8")
        } finally {
            lib.mpv_free(p)
        }
    }
}
