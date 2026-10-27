package com.hamzalabs.maktaba.core.api

import kotlinx.coroutines.runBlocking
import okhttp3.Interceptor
import okhttp3.Response
import java.io.IOException

/**
 * Hands a home's stream to the player with the user's token on every request.
 *
 * The home's web port serves `/stream/` only to a signed-in caller: the browser sends its
 * cookie, an app sends `Authorization: Bearer <access token>`. The access token lasts 15
 * minutes, so a header set once when the video opens would start failing mid-way. ExoPlayer
 * reads every playlist, segment and range through OkHttp, so each request gets the token as it
 * is now (refreshed when it has under [minLifeSec] seconds left), and a 401 refreshes once and
 * retries. (The Apple apps need a loopback proxy for the same; AVPlayer takes headers once.)
 *
 * Only this home's `/stream/` paths get the token: a playlist naming another host never sees it.
 * Runs on the player's loader threads, never the main thread, so it may block on a refresh.
 */
class StreamAuthInterceptor(
    private val client: ApiClient,
    private val minLifeSec: Long = 60,
) : Interceptor {
    override fun intercept(chain: Interceptor.Chain): Response {
        val request = chain.request()
        if (!client.isStream(request.url)) return chain.proceed(request)
        val token = token { client.playbackToken(minLifeSec) }
        val response = chain.proceed(request.newBuilder().header("Authorization", "Bearer $token").build())
        if (response.code != 401) return response
        response.close()
        val fresh = token { client.refreshAfter(token) }
        return chain.proceed(request.newBuilder().header("Authorization", "Bearer $fresh").build())
    }

    /** A token, or an IOException the player reports and retries like any network failure. */
    private fun token(get: suspend () -> String): String = try {
        runBlocking { get() }
    } catch (e: ApiException) {
        throw IOException(e.message, e)
    }
}
