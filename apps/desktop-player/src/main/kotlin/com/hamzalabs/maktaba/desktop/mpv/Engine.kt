package com.hamzalabs.maktaba.desktop.mpv

import com.hamzalabs.maktaba.core.session.KeyValueStore
import com.hamzalabs.maktaba.desktop.FxPlayers
import com.hamzalabs.maktaba.desktop.Players
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import java.io.File
import java.time.Instant

/**
 * The video engine chosen in Profile → Playback (story 13.21), kept in the Player's own
 * settings file as `player.engine`: **Automatic** (libmpv, JavaFX only when libmpv cannot load)
 * or the **Older engine** (JavaFX and the `desktop-player` profile).
 */
enum class EngineChoice(val stored: String) {
    Auto("auto"),
    Older("javafx"),
    ;

    companion object {
        const val KEY = "player.engine"

        /**
         * A stored choice. A value this Player does not know (written by a newer one), or the
         * older engine in a Player that no longer carries it ([olderAvailable] false, after
         * story 13.22), reads as Automatic.
         */
        fun of(stored: String?, olderAvailable: Boolean = true): EngineChoice {
            val v = stored?.trim()?.lowercase()
            val c = entries.firstOrNull { it.stored == v } ?: return Auto
            return if (c == Older && !olderAvailable) Auto else c
        }
    }
}

/**
 * Which engine plays the next video, read at each open (stories 13.18 and 13.21):
 * `MAKTABA_PLAYER_ENGINE=javafx` first (support's way, on this computer), then the choice in
 * Profile, then whether libmpv loaded. libmpv is looked for once, when first needed; the
 * Profile setting is read every time ([setting]).
 */
open class Engines(
    private val env: Map<String, String> = System.getenv(),
    private val load: () -> MpvLoad = { MpvLoad.current },
    /** libmpv's players (null: the Player's own, with what [startProbe] measured); a test gives its own. */
    private val mpv: ((MpvLoad.Loaded) -> Players)? = null,
    private val fx: Players = FxPlayers,
) {
    /** Why a video plays with the engine it does. */
    enum class Why { Automatic, Profile, Variable, Missing }

    /** The engine for a video ([mpv] true: libmpv), why, and libmpv's [reason] when it did not load. */
    data class Pick(val mpv: Boolean, val why: Why, val reason: String? = null) {
        /** For the problem report: the engine and why. */
        fun describe(): String = when (why) {
            Why.Automatic -> "libmpv, automatic"
            Why.Profile -> "JavaFX, chosen in Profile"
            Why.Variable -> "JavaFX, $VARIABLE"
            Why.Missing -> "JavaFX, libmpv did not load: $reason"
        }
    }

    /**
     * What Profile → Playback's **Video engine** row shows: the choice, or the older engine
     * locked by [lock] (the variable, or libmpv missing with its [reason]).
     */
    data class Row(val chosen: EngineChoice, val lock: Why? = null, val reason: String? = null) {
        /** The choice the row shows as chosen. */
        val shown: EngineChoice get() = if (lock != null) EngineChoice.Older else chosen
    }

    /** The choice in Profile, read at each open (the Player gives its settings' at start). */
    @Volatile var setting: () -> EngineChoice = { EngineChoice.Auto }

    /** The hardware decoder libmpv used for the last video ("no": software), for the problem report. */
    @Volatile var hwdec: String? = null

    private val loaded: MpvLoad by lazy { load() }
    private val mpvPlayers: Players? by lazy {
        (loaded as? MpvLoad.Loaded)?.let { l ->
            mpv?.invoke(l) ?: MpvPlayers(l.lib, captionFonts(), report = report, closed = { runner?.watchClosed() })
        }
    }

    /** What this machine plays (story 13.19), once [startProbe] has run; JavaFX has none. */
    val report = DeviceReport()

    private val runner: ReportRunner? by lazy {
        val l = loaded as? MpvLoad.Loaded ?: return@lazy null
        ReportRunner(
            report,
            newDecoder = { MpvProbeDecoder(l.lib) },
            clipsDir = { Probe.extractClips(File(cacheBase(), "maktaba-player/probe")) },
            cache = ProbeCache(File(com.hamzalabs.maktaba.desktop.AppDirs.config, "probe.json")),
            marker = File(com.hamzalabs.maktaba.desktop.AppDirs.config, "probe.running"),
            busy = { (mpvPlayers as? MpvPlayers)?.playing == true },
            videosOpened = { (mpvPlayers as? MpvPlayers)?.opened ?: 0L },
        )
    }
    @Volatile private var probing = false

    /**
     * Starts the decoder probe and the audio device's measurement (story 13.19), off the UI
     * thread, on handles of their own: nothing while videos play with JavaFX ([pick]).
     */
    fun startProbe() {
        // loading libmpv itself is off the UI thread too
        Thread({
            runCatching {
                if (pick().mpv) {
                    probing = true
                    runner?.start()
                }
            }
        }, "mpv-probe-start").apply {
            isDaemon = true
            start()
        }
    }

    /** The engine the next video opens with. */
    fun pick(): Pick = when {
        variableSet(env) -> Pick(false, Why.Variable)
        setting() == EngineChoice.Older -> Pick(false, Why.Profile)
        else -> when (val l = loaded) {
            is MpvLoad.Loaded -> Pick(true, Why.Automatic)
            is MpvLoad.Missing -> Pick(false, Why.Missing, l.reason)
        }
    }

    /** The row's state with [chosen] the choice in Profile: the variable first, then libmpv's state, then the choice. */
    fun row(chosen: EngineChoice = setting()): Row = when {
        variableSet(env) -> Row(chosen, Why.Variable)
        else -> when (val l = loaded) {
            is MpvLoad.Missing -> Row(chosen, Why.Missing, l.reason)
            is MpvLoad.Loaded -> Row(chosen)
        }
    }

    private val kept = ArrayDeque<Pair<Instant, String>>()
    @Volatile private var last: Pick? = null

    /**
     * The players for a video opening now, by [pick]; each open is kept for the problem report
     * (the last [KEPT] lines), so a report says which engine played each video.
     */
    fun players(now: Instant = Instant.now()): Players {
        val p = pick()
        last = p
        note("video opened: playback: ${p.describe()}", now)
        return if (p.mpv) mpvPlayers ?: fx else fx
    }

    /** A line for the problem report's log, about playback (the last [KEPT] are kept). */
    fun note(msg: String, now: Instant = Instant.now()) {
        synchronized(kept) {
            kept.addLast(now to msg)
            while (kept.size > KEPT) kept.removeFirst()
        }
    }

    /** A line for the problem report: the engine the last video played with (else the next one's) and why. */
    fun describe(): String {
        val p = last ?: pick()
        val file = (loaded as? MpvLoad.Loaded)?.path?.name
        return if (p.mpv) "playback: ${p.describe()} ($file), hardware decoder: ${hwdec ?: "not used yet"}"
        else "playback: ${p.describe()}"
    }

    /**
     * Why an installed Player plays with JavaFX against the person's choice, to say once
     * ([EngineNotice]): libmpv did not load. Null when libmpv plays, when JavaFX was chosen (in
     * Profile or by the variable), or in a development run, which has no libmpv to miss.
     */
    fun noticeReason(installed: Boolean = System.getProperty("jpackage.app-path") != null): String? {
        if (!installed) return null
        val p = pick()
        return if (p.why == Why.Missing) p.reason else null
    }

    /**
     * The problem report's lines (JSON lines): each video opened, with its engine and why, and
     * other [note]s, then the state now, then the device report once the probe has started (story
     * 13.19: each codec's result, the audio channels, the probe's cache key).
     */
    fun logLines(now: Instant = Instant.now()): List<String> {
        val each = synchronized(kept) { kept.toList() }.map { (at, msg) -> line(at, msg) }
        return each + line(now, describe()) + if (probing) listOf(report.logLine(now)) else emptyList()
    }

    private fun line(at: Instant, msg: String): String = buildJsonObject {
        put("ts", JsonPrimitive(at.toString()))
        put("level", JsonPrimitive("info"))
        put("msg", JsonPrimitive(msg))
    }.toString()

    companion object {
        const val VARIABLE = "MAKTABA_PLAYER_ENGINE"

        /** How many playback lines (video opens, notes) the problem report lists. */
        const val KEPT = 40

        /** Support's way back to JavaFX for one release: `MAKTABA_PLAYER_ENGINE=javafx`. */
        fun variableSet(env: Map<String, String>): Boolean = env[VARIABLE].equals("javafx", ignoreCase = true)
    }
}

/** This Player's engines: libmpv from its install folder, the setting from its settings file (Main). */
object Engine : Engines()

/**
 * The notice that libmpv did not load (story 13.18), said once per Player version and reason
 * (story 13.21), not at every start: what was said last is kept as `player.engine_notice`.
 */
class EngineNotice(private val store: KeyValueStore, private val version: String) {
    /** [reason], to say, or null: nothing to say, or said already for this version and reason. */
    fun pending(reason: String?): String? = reason?.takeIf { store.get(KEY) != key(it) }

    /** [reason] was said (shown): not again for this version. */
    fun said(reason: String) = store.put(KEY, key(reason))

    private fun key(reason: String) = "$version\n$reason"

    companion object {
        const val KEY = "player.engine_notice"
    }
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
