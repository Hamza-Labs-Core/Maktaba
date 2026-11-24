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
 * in `resources/probe/` (made by `apps/desktop-player/probe-media.sh`, outside libmpv/ so the
 * libmpv build cache keeps its key), [height] its picture's (2160, MPEG-2's 1080).
 */
data class ProbeClip(val codec: String, val bitDepth: Int, val file: String, val height: Int = 2160)

/**
 * What the probe found for one clip. [hwdec] is mpv's `hwdec-current` on its first
 * `playback-restart` with `hwdec=auto-copy-safe` ("no": software; null: it did not decode);
 * [softwareFps] the frames per second software decoded it at, scaled to 2160p (null when a
 * hardware decoder took it). [fallback] says why a clip has no speed though it is counted as
 * decoding in software, at [DesktopCaps.SAFE_HEIGHT]: it ran out of time, or the probe before
 * stopped in it (libmpv crashed, or the Player was killed).
 */
@Serializable
data class ClipResult(
    val codec: String,
    @SerialName("bit_depth") val bitDepth: Int,
    val decoded: Boolean,
    val hwdec: String? = null,
    @SerialName("software_fps") val softwareFps: Double? = null,
    val fallback: String? = null,
) {
    /** A hardware decoder took it: anything but "no". */
    val hardware: Boolean get() = decoded && hwdec != null && hwdec != "no"

    companion object {
        const val TIMED_OUT = "timed out"
        const val STOPPED_HERE = "skipped: the last probe stopped in this clip"
    }
}

/** The probe's answers on this machine, kept in `probe.json` under the [key] they were measured with. */
@Serializable
data class ProbeResult(
    val key: String,
    val clips: List<ClipResult>,
    @SerialName("took_ms") val tookMs: Long,
    /** Every clip was measured (none left out, none a fallback): an incomplete probe is tried again after a back-off. */
    val complete: Boolean = true,
    /** When it was measured (epoch milliseconds), and how many probes in a row were incomplete. */
    val at: Long = 0,
    val attempts: Int = 1,
)

/** What one play of a clip came to. */
sealed interface Play

/**
 * A clip decoded: the decoder used ("no": software), and the frames decoded after the first
 * picture ([frames]) in the time from mpv's first `playback-restart` to the file's end.
 */
data class Played(val hwdec: String, val frames: Int, val nanos: Long) : Play

/** It did not decode. */
data object PlayFailed : Play

/** It did not end in the time it had. */
data object PlayTimedOut : Play

/** The libmpv side of the probe (a test gives a fake). */
interface ProbeDecoder {
    /** libmpv's and its FFmpeg's versions, for the cache key. */
    val version: String

    /** Plays [clip] once with [hwdec] ("auto-copy-safe", "no"), within [timeoutMs]. */
    fun play(clip: File, hwdec: String, timeoutMs: Long): Play

    /** Plays [clip] once, untimed, so libmpv's first-use costs are paid before a clip is timed. */
    fun warmUp(clip: File, timeoutMs: Long) {}

    /** The default audio output's channel count for [clip] (a silent 7.1 clip); null: no device opened. */
    fun channels(clip: File, timeoutMs: Long): Int?

    /** Frees the probe's libmpv handle (the channel measurement opens its own each time). */
    fun close() {}
}

/**
 * What the probe's results are cached under: the GPUs (each adapter's name and its driver's
 * version; on Linux also the VA-API drivers' files, which Mesa updates change), the CPU and
 * libmpv's version. A key that changed (another GPU, a new driver, a new libmpv in an update)
 * probes again.
 */
data class ProbeKey(val gpus: List<String>, val cpu: String, val libmpv: String) {
    override fun toString(): String = "gpu=" + gpus.ifEmpty { listOf("none") }.joinToString(" + ") + "; cpu=$cpu; libmpv=$libmpv"

    companion object {
        /** This machine's key, read from the system (never throws: what cannot be read is "unknown"). */
        fun of(libmpv: String, os: String = System.getProperty("os.name").orEmpty()): ProbeKey = when {
            os.startsWith("Windows") -> ProbeKey(runCatching { windowsGpus() }.getOrDefault(emptyList()), windowsCpu(), libmpv)
            os.startsWith("Linux") -> ProbeKey(
                runCatching { linuxGpus(File("/sys/class/drm")) }.getOrDefault(emptyList()) +
                    runCatching { vaDrivers(vaDriverDirs(System.getenv("LIBVA_DRIVERS_PATH"))) }.getOrDefault(emptyList()),
                linuxCpu(), libmpv,
            )
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

        /** Where libva looks for its drivers: LIBVA_DRIVERS_PATH, else the distributions' folders. */
        fun vaDriverDirs(env: String?): List<File> =
            (env?.split(':')?.filter { it.isNotBlank() }
                ?: listOf("/usr/lib/x86_64-linux-gnu/dri", "/usr/lib/aarch64-linux-gnu/dri", "/usr/lib64/dri", "/usr/lib/dri"))
                .map(::File)

        /**
         * The VA-API drivers' files (`*_drv_video.so`: Mesa's radeonsi and nouveau, Intel's iHD,
         * …), each by name, size and modification time: a Mesa or driver update changes them,
         * and the user-space driver decides what VA-API decodes. Cheap: no file is read.
         */
        fun vaDrivers(dirs: List<File>): List<String> = dirs.flatMap { d ->
            (d.listFiles() ?: emptyArray()).filter { it.name.endsWith("_drv_video.so") && it.isFile }.sortedBy { it.name }
                .map { f -> "va:${f.name} ${f.length()} ${f.lastModified() / 1000}" }
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
 * never a watch screen's. libmpv is warmed up once (a clip played untimed), then each clip is
 * played with `hwdec=auto-copy-safe`: its `hwdec-current` says whether a hardware decoder took
 * it; one it did not is decoded in software from its first picture, timed from mpv's first
 * `playback-restart` to its end, and that speed (scaled to 2160p by pixel count) gives the
 * tallest picture it decodes smoothly ([DesktopCaps.heightFor]).
 *
 * - The results are cached in [cache] under the [key]: the same key reads them back, another
 *   probes again. An incomplete probe is cached too and tried again after a back-off
 *   ([backoffMs]), never at every start.
 * - Decoding is bounded at [boundMs]; a clip has at most [CLIP_TIMEOUT_MS]. A clip that runs out
 *   of time counts as software at [DesktopCaps.SAFE_HEIGHT]; clips the bound leaves unplayed
 *   are left out of the narrowing (the report lists their codec without limits).
 * - It waits between clips while [busy] (a watch screen is open), and a clip during which a
 *   video was opened ([videosOpened] moved, or [busy]) is measured again.
 * - [marker] names the clip being played; a probe that finds it at its start (libmpv crashed
 *   the Player in it, or the Player was killed) skips that clip, counted as software at the
 *   safe height, so a crash in a clip never repeats at every start.
 */
class Probe(
    private val decoder: ProbeDecoder,
    private val clipsDir: File,
    private val cache: ProbeCache,
    private val key: String,
    private val busy: () -> Boolean = { false },
    private val videosOpened: () -> Long = { 0L },
    private val marker: File? = null,
    private val boundMs: Long = BOUND_MS,
    private val nanoTime: () -> Long = System::nanoTime,
    private val now: () -> Long = System::currentTimeMillis,
) {
    /** The cached results when their key is this machine's (an incomplete one until its back-off ends), else a new probe's (cached). */
    fun run(): ProbeResult {
        val stopped = marker?.let { m -> runCatching { m.readText().trim() }.getOrNull() }?.takeIf { it.isNotEmpty() }
        val cached = cache.read()?.takeIf { it.key == key }
        if (cached != null && stopped == null) {
            if (cached.complete) return cached
            if (now() < cached.at + backoffMs(cached.attempts)) return cached
        }
        val attempts = if (cached != null && !cached.complete) cached.attempts + 1 else 1
        val r = measure(stopped, attempts)
        cache.write(r)
        marker?.delete()
        return r
    }

    private fun mark(clip: String?) {
        val m = marker ?: return
        runCatching {
            if (clip == null) m.delete() else {
                m.parentFile?.mkdirs()
                m.writeText(clip)
            }
        }
    }

    private fun measure(stopped: String?, attempts: Int): ProbeResult {
        var spent = 0L
        fun left() = boundMs - spent / 1_000_000
        fun timed(block: () -> Play): Play {
            val t0 = nanoTime()
            return try {
                block()
            } finally {
                spent += nanoTime() - t0
            }
        }
        val results = mutableListOf<ClipResult>()
        var warmed = false
        for (clip in CLIPS) {
            if (clip.file == stopped) {
                results += ClipResult(clip.codec, clip.bitDepth, decoded = true, hwdec = "no", fallback = ClipResult.STOPPED_HERE)
                continue
            }
            val file = File(clipsDir, clip.file)
            var result: ClipResult? = null
            var tries = 0
            while (result == null && tries < MAX_TRIES && left() > 0) {
                tries++
                while (busy()) Thread.sleep(200)
                if (!warmed) {
                    // libmpv's first use (its decoders' and threads' start) is paid here, untimed
                    mark(clip.file)
                    timed { decoder.warmUp(file, minOf(left(), CLIP_TIMEOUT_MS)); PlayFailed }
                    mark(null)
                    warmed = true
                    if (left() <= 0) break
                }
                val opened = videosOpened()
                mark(clip.file)
                val p = timed { decoder.play(file, "auto-copy-safe", minOf(left(), CLIP_TIMEOUT_MS)) }
                mark(null)
                // a video opened meanwhile shared the machine: this clip is measured again
                if (busy() || videosOpened() != opened) continue
                result = when (p) {
                    PlayFailed -> ClipResult(clip.codec, clip.bitDepth, decoded = false)
                    PlayTimedOut -> ClipResult(clip.codec, clip.bitDepth, decoded = true, hwdec = "no", fallback = ClipResult.TIMED_OUT)
                    is Played -> if (p.hwdec != "no") {
                        ClipResult(clip.codec, clip.bitDepth, decoded = true, hwdec = p.hwdec)
                    } else {
                        ClipResult(clip.codec, clip.bitDepth, decoded = true, hwdec = "no", softwareFps = fpsAt2160(p, clip))
                    }
                }
            }
            // out of time (or a video kept interrupting it): this clip is left out
            result?.let { results += it }
        }
        val complete = results.size == CLIPS.size && results.none { it.fallback != null }
        return ProbeResult(key, results, spent / 1_000_000, complete, at = now(), attempts = if (complete) 1 else attempts)
    }

    companion object {
        /** The bound on the probe's decoding; on the CI runners it takes under 5 s. */
        const val BOUND_MS = 20_000L

        /** The most one clip may take. */
        const val CLIP_TIMEOUT_MS = 8_000L

        /** A clip a video kept interrupting is left out after this many plays. */
        const val MAX_TRIES = 3

        private const val DAY_MS = 24L * 60 * 60 * 1000

        /** How long an incomplete probe stands before it is tried again: a day, doubling, at most 30. */
        fun backoffMs(attempts: Int): Long = (DAY_MS shl (attempts - 1).coerceIn(0, 5)).coerceAtMost(30 * DAY_MS)

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
        val codecs = Probe.CLIPS.joinToString(", ") { clip ->
            val name = clip.codec + if (clip.bitDepth >= 10) " 10-bit" else ""
            val c = p.clips.firstOrNull { it.codec == clip.codec && it.bitDepth == clip.bitDepth }
            when {
                c == null -> "$name: not probed (not narrowed)"
                !c.decoded -> "$name: did not decode"
                c.hardware -> "$name: hardware (${c.hwdec}) 2160p"
                c.fallback != null -> "$name: software ${DesktopCaps.SAFE_HEIGHT}p (${c.fallback})"
                else -> "$name: software ${DesktopCaps.heightFor(c.softwareFps ?: 0.0)}p (%.1f fps at 2160p)".format(java.util.Locale.ROOT, c.softwareFps ?: 0.0)
            }
        }
        return "device report: $codecs; audio channels: ${channels ?: "unknown"}; probe ${p.tookMs} ms" +
            (if (cached) " (cached)" else "") + (if (!p.complete) " (incomplete, try ${p.attempts})" else "") + "; cache key: ${p.key}"
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
 * once at start (the channels, then the probe, whose libmpv handle is freed once it ends),
 * [watchClosed] after each watch screen closes (the channels again, on a handle of their own).
 */
class ReportRunner(
    private val report: DeviceReport,
    private val newDecoder: () -> ProbeDecoder,
    private val clipsDir: () -> File,
    private val cache: ProbeCache,
    private val marker: File,
    private val busy: () -> Boolean,
    private val videosOpened: () -> Long,
) {
    private val worker = Executors.newSingleThreadExecutor { r -> Thread(r, "mpv-probe").apply { isDaemon = true; priority = Thread.MIN_PRIORITY } }
    private val started = AtomicBoolean(false)
    @Volatile private var dec: ProbeDecoder? = null

    private fun decoder(): ProbeDecoder = dec ?: newDecoder().also { dec = it }

    fun start() {
        if (!started.compareAndSet(false, true)) return
        // a Player that quits mid-clip is no crash: the marker goes (a crash runs no hook)
        runCatching { Runtime.getRuntime().addShutdownHook(Thread { if (report.probe == null) marker.delete() }) }
        worker.execute {
            try {
                val d = decoder()
                val dir = clipsDir()
                report.channels = d.channels(File(dir, Probe.SILENCE), CHANNELS_TIMEOUT_MS)
                val key = ProbeKey.of(d.version).toString()
                val before = cache.read()
                val r = Probe(d, dir, cache, key, busy, videosOpened, marker).run()
                report.cached = before != null && before == r
                report.probe = r
            } catch (e: Throwable) {
                report.probeError = e.message ?: e.toString()
            } finally {
                // the probe's handle is freed; the channels open their own when measured again
                runCatching { dec?.close() }
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
 * `untimed=yes`, `audio=no`), made at its first play and freed by [close], each clip loaded in
 * turn; and for the channels, a short-lived handle on the system's default output
 * (`audio-channels=auto-safe`, volume 0), never a watch screen's.
 */
class MpvProbeDecoder(private val lib: LibMpv) : ProbeDecoder {
    private var handle: Pointer? = null

    @Synchronized
    private fun ctx(): Pointer = handle ?: run {
        val c = lib.mpv_create() ?: error("libmpv could not start")
        fun opt(name: String, value: String) = lib.mpv_set_option_string(c, name, value)
        opt("vo", "null")
        opt("ao", "null")
        opt("audio", "no")
        opt("untimed", "yes")
        opt("framedrop", "no")
        opt("idle", "yes")
        opt("terminal", "no")
        opt("hwdec", "auto-copy-safe")
        val err = lib.mpv_initialize(c)
        if (err < 0) {
            lib.mpv_terminate_destroy(c)
            error("libmpv: " + lib.mpv_error_string(err))
        }
        handle = c
        c
    }

    override val version: String by lazy {
        val c = ctx()
        listOfNotNull(string(c, "mpv-version"), string(c, "ffmpeg-version")?.let { "ffmpeg $it" }).joinToString(" ")
            .ifBlank { "unknown" }
    }

    override fun warmUp(clip: File, timeoutMs: Long) {
        play(clip, "no", timeoutMs)
    }

    override fun play(clip: File, hwdec: String, timeoutMs: Long): Play {
        val ctx = ctx()
        lib.mpv_set_property_string(ctx, "hwdec", hwdec)
        if (lib.mpv_command(ctx, arrayOf("loadfile", clip.absolutePath, null)) < 0) return PlayFailed
        val end = System.nanoTime() + timeoutMs * 1_000_000
        var restartAt = 0L
        var used: String? = null
        var frames = 24
        while (true) {
            val left = (end - System.nanoTime()) / 1e9
            if (left <= 0) {
                // past its time: stopped, and its end waited for
                lib.mpv_command(ctx, arrayOf("stop", null))
                drainUntilEnd(ctx, 2.0)
                return PlayTimedOut
            }
            val ev = lib.mpv_wait_event(ctx, left)
            when (ev.getInt(0)) {
                MpvConst.EVENT_FILE_LOADED -> frames = string(ctx, "estimated-frame-count")?.toIntOrNull()?.takeIf { it > 1 } ?: 24
                MpvConst.EVENT_PLAYBACK_RESTART -> if (used == null) {
                    // the first picture is out: the decoder has started (a failed hardware
                    // attempt is behind it); the frames after it are the ones timed
                    restartAt = System.nanoTime()
                    used = string(ctx, "hwdec-current") ?: "no"
                }
                MpvConst.EVENT_END_FILE -> {
                    val reason = ev.getPointer(16)?.getInt(0) ?: MpvConst.END_ERROR
                    val hw = used
                    return if (reason == MpvConst.END_EOF && hw != null) Played(hw, frames - 1, System.nanoTime() - restartAt) else PlayFailed
                }
                MpvConst.EVENT_SHUTDOWN -> return PlayFailed
            }
        }
    }

    private fun drainUntilEnd(ctx: Pointer, seconds: Double) {
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

    @Synchronized
    override fun close() {
        handle?.let { lib.mpv_terminate_destroy(it) }
        handle = null
    }

    private fun string(c: Pointer, name: String): String? {
        val p = lib.mpv_get_property_string(c, name) ?: return null
        return try {
            p.getString(0, "UTF-8")
        } finally {
            lib.mpv_free(p)
        }
    }
}
