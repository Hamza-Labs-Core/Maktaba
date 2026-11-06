package com.hamzalabs.maktaba.core.queue

import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.serialization.SerialName
import kotlinx.serialization.builtins.serializer
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonObject
import okhttp3.Call
import okhttp3.Callback
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import java.io.IOException
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException

/*
 * Epic 56 stories 56.02, 56.11 and 56.12: the server play queue (story 56.01) as the TV
 * and the phone Players use it. Every Play and Shuffle makes a queue on the home
 * (POST /api/me/play-queues) from a source; the app keeps the window of items around the
 * current one, moves with PUT …/current at each change (409 stale_queue: another device
 * moved it, the answer's state is taken) and reads the next window when within
 * [PlayQueueModel.REFILL_WITHIN] items of its edge.
 */

/** Where a queue's videos come from: library (with the grid's filters), series, season, collection, playlist, video. */
@Serializable
data class QueueSource(
    val kind: String,
    val id: String,
    val filters: Map<String, String>? = null,
)

@Serializable
data class QueueItem(
    val pos: Int,
    val origin: String = "source",
    val id: String,
    val title: String? = null,
    val filename: String = "",
    @SerialName("library_id") val libraryId: String = "",
    @SerialName("duration_sec") val durationSec: Double? = null,
    @SerialName("position_sec") val positionSec: Double? = null,
    val completed: Boolean? = null,
    @SerialName("series_id") val seriesId: String? = null,
    @SerialName("series_name") val seriesName: String? = null,
) {
    val name: String get() = title?.takeIf { it.isNotBlank() } ?: filename
}

@Serializable
data class QueueDoc(
    val id: String,
    val source: QueueSource,
    val shuffle: Boolean = false,
    @SerialName("current_pos") val currentPos: Int = 0,
    val version: Int = 1,
    @SerialName("device_id") val deviceId: String? = null,
    @SerialName("source_done") val sourceDone: Boolean = false,
    @SerialName("total_estimate") val totalEstimate: Int = 0,
    @SerialName("used_at") val usedAt: String? = null,
    val items: List<QueueItem> = emptyList(),
) {
    val current: QueueItem? get() = items.firstOrNull { it.pos == currentPos }
    fun after(pos: Int): QueueItem? = items.filter { it.pos > pos }.minByOrNull { it.pos }
    fun before(pos: Int): QueueItem? = items.filter { it.pos < pos }.maxByOrNull { it.pos }
}

private val STR = String.serializer()

/** One call to the home: the status and the body. */
fun interface QueueTransport {
    suspend fun send(method: String, path: String, body: String?): Pair<Int, String>
}

/** The empty source (422 empty_source): nothing to play here. */
class EmptySource : Exception("empty_source")

/** The queue's routes over a [QueueTransport]. */
class PlayQueueModel(private val transport: QueueTransport, private val deviceId: String) {
    /** The queue being played, its window around the current item; null before any Play. */
    var doc: QueueDoc? = null
        private set

    /** Set when a move found the queue changed on another device (the app says so once). */
    var staleSeen: Boolean = false

    private suspend fun call(method: String, path: String, body: String? = null): String {
        val (code, text) = transport.send(method, path, body)
        if (code == 422 && text.contains("empty_source")) throw EmptySource()
        if (code !in 200..299) throw ApiException.Status(code, text.take(500))
        return text
    }

    /** Makes a queue from [source]; the item to play first. */
    suspend fun create(source: QueueSource, shuffle: Boolean, startVideoId: String? = null): QueueItem? {
        val body = buildString {
            append("{\"source\":").append(json.encodeToString(QueueSource.serializer(), source))
            append(",\"shuffle\":").append(shuffle)
            if (startVideoId != null) append(",\"start_video_id\":").append(json.encodeToString(STR, startVideoId))
            append(",\"device_id\":").append(json.encodeToString(STR, deviceId)).append('}')
        }
        val d = json.decodeFromString(QueueDoc.serializer(), call("POST", PATH, body))
        doc = d
        return d.current ?: d.items.firstOrNull()
    }

    /** Continues a queue read elsewhere (Home's Continue your queue). */
    fun resume(d: QueueDoc): QueueItem? {
        doc = d
        return d.current
    }

    /** The caller's latest queue from any device, or null (204). */
    suspend fun fetchCurrent(): QueueDoc? {
        val (code, text) = transport.send("GET", "$PATH/current", null)
        if (code == 204 || text.isBlank()) return null
        if (code !in 200..299) throw ApiException.Status(code, text.take(500))
        return json.decodeFromString(QueueDoc.serializer(), text)
    }

    /** The item after the current one (reading on when the window runs out), without moving. */
    suspend fun peekNext(): QueueItem? {
        val d = doc ?: return null
        d.after(d.currentPos)?.let { return it }
        if (d.sourceDone && d.items.none { it.pos > d.currentPos }) return refill(d)?.after(d.currentPos)
        return refill(d)?.after(d.currentPos)
    }

    /** Moves to the next item; null at the end. */
    suspend fun next(): QueueItem? {
        val n = peekNext() ?: return null
        return moveTo(n.pos)
    }

    /** Moves to the item before; null at the start. */
    suspend fun previous(): QueueItem? {
        val d = doc ?: return null
        val p = d.before(d.currentPos) ?: refill(d, before = 10, after = 0)?.before(d.currentPos) ?: return null
        return moveTo(p.pos)
    }

    /** Sets the current item to [pos] for the version held (a stale one takes the latest). */
    suspend fun moveTo(pos: Int): QueueItem? {
        val d = doc ?: return null
        val (code, text) = transport.send("PUT", "$PATH/${d.id}/current", "{\"pos\":$pos,\"version\":${d.version}}")
        if (code == 409) {
            staleSeen = true
            val latest = runCatching { json.parseToJsonElement(text).jsonObject["queue"] as JsonObject }.getOrNull()
            doc = latest?.let { json.decodeFromJsonElement(QueueDoc.serializer(), it) } ?: refill(d)
            return doc?.current
        }
        if (code !in 200..299) throw ApiException.Status(code, text.take(500))
        val moved = json.decodeFromString(QueueDoc.serializer(), text)
        doc = moved
        // near the edge of the window: read the next one now, so Next never waits
        val ahead = moved.items.count { it.pos > moved.currentPos }
        if (ahead < REFILL_WITHIN && !moved.sourceDone) refill(moved)
        return doc?.current
    }

    /** Play next ([where] "next") or Add to queue ("end"). */
    suspend fun add(videoIds: List<String>, where: String) {
        val d = doc ?: run {
            val first = videoIds.firstOrNull() ?: return
            create(QueueSource("video", first), shuffle = false)
            val rest = videoIds.drop(1)
            if (rest.isEmpty()) return
            doc ?: return
        }
        val ids = json.encodeToString(kotlinx.serialization.builtins.ListSerializer(STR), videoIds)
        doc = json.decodeFromString(QueueDoc.serializer(), call("POST", "$PATH/${d.id}/items", "{\"video_ids\":$ids,\"where\":\"$where\"}"))
    }

    /** Shuffle again: a new order after the current item. */
    suspend fun reshuffle() {
        val d = doc ?: return
        if (!d.shuffle) return
        doc = json.decodeFromString(QueueDoc.serializer(), call("POST", "$PATH/${d.id}/reshuffle"))
    }

    private suspend fun refill(d: QueueDoc, before: Int = 10, after: Int = 30): QueueDoc? = runCatching {
        val w = json.decodeFromString(
            QueueDoc.serializer(),
            call("GET", "$PATH/${d.id}?around=${d.currentPos}&before=$before&after=$after"),
        )
        doc = w
        w
    }.getOrNull()

    companion object {
        const val PATH = "/api/me/play-queues"
        /** The window is read again when fewer items than this are left after the current one. */
        const val REFILL_WITHIN = 5
        val json = Json { ignoreUnknownKeys = true; explicitNulls = false; coerceInputValues = true }
    }
}


/** The transport over the shared client: the user's token, one refresh on a 401. */
class ApiQueueTransport(
    private val client: ApiClient,
    private val http: OkHttpClient = ApiClient.defaultHttp,
) : QueueTransport {
    override suspend fun send(method: String, path: String, body: String?): Pair<Int, String> {
        val url = client.home.resolve(path) ?: throw ApiException.Transport("bad path $path")
        val token = client.playbackToken(30)
        val first = exchange(request(url, method, body, token))
        if (first.first != 401) return first
        val fresh = client.refreshAfter(token)
        val again = exchange(request(url, method, body, fresh))
        if (again.first == 401) throw ApiException.SignedOut
        return again
    }

    private fun request(url: okhttp3.HttpUrl, method: String, body: String?, token: String): Request =
        Request.Builder().url(url)
            .header("Accept", "application/json")
            .header("X-Maktaba-Client", "native")
            .header("Authorization", "Bearer $token")
            .method(method, body?.toRequestBody(JSON) ?: if (method == "POST" || method == "PUT") "".toRequestBody(JSON) else null)
            .build()

    private suspend fun exchange(r: Request): Pair<Int, String> = suspendCancellableCoroutine { cont ->
        val call = http.newCall(r)
        cont.invokeOnCancellation { call.cancel() }
        call.enqueue(object : Callback {
            override fun onFailure(call: Call, e: IOException) {
                cont.resumeWithException(ApiException.Transport(e.message ?: e.toString()))
            }

            override fun onResponse(call: Call, response: Response) {
                val read = runCatching { response.use { it.code to it.body.string() } }
                read.onSuccess { cont.resume(it) }
                read.onFailure { cont.resumeWithException(ApiException.Transport(it.message ?: it.toString())) }
            }
        })
    }

    private companion object {
        val JSON = "application/json".toMediaType()
    }
}
