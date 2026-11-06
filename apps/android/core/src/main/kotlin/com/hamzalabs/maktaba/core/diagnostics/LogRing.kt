package com.hamzalabs.maktaba.core.diagnostics

import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import java.io.File
import java.time.Instant
import java.time.ZoneOffset
import java.time.format.DateTimeFormatter

/**
 * The app's own log lines, kept on the device (Epic 53 story 53.01): each [AppLog] line also goes,
 * as a shared-schema line (`shared/log/schema/line.schema.json`), to `files/logs/app.ndjson`. At
 * 512 KB that file becomes `app.1.ndjson` (the older one goes), so the ring stays under 1 MB with
 * the newest lines kept. What a problem report attaches (story 53.15). Values are scrubbed with the
 * shared list; nothing names a person.
 */
class LogRing(val directory: File, private val base: Map<String, String>) {
    val current: File get() = File(directory, "app.ndjson")
    val previous: File get() = File(directory, "app.1.ndjson")
    private var size = -1L

    /** Appends one line: [msg] a snake_case event name, [attrs] scrubbed. Never throws. */
    @Synchronized
    fun write(level: String, msg: String, attrs: Map<String, Any?> = emptyMap(), at: Instant = Instant.now()) {
        try {
            val fields = LinkedHashMap<String, JsonElement>()
            fields["ts"] = JsonPrimitive(TS.format(at))
            fields["level"] = JsonPrimitive(level)
            fields["msg"] = JsonPrimitive(msg)
            base.forEach { (k, v) -> fields[k] = JsonPrimitive(v) }
            if (attrs.isNotEmpty()) fields["attrs"] = JsonObject(Redact.attrs(attrs).mapValues { json(it.value) })
            val bytes = (JsonObject(fields).toString() + "\n").toByteArray(Charsets.UTF_8)
            directory.mkdirs()
            if (size < 0) size = if (current.exists()) current.length() else 0
            if (size + bytes.size > FILE_LIMIT) {
                previous.delete()
                current.renameTo(previous)
                size = 0
            }
            current.appendBytes(bytes)
            size += bytes.size
        } catch (_: Exception) {
            // the disk is full or gone: logcat still has the line
        }
    }

    /** Both files' lines, oldest first. */
    @Synchronized
    fun lines(): List<String> = listOf(previous, current).filter { it.exists() }.flatMap { f ->
        f.readLines(Charsets.UTF_8).filter { it.isNotBlank() }
    }

    companion object {
        const val FILE_LIMIT = 512 * 1024
        private val TS: DateTimeFormatter = DateTimeFormatter.ofPattern("yyyy-MM-dd'T'HH:mm:ss.SSS'Z'").withZone(ZoneOffset.UTC)

        private fun json(v: Any?): JsonElement = when (v) {
            null -> JsonNull
            is Boolean -> JsonPrimitive(v)
            is Number -> JsonPrimitive(v)
            else -> JsonPrimitive(v.toString())
        }

        /** The schema's `service` for an app package: android.tv, android.remote or android.player. */
        fun service(packageName: String, tv: Boolean): String = when {
            tv -> "android.tv"
            packageName.contains("remote") -> "android.remote"
            else -> "android.player"
        }
    }
}
