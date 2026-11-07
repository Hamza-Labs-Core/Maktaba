package com.hamzalabs.maktaba.core.crash

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement

import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import java.io.File
import java.time.Instant
import java.util.UUID

/**
 * The crashes kept on this device and which were sent (Epic 53 story 53.14): each kept file
 * (`crash-<ms>-<id>.json`, a [KeptCrash], the newest [KEEP]) goes to the home once crash
 * reports are on, at the next start signed in. A 429 or no answer keeps it for later: at most
 * [MAX_RETRIES] retries, over at most [MAX_AGE_MS]. When crash reports are off nothing is sent
 * and the crash stays on the device, where a problem report can attach it.
 */
class CrashOutbox(val dir: File, private val now: () -> Long = System::currentTimeMillis) {
    @Serializable
    internal data class Entry(var attempts: Int = 0, var next: Long? = null, var done: Boolean = false, var gaveUp: Boolean = false)

    @Serializable
    internal data class State(val entries: MutableMap<String, Entry> = mutableMapOf(), var announced: String = "")

    sealed interface SendResult {
        data object Sent : SendResult
        /** later: after [waitMs] (a 429's Retry-After), or the outbox's own wait */
        data class Later(val waitMs: Long? = null) : SendResult
        /** the home says crash reports are off for this device */
        data object Refused : SendResult
    }

    /** What to tell the person about crashes since they were last told, once. */
    enum class Notice { Sent, Kept }

    private val stateFile = File(dir, "outbox.json")
    private val lock = Any()

    internal fun load(): State = runCatching { JSON.decodeFromString(State.serializer(), stateFile.readText()) }.getOrElse { State() }

    private fun save(s: State) {
        dir.mkdirs()
        val tmp = File(dir, "outbox.json.tmp")
        tmp.writeText(JSON.encodeToString(State.serializer(), s))
        // File.renameTo does not replace an existing file on Windows: the state was never saved
        // after its first time there (CrashOutboxTest on the desktop Player's Windows job)
        java.nio.file.Files.move(tmp.toPath(), stateFile.toPath(), java.nio.file.StandardCopyOption.REPLACE_EXISTING)
    }

    /** The kept files, oldest first. */
    fun files(): List<File> = dir.listFiles { f -> f.name.startsWith("crash-") && f.name.endsWith(".json") }
        .orEmpty().sortedBy { it.name }

    /** Keeps [crash] (the oldest go past [KEEP]). */
    fun keep(crash: KeptCrash): File {
        dir.mkdirs()
        val f = File(dir, "crash-${"%015d".format(crash.at)}-${UUID.randomUUID().toString().take(8)}.json")
        f.writeText(crash.encode())
        files().dropLast(KEEP).forEach { it.delete() }
        return f
    }

    /** The kept crashes' events, scrubbed (a problem report attaches them when ticked). */
    fun events(scrub: (String) -> String, limit: Int = 3): List<JsonElement> =
        files().takeLast(limit).mapNotNull { KeptCrash.decode(it.readText())?.event() }.map { Envelope.scrubbed(it, scrub) }

    /** Sends what is due through [send]; nothing at all when [on] is false. Returns how many went. */
    suspend fun flush(on: Boolean, header: Envelope.Header, log: () -> List<String>, scrub: (String) -> String,
                      send: suspend (ByteArray) -> SendResult): Int {
        if (!on) return 0
        val state = synchronized(lock) { load() }
        val t = now()
        var sent = 0
        val lines by lazy(log)
        for (f in files()) {
            val e = state.entries.getOrPut(f.name) { Entry() }
            if (e.done) continue
            if (e.next != null && t < e.next!!) continue
            val crash = KeptCrash.decode(f.readText())
            if (crash == null) { e.done = true; e.gaveUp = true; continue }
            if (e.attempts > 0 && t - crash.at > MAX_AGE_MS) { e.done = true; e.gaveUp = true; continue }
            when (val r = send(Envelope.build(header, crash.event(), lines, scrub))) {
                SendResult.Sent -> { e.done = true; e.next = null; sent++ }
                is SendResult.Later -> {
                    e.attempts++
                    if (e.attempts > MAX_RETRIES) { e.done = true; e.gaveUp = true }
                    else e.next = t + (r.waitMs ?: WAITS[minOf(e.attempts - 1, WAITS.size - 1)])
                }
                SendResult.Refused -> break
            }
        }
        val names = files().map { it.name }.toSet()
        state.entries.keys.retainAll(names)
        synchronized(lock) { save(state) }
        return sent
    }

    /** Sent when every crash since the last notice went to the home, else Kept; null: none. */
    fun notice(): Notice? = synchronized(lock) {
        val s = load()
        val newer = files().filter { it.name > s.announced }
        val last = newer.lastOrNull() ?: return null
        s.announced = last.name
        save(s)
        if (newer.all { s.entries[it.name]?.let { e -> e.done && !e.gaveUp } == true }) Notice.Sent else Notice.Kept
    }

    internal fun wasSent(name: String): Boolean = load().entries[name]?.let { it.done && !it.gaveUp } == true

    companion object {
        const val KEEP = 5
        const val MAX_RETRIES = 3
        const val MAX_AGE_MS = 7 * 86_400_000L
        private val WAITS = listOf(3_600_000L, 86_400_000L, 2 * 86_400_000L)
        private val JSON = Json { ignoreUnknownKeys = true; encodeDefaults = true }
    }
}

/**
 * Report a problem (Epic 53 story 53.15): the app's part of a report, as the home's `reportIn`
 * reads it. The form shows all of it first: the app and device, the log of the last 30 minutes
 * (at most 2,000 lines, scrubbed), the last request ids, the kept crashes when ticked; a
 * screenshot travels only beside it, when ticked and seen (`screenshot_previewed`).
 */
object ReportPart {
    const val TEXT_MAX = 2000
    const val LINES_MAX = 2000
    const val WINDOW_MS = 30 * 60_000L
    const val PART_MAX = 512 shl 10
    const val SCREENSHOT_MAX = 1 shl 20

    /** The ring's last 30 minutes, at most 2,000 lines, scrubbed again. */
    fun lines(all: List<String>, nowMs: Long, scrub: (String) -> String): List<String> = all.filter { l ->
        val ts = Regex("\"ts\":\"([^\"]+)\"").find(l)?.groupValues?.get(1) ?: return@filter false
        val at = runCatching { Instant.parse(ts).toEpochMilli() }.getOrNull() ?: return@filter false
        at >= nowMs - WINDOW_MS && at <= nowMs + 60_000
    }.takeLast(LINES_MAX).map(scrub)

    fun part(
        category: String,
        text: String,
        earlierMs: Long?,
        lines: List<String>,
        crashes: List<JsonElement>,
        header: Envelope.Header,
        requests: List<String>,
        reference: String?,
        screenshotPreviewed: Boolean,
        scrub: (String) -> String,
    ): ByteArray {
        val reqs = requests.toMutableList()
        if (reference != null && reqs.none { it.startsWith(reference) }) reqs += reference
        var objects = lines.mapNotNull { runCatching { Json.parseToJsonElement(it) }.getOrNull() }
        while (true) {
            val body = buildJsonObject {
                put("category", category)
                put("text", scrub(text.take(TEXT_MAX)))
                earlierMs?.let { put("when", Instant.ofEpochMilli(it).toString()) }
                put("app", header.app); put("version", header.appVersion); put("platform", header.platform)
                put("os_version", header.osVersion); put("device_model", header.deviceModel); put("install_id", header.installId)
                put("request_ids", JsonArray(reqs.takeLast(20).map(::JsonPrimitive)))
                put("screenshot_previewed", screenshotPreviewed)
                put("share_with_maktaba", false)
                if (crashes.isNotEmpty()) put("crashes", JsonArray(crashes))
                put("lines", JsonArray(objects))
            }
            val b = body.toString().toByteArray()
            if (b.size <= PART_MAX || objects.isEmpty()) return b
            objects = objects.drop(maxOf(1, objects.size / 10))
        }
    }
}

