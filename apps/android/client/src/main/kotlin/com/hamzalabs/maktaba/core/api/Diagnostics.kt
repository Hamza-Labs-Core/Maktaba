package com.hamzalabs.maktaba.core.api

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.put
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.MultipartBody
import okhttp3.RequestBody.Companion.toRequestBody
import java.time.Instant

// The home's diagnostics as the Remote reads them (Epic 53 stories 53.10 to 53.12, 53.15), and
// what every app sends (53.14 crash envelopes, 53.15 problem reports). The routes are the web's
// (web/src/lib/logIndex.ts, dashboards.ts, problemReports.ts); the home refuses every admin
// route to anyone else (403), whatever an app shows.

/** One error group (`GET /api/admin/errors`). */
@Serializable
data class ErrorGroup(
    val fp: String,
    val service: String = "",
    val component: String? = null,
    val code: String? = null,
    val title: String = "",
    @SerialName("first_seen") val firstSeen: String? = null,
    @SerialName("last_seen") val lastSeen: String? = null,
    val count: Long = 0,
    @SerialName("count_24h") val count24h: Long = 0,
    val users: Long = 0,
    val status: String = "new",
    @SerialName("muted_until") val mutedUntil: String? = null,
    @SerialName("last_version") val lastVersion: String? = null,
    @SerialName("last_request_id") val lastRequestId: String? = null,
    val days: List<Long> = emptyList(),
) {
    /** Its component, else its service. */
    val part: String get() = component?.takeIf { it.isNotEmpty() } ?: service
}

@Serializable
internal data class ErrorGroups(val groups: List<ErrorGroup>? = null)

/** The list's status filter: new and seen (the web's default), or one. */
enum class ErrorFilter(val query: String?) { Open("new,seen"), Resolved("resolved"), Muted("muted"), All(null) }

enum class ErrorSort(val query: String) { LastSeen("last_seen"), Count("count"), Users("users") }

/** What the admin does to a group. */
sealed interface ErrorGroupAction {
    data object Seen : ErrorGroupAction
    data class Resolve(val version: String?) : ErrorGroupAction
    /** Muted until [until] (epoch ms), or until it is resolved (null). */
    data class Mute(val until: Long?) : ErrorGroupAction

    fun body(): JsonObject = buildJsonObject {
        when (val a = this@ErrorGroupAction) {
            Seen -> put("status", "seen")
            is Resolve -> { put("status", "resolved"); a.version?.takeIf { it.isNotEmpty() }?.let { put("version", it) } }
            is Mute -> { put("status", "muted"); a.until?.let { put("muted_until", Instant.ofEpochMilli(it).toString()) } }
        }
    }
}

/** One line as the shared schema writes it (story 53.01); every field is kept. */
@Serializable(with = LogLineSerializer::class)
data class LogLine(val fields: JsonObject) {
    private fun s(k: String): String? = (fields[k] as? JsonPrimitive)?.contentOrNull
    val ts: String? get() = s("ts")
    val level: String get() = s("level") ?: "info"
    val service: String get() = s("service") ?: ""
    val component: String? get() = s("component")
    val node: String? get() = s("node")
    val msg: String get() = s("msg") ?: ""
    val requestId: String? get() = s("request_id")
    val version: String? get() = s("version") ?: s("app_version")
    val userH: String? get() = s("user_h")
    val at: Instant? get() = ts?.let { runCatching { Instant.parse(it) }.getOrNull() }
    val isError: Boolean get() = level == "error" || level == "fatal"

    /** Each field as text (objects as JSON), by name. */
    val texts: Map<String, String>
        get() = fields.mapValues { (_, v) -> if (v is JsonPrimitive) v.contentOrNull ?: "null" else v.toString() }.toSortedMap()
}

internal object LogLineSerializer : kotlinx.serialization.KSerializer<LogLine> {
    override val descriptor = JsonObject.serializer().descriptor
    override fun deserialize(decoder: kotlinx.serialization.encoding.Decoder) = LogLine(JsonObject.serializer().deserialize(decoder))
    override fun serialize(encoder: kotlinx.serialization.encoding.Encoder, value: LogLine) =
        JsonObject.serializer().serialize(encoder, value.fields)
}

/** `GET /api/admin/logs/search`: lines, each with its id ("Lines around this"). */
@Serializable
data class LogSearch(
    val lines: List<LogLine> = emptyList(),
    val ids: List<Long>? = null,
    val cursor: String? = null,
    val partial: Boolean = false,
) {
    val rows: List<Pair<Long?, LogLine>> get() = lines.mapIndexed { i, l -> ids?.getOrNull(i) to l }
}

/** `GET /api/admin/errors/{fp}`. */
@Serializable
data class ErrorGroupDetail(
    val group: ErrorGroup,
    val hours: Map<String, Long>? = null,
    val samples: LogSearch? = null,
) {
    /** The hours as (time, count), oldest first. */
    val hourly: List<Pair<Instant, Long>>
        get() = (hours ?: emptyMap()).mapNotNull { (k, v) -> runCatching { Instant.parse(k) }.getOrNull()?.let { it to v } }
            .sortedBy { it.first }
    val versions: List<String> get() = samples?.lines.orEmpty().mapNotNull { it.version }.distinct().sorted()
    val machines: List<String> get() = samples?.lines.orEmpty().mapNotNull { it.node }.distinct().sorted()
}

/** What the Logs screen asks for: words, at least a level, a window; or one request. */
data class LogQuery(
    val text: String = "",
    val level: String = "",
    val windowMinutes: Long = 24 * 60,
    val requestId: String? = null,
) {
    fun items(nowMs: Long, cursor: String?): Map<String, String> = buildMap {
        if (requestId != null) {
            put("request_id", requestId)
        } else {
            text.trim().takeIf { it.isNotEmpty() }?.let { put("q", it) }
            put("since", Instant.ofEpochMilli(nowMs - windowMinutes * 60_000).toString())
        }
        if (level.isNotEmpty()) put("level", level)
        cursor?.let { put("cursor", it) }
    }

    companion object {
        val WINDOWS = listOf(15L, 60L, 24 * 60L, 7 * 24 * 60L)

        /** A reference from an error screen (8 hex characters) or a whole request id. */
        fun requestReference(s: String): String? {
            val v = s.trim().lowercase()
            if (Regex("^[0-9a-f]{8}$").matches(v)) return v
            if (Regex("^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$").matches(v)) return v
            return null
        }
    }
}

/** Where each line is more than a second after the one before (Follow a request). */
fun gapsOverASecond(lines: List<LogLine>): List<Boolean> = lines.indices.map { i ->
    val a = lines.getOrNull(i - 1)?.at
    val b = lines[i].at
    i > 0 && a != null && b != null && b.toEpochMilli() - a.toEpochMilli() > 1000
}

/** A board's series (`GET /api/admin/dashboards/{board}`): ≤ 50, ≤ 2,000 points each. */
@Serializable
data class BoardSeries(
    val name: String,
    val labels: String? = null,
    val kind: String = "gauge",
    val points: List<List<Double>> = emptyList(),
) {
    /** The labels' values ("core.api · home"). */
    val legend: String
        get() = labels.orEmpty().split(",").mapNotNull { kv -> kv.substringAfter("=", "").takeIf { it.isNotEmpty() } }.joinToString(" · ")
}

@Serializable
data class BoardDoc(val board: String = "", val range: String = "", val series: List<BoardSeries> = emptyList()) {
    /** One panel per metric name, in the board's order. */
    val panels: List<Pair<String, List<BoardSeries>>>
        get() = series.groupBy { it.name }.toList()
}

/** The boards the Remote shows (read only). */
enum class Board(val id: String) { Overview("overview"), Storage("storage") }

val BOARD_RANGES = listOf("1h", "24h", "7d", "30d", "90d")

@Serializable
data class ReportSummary(
    val id: Int,
    val at: String? = null,
    val name: String? = null,
    val app: String = "",
    val category: String = "other",
    val status: String = "new",
    @SerialName("shared_with_maktaba") val sharedWithMaktaba: Boolean = false,
    val reply: String? = null,
)

@Serializable
internal data class ReportList(val reports: List<ReportSummary>? = null)

/** One report as an admin reads it. */
@Serializable
data class ReportDoc(
    val id: Int,
    val at: String? = null,
    val `when`: String? = null,
    val name: String? = null,
    val app: String = "",
    val version: String = "",
    val platform: String = "",
    @SerialName("os_version") val osVersion: String? = null,
    @SerialName("device_model") val device: String? = null,
    val category: String = "other",
    val text: String? = null,
    val status: String = "new",
    val reply: String? = null,
    val screenshot: Boolean = false,
    @SerialName("request_ids") val requestIds: List<String> = emptyList(),
    val lines: List<LogLine> = emptyList(),
    val server: List<LogLine> = emptyList(),
    val crashes: List<JsonElement> = emptyList(),
) {
    /** The app's lines and the home's, merged by time, oldest first (true: the home's). */
    val timeline: List<Pair<Boolean, LogLine>>
        get() = (lines.map { false to it } + server.map { true to it }).sortedBy { it.second.at ?: Instant.EPOCH }
}

@Serializable
internal data class ReportNumber(val id: Int)

@Serializable
internal data class UserNames(val users: Map<String, String>? = null)

val REPORT_CATEGORIES = listOf("playback", "live_tv", "search", "signing_in", "other")
val REPORT_STATUSES = listOf("new", "looking", "fixed", "closed")

suspend fun ApiClient.errorGroups(filter: ErrorFilter = ErrorFilter.Open, sort: ErrorSort = ErrorSort.LastSeen): List<ErrorGroup> =
    get<ErrorGroups>("/api/admin/errors", buildMap {
        put("sort", sort.query)
        filter.query?.let { put("status", it) }
    }).groups.orEmpty()

suspend fun ApiClient.errorGroup(fp: String): ErrorGroupDetail = get("/api/admin/errors/${pathSegment(fp)}")

suspend fun ApiClient.setErrorStatus(fp: String, action: ErrorGroupAction) {
    send(request("POST", "/api/admin/errors/${pathSegment(fp)}/status", body = action.body()))
}

/** A search of the log index (503 while the logs role is off or restarting). */
suspend fun ApiClient.searchLogs(q: LogQuery, cursor: String? = null, nowMs: Long = System.currentTimeMillis()): LogSearch =
    get("/api/admin/logs/search", q.items(nowMs, cursor))

suspend fun ApiClient.linesAround(id: Long): LogSearch =
    get("/api/admin/logs/around", mapOf("id" to id.toString(), "before" to "25", "after" to "25"))

suspend fun ApiClient.logUserNames(): Map<String, String> = get<UserNames>("/api/admin/logs/users").users.orEmpty()

suspend fun ApiClient.board(board: Board, range: String): BoardDoc =
    get("/api/admin/dashboards/${board.id}", mapOf("range" to range))

/** The admin's inbox, or a viewer's own reports. */
suspend fun ApiClient.problemReports(): List<ReportSummary> = get<ReportList>("/api/reports").reports.orEmpty()

suspend fun ApiClient.problemReport(id: Int): ReportDoc = get("/api/reports/$id")

suspend fun ApiClient.setReportStatus(id: Int, status: String) {
    send(request("POST", "/api/reports/$id/status", body = buildJsonObject { put("status", status) }))
}

suspend fun ApiClient.replyToReport(id: Int, reply: String) {
    send(request("POST", "/api/reports/$id/reply", body = buildJsonObject { put("reply", reply) }))
}

/** Sends a problem report: the app's [part] (JSON) and a [screenshot] only when ticked; its number. */
suspend fun ApiClient.sendProblemReport(part: ByteArray, screenshot: ByteArray?): Int {
    val body = MultipartBody.Builder().setType(MultipartBody.FORM)
        .addFormDataPart("report", "report.json", part.toRequestBody("application/json".toMediaType()))
        .apply { screenshot?.let { addFormDataPart("screenshot", "screenshot.jpg", it.toRequestBody("image/jpeg".toMediaType())) } }
        .build()
    val r = request("POST", "/api/reports").newBuilder().post(body).build()
    return decode<ReportNumber>(send(r)).id
}

/** Sends one crash envelope (200; 403 crash_reports_off; 429 with Retry-After). */
suspend fun ApiClient.sendCrashEnvelope(envelope: ByteArray) {
    val r = request("POST", "/api/diagnostics/envelope").newBuilder()
        .post(envelope.toRequestBody("application/x-sentry-envelope".toMediaType())).build()
    send(r)
}
