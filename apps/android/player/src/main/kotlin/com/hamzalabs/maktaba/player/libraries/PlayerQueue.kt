package com.hamzalabs.maktaba.player.libraries

import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.queue.ApiQueueTransport
import com.hamzalabs.maktaba.core.queue.PlayQueueModel
import com.hamzalabs.maktaba.core.queue.QueueSource
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.builtins.serializer
import kotlinx.serialization.json.Json

/*
 * Epic 56 story 56.12: the phone and tablet Player on the server play queue. Play and Shuffle
 * on a library (its sort and filters), a series, a season, a collection and a playlist make a
 * queue (PlayQueueModel, apps/android/core, shared with the TV); the player's Up next reads the
 * queue first, then the series. The library's Collections and Playlists tabs read through the
 * same authorized transport, so the shared client stays as it is.
 */

/** One play queue per home (the client): the one this Player plays. */
object PlayerQueues {
    private val byHome = mutableMapOf<String, PlayQueueModel>()

    fun of(client: ApiClient): PlayQueueModel = synchronized(byHome) {
        byHome.getOrPut(client.home.toString()) { PlayQueueModel(ApiQueueTransport(client), "phone-android") }
    }
}

@Serializable
data class PlayerCollection(
    val kind: String,
    val id: String,
    val name: String = "",
    val videos: Long? = null,
    @SerialName("poster_video_ids") val posters: List<String> = emptyList(),
    @SerialName("source_kind") val sourceKind: String? = null,
) {
    val queueSource: QueueSource get() = QueueSource("collection", if (kind == "topic" || kind == "clip") "$kind:$id" else id)
}

@Serializable
data class PlayerPlaylist(
    val id: String,
    val name: String = "",
    val videos: Long = 0,
    @SerialName("poster_video_ids") val posters: List<String> = emptyList(),
    val kind: String = "manual",
)

@Serializable
private data class Items<T>(val items: List<T> = emptyList())

/** The library view's reads and the smart playlist's write, through the queue's transport. */
class PlayerLibraryApi(client: ApiClient) {
    private val t = ApiQueueTransport(client)
    private val json = Json { ignoreUnknownKeys = true; explicitNulls = false; coerceInputValues = true }

    private suspend fun get(path: String): String {
        val (code, body) = t.send("GET", path, null)
        if (code !in 200..299) throw com.hamzalabs.maktaba.core.api.ApiException.Status(code, body.take(500))
        return body
    }

    suspend fun collections(library: String): List<PlayerCollection> =
        json.decodeFromString(Items.serializer(PlayerCollection.serializer()), get("/api/libraries/$library/collections?limit=200")).items

    suspend fun playlists(library: String): List<PlayerPlaylist> =
        json.decodeFromString(Items.serializer(PlayerPlaylist.serializer()), get("/api/me/playlists?library=$library")).items

    /** Saves the grid's filters and sort as a smart playlist (the filter sheet, story 56.09). */
    suspend fun saveSmart(name: String, library: String, filters: Map<String, String>, limit: Int = 200) {
        val f = filters.filterKeys { it != "sort" && it != "order" }
        val rules = buildString {
            append("{\"libraries\":[").append(json.encodeToString(String.serializer(), library)).append("],\"filters\":")
            append(json.encodeToString(kotlinx.serialization.builtins.MapSerializer(String.serializer(), String.serializer()), f))
            filters["sort"]?.let { append(",\"sort\":").append(json.encodeToString(String.serializer(), it)) }
            filters["order"]?.let { append(",\"order\":").append(json.encodeToString(String.serializer(), it)) }
            append(",\"limit\":").append(limit).append('}')
        }
        val body = "{\"name\":${json.encodeToString(String.serializer(), name)},\"kind\":\"smart\",\"smart_query\":$rules}"
        val (code, text) = t.send("POST", "/api/me/playlists", body)
        if (code !in 200..299) throw com.hamzalabs.maktaba.core.api.ApiException.Status(code, text.take(500))
    }

    @Suppress("unused")
    private val ids = ListSerializer(String.serializer())
}
