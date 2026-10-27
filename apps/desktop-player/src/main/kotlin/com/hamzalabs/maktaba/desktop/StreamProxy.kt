package com.hamzalabs.maktaba.desktop

import com.hamzalabs.maktaba.core.api.ApiClient
import com.sun.net.httpserver.HttpExchange
import com.sun.net.httpserver.HttpServer
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.OkHttpClient
import okhttp3.Request
import java.io.IOException
import java.net.InetAddress
import java.net.InetSocketAddress
import java.security.SecureRandom
import java.util.Base64
import java.util.concurrent.ExecutorService
import java.util.concurrent.Executors

/**
 * Hands one home's stream to JavaFX's player, which cannot send a header: it plays
 * `http://127.0.0.1:<port>/<secret>/stream/…`, and each request goes on to the home's
 * `/stream/…` with the user's token as it is now (the client's `streamHttp`, its
 * `StreamAuthInterceptor`: refreshed before it runs out, once more on a 401). The Apple apps
 * do the same for AVPlayer.
 *
 * - Loopback only, under a random secret path: another program or user on this computer
 *   cannot borrow the token by guessing the address.
 * - Only this home's `/stream/` is fetched (`ApiClient.isStream`): nothing else gets the token.
 * - A playlist naming the home's own address is rewritten to the proxy's, so every segment
 *   comes back through it; any other host in a playlist is left alone and never sees the token.
 */
class StreamProxy(
    private val client: ApiClient,
    private val http: OkHttpClient = client.streamHttp(),
) : AutoCloseable {
    private val secret = ByteArray(18).also(SecureRandom()::nextBytes)
        .let { Base64.getUrlEncoder().withoutPadding().encodeToString(it) }
    private val pool: ExecutorService = Executors.newCachedThreadPool { r ->
        Thread(r, "maktaba-stream-proxy").apply { isDaemon = true }
    }
    private val server: HttpServer = HttpServer.create(InetSocketAddress(InetAddress.getLoopbackAddress(), 0), 16).apply {
        createContext("/") { ex -> runCatching { serve(ex) }.onFailure { runCatching { ex.close() } } }
        executor = pool
        start()
    }

    private val home: HttpUrl = client.home
    /** The home's origin and web base path, as playlists may spell it. */
    private val homePrefix = home.newBuilder().encodedPath(client.basePath.ifEmpty { "/" }).build().toString().trimEnd('/')

    /** Where the proxy answers: `http://127.0.0.1:<port>/<secret>`. */
    val origin: String get() = "http://${server.address.address.hostAddress}:${server.address.port}/$secret"

    /** The proxy's address for a stream URL of this home, or null for anything else. */
    fun local(url: HttpUrl): String? {
        if (!client.isStream(url)) return null
        val path = url.encodedPath.removePrefix(client.basePath)
        return origin + path + (url.encodedQuery?.let { "?$it" } ?: "")
    }

    /** The home's URL for a request the proxy received, or null when it is not one of its streams. */
    internal fun remote(rawPath: String, rawQuery: String?): HttpUrl? {
        val prefix = "/$secret/"
        if (!rawPath.startsWith(prefix)) return null
        val rest = rawPath.substring(prefix.length - 1)
        val url = "$homePrefix$rest${rawQuery?.let { "?$it" } ?: ""}".toHttpUrlOrNull() ?: return null
        return url.takeIf { client.isStream(it) }
    }

    private fun serve(ex: HttpExchange) {
        ex.use {
            val method = ex.requestMethod
            val target = remote(ex.requestURI.rawPath, ex.requestURI.rawQuery)
            if (target == null || (method != "GET" && method != "HEAD")) {
                ex.sendResponseHeaders(404, -1)
                return
            }
            val req = Request.Builder().url(target).method(method, null).apply {
                ex.requestHeaders.getFirst("Range")?.let { header("Range", it) }
            }.build()
            val resp = try {
                http.newCall(req).execute()
            } catch (e: IOException) {
                ex.sendResponseHeaders(502, -1)
                return
            }
            resp.use {
                for (h in PASSED) resp.header(h)?.let { ex.responseHeaders.set(h, it) }
                val body = resp.body
                if (method == "HEAD") {
                    ex.sendResponseHeaders(resp.code, -1)
                    return
                }
                if (isPlaylist(target, resp.header("Content-Type"))) {
                    val text = rewrite(body.string()).toByteArray()
                    ex.responseHeaders.remove("Content-Length")
                    ex.sendResponseHeaders(resp.code, if (text.isEmpty()) -1 else text.size.toLong())
                    if (text.isNotEmpty()) ex.responseBody.write(text)
                    return
                }
                val length = body.contentLength()
                ex.sendResponseHeaders(resp.code, if (length == 0L) -1 else if (length > 0) length else 0)
                if (length != 0L) body.byteStream().use { it.copyTo(ex.responseBody, 64 * 1024) }
            }
        }
    }

    /** A playlist's lines naming the home's own address now name the proxy's. */
    internal fun rewrite(playlist: String): String = playlist.lineSequence().joinToString("\n") { line ->
        if (line.startsWith(homePrefix + "/")) origin + line.removePrefix(homePrefix) else line
    }

    override fun close() {
        server.stop(0)
        pool.shutdownNow()
    }

    private companion object {
        val PASSED = listOf("Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "Cache-Control")

        fun isPlaylist(url: HttpUrl, type: String?): Boolean =
            url.encodedPath.endsWith(".m3u8") || type?.lowercase()?.contains("mpegurl") == true
    }
}
