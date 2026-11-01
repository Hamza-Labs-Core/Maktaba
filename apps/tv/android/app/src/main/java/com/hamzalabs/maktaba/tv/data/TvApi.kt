package com.hamzalabs.maktaba.tv.data

import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.BODY_KEPT
import com.hamzalabs.maktaba.core.api.LiveApi
import com.hamzalabs.maktaba.core.api.Page
import com.hamzalabs.maktaba.core.diagnostics.AppLog
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.async
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.serialization.json.Json
import okhttp3.Call
import okhttp3.Callback
import okhttp3.HttpUrl
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import java.io.IOException
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException

/**
 * What the TV reads beyond the shared core's [ApiClient]: the home's rails, the video list and
 * detail with the names people read and where they stopped. Signed in through the core: each
 * request carries the core's current token, and a 401 takes the core's single refresh
 * ([ApiClient.refreshAfter]) and tries once more.
 */
class TvApi(
    val client: ApiClient,
    private val http: OkHttpClient = ApiClient.defaultHttp,
) {
    val home: HttpUrl get() = client.home

    /** Live TV on this home (the shared client's, Epic 48.9): what is on now, the guide, the streams. */
    val live: LiveApi by lazy { LiveApi(client, http) }

    /**
     * Whether this user may watch any channel: the Live TV tab shows only then. A home that does
     * not answer, or has no Live TV, counts as none (the tab stays hidden; the others still work).
     */
    suspend fun hasChannels(): Boolean = try {
        live.now().channels.isNotEmpty()
    } catch (e: CancellationException) {
        throw e
    } catch (e: Exception) {
        AppLog.failure("tv live channels", e)
        false
    }

    suspend fun recommendations(): Recommendations = get("/api/recommendations")

    /**
     * The most recently added first; a search, by best match then the most recently added (the
     * home's sorts are updated_at, created_at or title, never `-…`).
     */
    suspend fun videos(library: String? = null, query: String? = null, cursor: String? = null): Page<TvVideo> =
        get("/api/videos", ApiClient.videoListQuery(library, query, cursor))

    suspend fun video(id: String): TvVideo = get("/api/videos/$id")

    /**
     * The rail's videos with their names (a rail item carries only an id), in the rail's order;
     * a video that cannot be read any more (removed, its library's grant lost) is left out.
     */
    suspend fun railVideos(rail: Rail, limit: Int = 20): List<Pair<RailItem, TvVideo>> = coroutineScope {
        rail.items.take(limit)
            .map { item ->
                async {
                    runCatching { item to video(item.videoId) }
                        .onFailure { if (it is CancellationException) throw it else AppLog.failure("tv rail video", it) }
                        .getOrNull()
                }
            }
            .mapNotNull { it.await() }
    }

    private suspend inline fun <reified T> get(path: String, query: Map<String, String> = emptyMap()): T {
        val url = home.newBuilder().encodedPath(path).apply {
            query.toSortedMap().forEach { (k, v) -> addQueryParameter(k, v) }
        }.build()
        val body = authorized(url)
        return try {
            json.decodeFromString<T>(body)
        } catch (e: Exception) {
            throw ApiException.Decoding(e.message ?: e.toString())
        }
    }

    private suspend fun authorized(url: HttpUrl): String {
        val token = client.playbackToken(30)
        val (code, body) = exchange(http, request(url, token))
        if (code != 401) return checked(code, body)
        val fresh = client.refreshAfter(token)
        val (again, body2) = exchange(http, request(url, fresh))
        if (again == 401) throw ApiException.SignedOut
        return checked(again, body2)
    }

    private fun request(url: HttpUrl, token: String) = Request.Builder().url(url)
        .header("Accept", "application/json")
        .header("X-Maktaba-Client", "native")
        .header("Authorization", "Bearer $token")
        .build()

    companion object {
        val json = Json {
            ignoreUnknownKeys = true
            explicitNulls = false
            coerceInputValues = true
        }

        internal val JSON = "application/json".toMediaType()

        fun checked(code: Int, body: String): String {
            if (code !in 200..299) throw ApiException.Status(code, body.take(BODY_KEPT))
            return body
        }

        /** Sends [r] and reads the answer on OkHttp's thread (the caller may be the main thread). */
        suspend fun exchange(http: OkHttpClient, r: Request): Pair<Int, String> = suspendCancellableCoroutine { cont ->
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

        fun post(url: HttpUrl, body: String): Request = Request.Builder().url(url)
            .header("Accept", "application/json")
            .header("X-Maktaba-Client", "native")
            .post(body.toRequestBody(JSON))
            .build()
    }
}
