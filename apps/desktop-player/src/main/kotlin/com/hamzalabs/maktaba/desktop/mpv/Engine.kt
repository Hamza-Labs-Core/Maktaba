package com.hamzalabs.maktaba.desktop.mpv

import com.hamzalabs.maktaba.desktop.FxPlayers
import com.hamzalabs.maktaba.desktop.Players
import java.io.File

/**
 * Which engine plays videos (story 13.18): libmpv, which the Player carries, or, when it cannot
 * load, JavaFX's for one more release. [fallbackReason] says why JavaFX plays (null: libmpv
 * does); the Player says so once and its problem report carries it.
 */
object Engine {
    private val load: MpvLoad by lazy { MpvLoad.current }

    /** What this machine plays (story 13.19), once [startProbe] has run; JavaFX has none. */
    val report = DeviceReport()

    private val runner: ReportRunner? by lazy {
        val l = load as? MpvLoad.Loaded ?: return@lazy null
        ReportRunner(
            report,
            decoder = { MpvProbeDecoder(l.lib) },
            clipsDir = { Probe.extractClips(File(cacheBase(), "maktaba-player/probe")) },
            cache = ProbeCache(File(com.hamzalabs.maktaba.desktop.AppDirs.config, "probe.json")),
            busy = { (players as? MpvPlayers)?.playing == true },
        )
    }

    val players: Players by lazy {
        when (val l = load) {
            is MpvLoad.Loaded -> MpvPlayers(l.lib, captionFonts(), report, closed = { runner?.watchClosed() })
            is MpvLoad.Missing -> FxPlayers
        }
    }

    /**
     * Starts the decoder probe and the audio device's measurement (story 13.19), off the UI
     * thread, on handles of their own: nothing when JavaFX plays.
     */
    fun startProbe() {
        // loading libmpv itself is off the UI thread too
        Thread({ runCatching { runner?.start() } }, "mpv-probe-start").apply {
            isDaemon = true
            start()
        }
    }

    val fallbackReason: String? get() = (load as? MpvLoad.Missing)?.reason

    /** The hardware decoder libmpv used for the last video ("no": software), for the problem report. */
    @Volatile var hwdec: String? = null

    /**
     * Why a packaged Player plays with JavaFX, to say once at start (null: libmpv plays, or a
     * development run, which has no libmpv to miss, or JavaFX chosen on purpose).
     */
    fun noticeReason(): String? = when {
        // only an installed Player (jpackage's launcher sets it), not a development run
        System.getProperty("jpackage.app-path") == null -> null
        fallbackReason?.startsWith("MAKTABA_PLAYER_ENGINE") == true -> null
        else -> fallbackReason
    }

    /**
     * The problem report's lines: the engine, and with libmpv the device report (story 13.19:
     * each codec's probe result, the audio channels, the probe's cache key).
     */
    fun logLines(now: java.time.Instant = java.time.Instant.now()): List<String> =
        listOf(logLine(now)) + if (load is MpvLoad.Loaded) listOf(report.logLine(now)) else emptyList()

    /** [describe] as a line of the problem report's log (its JSON lines, stamped now). */
    fun logLine(now: java.time.Instant = java.time.Instant.now()): String =
        kotlinx.serialization.json.buildJsonObject {
            put("ts", kotlinx.serialization.json.JsonPrimitive(now.toString()))
            put("level", kotlinx.serialization.json.JsonPrimitive("info"))
            put("msg", kotlinx.serialization.json.JsonPrimitive(describe()))
        }.toString()

    /** A line for the problem report's log: the engine, and why JavaFX when it is. */
    fun describe(): String = when (val l = load) {
        is MpvLoad.Loaded -> "playback: libmpv (${l.path.name}), hardware decoder: ${hwdec ?: "not used yet"}"
        is MpvLoad.Missing -> "playback: JavaFX (${l.reason})"
    }

    /**
     * The Player's caption face (IBM Plex Sans Arabic, from its resources) in a folder libass
     * can read; null when it cannot be written (mpv then uses the system's sans-serif).
     */
    private fun captionFonts(): File? = runCatching {
        val dir = File(cacheBase(), "maktaba-player/fonts").apply { mkdirs() }
        for (name in listOf("ibm_plex_sans_arabic_regular.ttf", "ibm_plex_sans_arabic_semibold.ttf")) {
            val out = File(dir, name)
            if (out.isFile && out.length() > 0) continue
            val bytes = Engine::class.java.classLoader.getResourceAsStream(name)?.use { it.readBytes() } ?: continue
            out.writeBytes(bytes)
        }
        dir
    }.getOrNull()

    /** This user's own cache folder (a shared temporary one could be another user's, files planted). */
    private fun cacheBase(): File = System.getenv("LOCALAPPDATA")?.let(::File)
        ?: System.getenv("XDG_CACHE_HOME")?.let(::File)
        ?: File(System.getProperty("user.home"), ".cache")
}
