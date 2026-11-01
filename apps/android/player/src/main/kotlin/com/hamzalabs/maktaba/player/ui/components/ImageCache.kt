package com.hamzalabs.maktaba.player.ui.components

import android.app.ActivityManager
import android.content.Context
import androidx.compose.runtime.Composable
import androidx.compose.runtime.ProvidableCompositionLocal
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.platform.LocalContext
import coil3.ComponentRegistry
import coil3.ImageLoader
import coil3.SingletonImageLoader
import coil3.disk.DiskCache
import coil3.memory.MemoryCache
import coil3.network.CacheStrategy
import coil3.network.ConnectivityChecker
import coil3.network.NetworkRequest
import coil3.network.NetworkResponse
import coil3.network.okhttp.OkHttpNetworkFetcherFactory
import coil3.request.Options
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import kotlinx.coroutines.runBlocking
import okhttp3.HttpUrl
import okhttp3.Interceptor
import okhttp3.OkHttpClient
import okhttp3.Response
import okio.Path.Companion.toOkioPath
import java.io.File
import java.io.IOException
import java.util.concurrent.TimeUnit

/**
 * The Player's pictures (NFR-4): posters, backdrops and channel logos, through Coil.
 *
 * - **Decoded at display size:** every component lets Coil size the request from its layout
 *   (never `Size.ORIGINAL`), so a 160 dp card decodes a 160 dp bitmap, not the 1080p frame.
 * - **A memory cache of at most [MEMORY_MAX_BYTES]** (a quarter of the app's heap on a small phone,
 *   never more than 50 MB) and **a disk cache of at most [DISK_MAX_BYTES]**, both least recently
 *   used first out, both shared by every home this app signs in to.
 * - **A repeat visit fetches nothing within the server's `max-age`** ([MaxAgeCacheStrategy]): the
 *   poster route answers `Cache-Control: private, max-age=3600`.
 * - **The small poster in grids and rails** ([posterUrl], `?size=small`, Epic 48 G4).
 *
 * The home's pictures are behind sign-in: [HomeImageAuth] adds the user's token to this home's
 * `/api/` requests only, so a logo URL on another host never sees it.
 */
object ImageCache {
    /** NFR-4: the memory cache's ceiling. */
    const val MEMORY_MAX_BYTES: Long = 50L * 1024 * 1024

    /** NFR-4: the disk cache's ceiling. */
    const val DISK_MAX_BYTES: Long = 200L * 1024 * 1024

    /** The share of the app's heap the memory cache may take below the ceiling (a small phone). */
    const val MEMORY_HEAP_SHARE = 0.25

    /** Where the disk cache lives, under the app's cache folder (the system may clear it). */
    const val DISK_FOLDER = "images"

    /** The memory cache's size for a heap of [heapBytes]: a quarter of it, at most 50 MB. */
    fun memoryBytes(heapBytes: Long): Long = (heapBytes * MEMORY_HEAP_SHARE).toLong().coerceIn(0, MEMORY_MAX_BYTES)

    /** The memory cache's size on this device. */
    fun memoryBytes(context: Context): Long {
        val am = context.getSystemService(Context.ACTIVITY_SERVICE) as? ActivityManager
        val heapMb = am?.memoryClass ?: 128
        return memoryBytes(heapMb * 1024L * 1024L)
    }

    /** A memory cache within NFR-4's limit. */
    fun memoryCache(context: Context): MemoryCache =
        MemoryCache.Builder().maxSizeBytes(memoryBytes(context)).strongReferencesEnabled(true).build()

    /** A disk cache within NFR-4's limit, in [directory]. */
    fun diskCache(directory: File): DiskCache =
        DiskCache.Builder().directory(directory.toOkioPath()).maxSizeBytes(DISK_MAX_BYTES).build()

    @Volatile private var shared: Pair<MemoryCache, DiskCache>? = null

    /**
     * The app's one memory cache and one disk cache: two disk caches on one folder would corrupt
     * each other, and a memory cache per home would let the total pass the limit.
     */
    fun shared(context: Context): Pair<MemoryCache, DiskCache> = shared ?: synchronized(this) {
        shared ?: run {
            val app = context.applicationContext
            (memoryCache(app) to diskCache(File(app.cacheDir, DISK_FOLDER))).also { shared = it }
        }
    }

    /**
     * The image loader for [client]'s home: the shared caches, the token on the home's `/api/`
     * pictures, and the server's `max-age` honoured. Build one per signed-in home (it is cheap:
     * the caches are shared) and provide it with [LocalHomeImages]. [now], [online] and
     * [decoders] are for tests (a fake clock, no connectivity check, Robolectric's decoder).
     */
    fun loader(
        context: Context,
        client: ApiClient,
        memory: MemoryCache = shared(context).first,
        disk: DiskCache = shared(context).second,
        now: () -> Long = System::currentTimeMillis,
        online: Boolean? = null,
        decoders: ComponentRegistry.Builder.() -> Unit = {},
    ): ImageLoader {
        val http = OkHttpClient.Builder()
            .connectTimeout(15, TimeUnit.SECONDS) // NFR-15: no spinner forever
            .readTimeout(15, TimeUnit.SECONDS)
            .addInterceptor(HomeImageAuth(client))
            .build()
        return ImageLoader.Builder(context)
            .memoryCache { memory }
            .diskCache { disk }
            .components {
                decoders()
                add(
                    OkHttpNetworkFetcherFactory(
                        callFactory = { http },
                        cacheStrategy = { MaxAgeCacheStrategy(now) },
                        connectivityChecker = { ctx ->
                            when (online) {
                                null -> ConnectivityChecker(ctx)
                                else -> object : ConnectivityChecker { override fun isOnline() = online }
                            }
                        },
                    ),
                )
            }
            .build()
    }
}

/** Which copy of a poster to ask for: the small one (grids, rails, a phone) or the full one. */
enum class PosterSize { Small, Full }

/**
 * Where [videoId]'s poster loads from on [client]'s home: `/api/videos/{id}/poster`, with
 * `?size=small` for grids and rails (G4: the pipeline's 320 px copy, the full one when it has none).
 */
fun posterUrl(client: ApiClient, videoId: String, size: PosterSize = PosterSize.Small): String =
    posterUrl(client.home, client.basePath, videoId, size)

/** [posterUrl] from the home's address and base path (testable without a client). */
fun posterUrl(home: HttpUrl, basePath: String, videoId: String, size: PosterSize = PosterSize.Small): String =
    home.newBuilder()
        .encodedPath("$basePath/api/videos/")
        .addPathSegment(videoId)
        .addPathSegment("poster")
        .apply { if (size == PosterSize.Small) addQueryParameter("size", "small") }
        .build()
        .toString()

/**
 * Adds the user's token to [client]'s home's `/api/` picture requests (the poster and logo routes
 * need one), refreshing it when it is about to end and once more on a 401. Any other host, or any
 * other path on the home, goes out as it came. Runs on Coil's IO threads, so it may block.
 */
class HomeImageAuth(private val client: ApiClient) : Interceptor {
    override fun intercept(chain: Interceptor.Chain): Response {
        val request = chain.request()
        if (!isHomeApi(request.url)) return chain.proceed(request)
        val token = token { client.playbackToken(MIN_LIFE_SEC) }
        val response = chain.proceed(request.newBuilder().header("Authorization", "Bearer $token").build())
        if (response.code != 401) return response
        response.close()
        val fresh = token { client.refreshAfter(token) }
        return chain.proceed(request.newBuilder().header("Authorization", "Bearer $fresh").build())
    }

    /** Whether [url] is this home's API (same scheme, host and port; under its base path). */
    fun isHomeApi(url: HttpUrl): Boolean {
        val home = client.home
        return url.scheme == home.scheme && url.host == home.host && url.port == home.port &&
            url.encodedPath.startsWith("${client.basePath}/api/")
    }

    private fun token(get: suspend () -> String): String = try {
        runBlocking { get() }
    } catch (e: ApiException) {
        throw IOException(e.message, e)
    }

    private companion object {
        const val MIN_LIFE_SEC = 60L
    }
}

/**
 * Coil's disk cache by the server's word: a cached picture younger than its `max-age` is used
 * without asking (a repeat visit fetches nothing, NFR-4); an older one, or one with `no-cache`,
 * is fetched again; `no-store` is never written. A response with no `max-age` is kept for
 * [defaultMaxAgeSec] (a logo URL on another host may send none).
 */
class MaxAgeCacheStrategy(
    private val now: () -> Long = System::currentTimeMillis,
    private val defaultMaxAgeSec: Long = 3600,
) : CacheStrategy {
    override suspend fun read(cacheResponse: NetworkResponse, networkRequest: NetworkRequest, options: Options): CacheStrategy.ReadResult {
        val maxAge = maxAgeSec(cacheResponse.headers["Cache-Control"]) ?: defaultMaxAgeSec
        val ageMs = now() - cacheResponse.responseMillis
        return if (maxAge > 0 && ageMs in 0 until maxAge * 1000) {
            CacheStrategy.ReadResult(cacheResponse)
        } else {
            CacheStrategy.ReadResult(networkRequest)
        }
    }

    override suspend fun write(
        cacheResponse: NetworkResponse?,
        networkRequest: NetworkRequest,
        networkResponse: NetworkResponse,
        options: Options,
    ): CacheStrategy.WriteResult {
        val cc = directives(networkResponse.headers["Cache-Control"])
        if ("no-store" in cc || networkResponse.code !in 200..299) return CacheStrategy.WriteResult.DISABLED
        // the response's own clock, so its age counts from when it arrived
        return CacheStrategy.WriteResult(networkResponse.copy(responseMillis = now()))
    }

    companion object {
        /** `max-age` in seconds from a Cache-Control header; 0 for `no-cache`; null when absent. */
        fun maxAgeSec(header: String?): Long? {
            val d = directives(header)
            if ("no-cache" in d || "no-store" in d) return 0
            return d.firstNotNullOfOrNull { if (it.startsWith("max-age=")) it.removePrefix("max-age=").trim('"').toLongOrNull() else null }
        }

        private fun directives(header: String?): List<String> =
            header?.split(',')?.map { it.trim().lowercase() }?.filter { it.isNotEmpty() }.orEmpty()
    }
}

/**
 * The signed-in home's image loader ([ImageCache.loader]); the app's default loader where none is
 * provided (a preview, a screen test that brings its own).
 */
val LocalHomeImages: ProvidableCompositionLocal<ImageLoader?> = staticCompositionLocalOf { null }

/** The image loader the components draw with. */
@Composable
fun homeImageLoader(): ImageLoader = LocalHomeImages.current ?: SingletonImageLoader.get(LocalContext.current)
