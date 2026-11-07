package com.hamzalabs.maktaba.core.crash

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonArray
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import java.time.Instant
import java.util.UUID

/**
 * Crash reports as the home takes them (Epic 53 story 53.14): a Sentry envelope (README D5)
 * with the person's consent in its header (`consent: crash_reports_to_home`; the home answers
 * 403 without it), one `event` (the crash) and one `attachment` (the app's log ring, at most
 * 512 KB). The whole envelope stays under 1 MB. Every string goes through [scrub] (the shared
 * redaction rules, 53.01) before it leaves the device; the home scrubs again.
 */
object Envelope {
    const val CONSENT = "crash_reports_to_home"
    const val MAX_BYTES = 1 shl 20
    const val MAX_ATTACHMENT = 512 shl 10
    const val MAX_FRAMES = 100

    /** The device's side of the header. */
    data class Header(
        val app: String,
        val appVersion: String,
        val platform: String,
        val osVersion: String,
        val deviceModel: String,
        val installId: String,
    )

    fun newEventId(): String = UUID.randomUUID().toString().replace("-", "")

    /** One envelope: the header, [event] and the newest [log] lines that fit. */
    fun build(h: Header, event: JsonObject, log: List<String>, scrub: (String) -> String, eventId: String = newEventId()): ByteArray {
        var e = JsonObject(scrubbed(event, scrub).jsonObject + ("event_id" to JsonPrimitive(eventId)))
        val header = buildJsonObject {
            put("event_id", eventId); put("consent", CONSENT); put("app", h.app); put("app_version", h.appVersion)
            put("platform", h.platform); put("os_version", h.osVersion); put("device_model", h.deviceModel)
            put("install_id", h.installId)
        }.toString().toByteArray()
        var eventBytes = e.toString().toByteArray()
        if (header.size + eventBytes.size + 256 > MAX_BYTES) {
            e = trimmed(e, 20)
            eventBytes = e.toString().toByteArray()
        }
        val room = minOf(MAX_ATTACHMENT, MAX_BYTES - header.size - eventBytes.size - 256).coerceAtLeast(0)
        val attachment = tail(log.map(scrub), room)
        val out = java.io.ByteArrayOutputStream()
        fun line(b: ByteArray) { out.write(b); out.write('\n'.code) }
        line(header)
        line(buildJsonObject { put("type", "event"); put("length", eventBytes.size) }.toString().toByteArray())
        line(eventBytes)
        if (attachment.isNotEmpty()) {
            line(buildJsonObject {
                put("type", "attachment"); put("length", attachment.size); put("filename", "app.ndjson")
                put("content_type", "application/x-ndjson")
            }.toString().toByteArray())
            line(attachment)
        }
        return out.toByteArray()
    }

    /** The newest lines that fit in [limit] bytes, oldest first, joined by newlines. */
    fun tail(lines: List<String>, limit: Int): ByteArray {
        val kept = ArrayDeque<ByteArray>()
        var size = 0
        for (l in lines.asReversed()) {
            val b = l.toByteArray()
            if (size + b.size + 1 > limit) break
            kept.addFirst(b)
            size += b.size + 1
        }
        val out = java.io.ByteArrayOutputStream()
        kept.forEachIndexed { i, b -> if (i > 0) out.write('\n'.code); out.write(b) }
        return out.toByteArray()
    }

    /** Strings scrubbed and sensitive keys masked, all the way down. */
    fun scrubbed(v: JsonElement, scrub: (String) -> String, sensitive: (String) -> Boolean = ::sensitiveKey): JsonElement = when (v) {
        is JsonObject -> JsonObject(v.mapValues { (k, x) -> if (sensitive(k) && x is JsonPrimitive && x.isString) JsonPrimitive("***REDACTED***") else scrubbed(x, scrub, sensitive) })
        is JsonArray -> JsonArray(v.map { scrubbed(it, scrub, sensitive) })
        is JsonPrimitive -> if (v.isString) JsonPrimitive(scrub(v.content)) else v
    }

    private val SENSITIVE = setOf("password", "token", "access_token", "refresh_token", "authorization", "cookie", "secret")
    private fun sensitiveKey(k: String) = k.lowercase() in SENSITIVE

    private fun trimmed(e: JsonObject, n: Int): JsonObject {
        val ex = e["exception"] as? JsonObject ?: return e
        val values = (ex["values"] as? JsonArray)?.map { v ->
            val o = v as? JsonObject ?: return@map v
            val st = o["stacktrace"] as? JsonObject ?: return@map v
            val frames = (st["frames"] as? JsonArray)?.takeLast(n) ?: return@map v
            JsonObject(o + ("stacktrace" to JsonObject(st + ("frames" to JsonArray(frames)))))
        } ?: return e
        return JsonObject(e + ("exception" to JsonObject(ex + ("values" to JsonArray(values)))))
    }

    private val JsonElement.jsonObject: JsonObject get() = this as JsonObject
}

/**
 * A crash kept on the device, as the outbox sends it: a Java or Kotlin crash ([CrashLog]'s
 * report), or a process exit Android recorded (an ANR, a native crash, a low-memory kill; API
 * 30+). Frames are top first.
 */
@Serializable
data class KeptCrash(
    val kind: String = KIND,
    val at: Long,
    val type: String,
    val reason: String = "",
    val frames: List<String> = emptyList(),
    val appVersion: String? = null,
    val osVersion: String? = null,
    val device: String? = null,
    /** an ANR's or native crash's trace, at most 256 KB */
    val trace: String? = null,
    /** "uncaught", "anr", "native", "low_memory" */
    val mechanism: String = "uncaught",
) {
    /** The Sentry event; a frame of the app's own package ([ownPackage]) is "in app". */
    fun event(ownPackage: String = "com.hamzalabs.maktaba"): JsonObject = buildJsonObject {
        put("platform", "java")
        put("level", "fatal")
        put("timestamp", Instant.ofEpochMilli(at).toString())
        appVersion?.let { put("release", it) }
        put("exception", buildJsonObject {
            put("values", buildJsonArray {
                add(buildJsonObject {
                    put("type", type)
                    put("value", reason.ifEmpty { type })
                    put("mechanism", buildJsonObject { put("type", mechanism); put("handled", false) })
                    put("stacktrace", buildJsonObject {
                        put("frames", buildJsonArray {
                            // oldest first: the home groups on the last own frame
                            frames.take(Envelope.MAX_FRAMES).asReversed().forEach { f ->
                                add(buildJsonObject {
                                    put("function", f.substringBefore('('))
                                    put("module", f.substringBefore('(').substringBeforeLast('.', ""))
                                    put("in_app", f.startsWith(ownPackage))
                                })
                            }
                        })
                    })
                })
            })
        })
        put("contexts", buildJsonObject {
            osVersion?.let { put("os", buildJsonObject { put("version", it) }) }
            device?.let { put("device", buildJsonObject { put("model", it) }) }
        })
        trace?.let { put("extra", buildJsonObject { put("trace", it.take(TRACE_MAX)) }) }
    }

    fun encode(): String = JSON.encodeToString(serializer(), this)

    companion object {
        const val KIND = "maktaba.crash"
        const val TRACE_MAX = 256 * 1024
        private val JSON = Json { ignoreUnknownKeys = true; encodeDefaults = true }

        fun decode(text: String): KeptCrash? = runCatching { JSON.decodeFromString(serializer(), text) }.getOrNull()?.takeIf { it.kind == KIND }

        /**
         * A crash from [CrashLog]'s `last-crash.txt`: the `app:`, `os:` and `at:` lines, then the
         * redacted stack (`java.lang.IllegalStateException` and `\tat com.x.Y.f(Y.kt:12)` lines;
         * the messages were never kept). The type is the deepest cause's class.
         */
        fun fromCrashLog(text: String, device: String?): KeptCrash? {
            val lines = text.lines()
            fun field(k: String) = lines.firstOrNull { it.startsWith("$k: ") }?.substringAfter(": ")
            val at = field("at")?.let { runCatching { Instant.parse(it).toEpochMilli() }.getOrNull() } ?: return null
            val stack = lines.dropWhile { !it.startsWith("thread: ") }.drop(1)
            val types = stack.filter { !it.startsWith("\t") && it.isNotBlank() }
                .map { it.removePrefix("Caused by: ").substringBefore(':').trim() }
            val type = types.lastOrNull() ?: return null
            // the deepest cause's frames: where it went wrong
            val lastHeader = stack.indexOfLast { !it.startsWith("\t") && it.isNotBlank() }
            val frames = stack.drop(lastHeader + 1).filter { it.trimStart().startsWith("at ") }.map { it.trim().removePrefix("at ") }
            return KeptCrash(
                at = at, type = type, frames = frames.ifEmpty { stack.filter { it.trimStart().startsWith("at ") }.map { it.trim().removePrefix("at ") } },
                appVersion = field("app")?.substringAfter(' '), osVersion = field("os"), device = device,
            )
        }
    }
}
