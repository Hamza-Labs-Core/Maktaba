package com.hamzalabs.maktaba.desktop

import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.CF_HOST_SUFFIX
import com.hamzalabs.maktaba.core.api.rewriteToken
import com.hamzalabs.maktaba.desktop.watch.Cue
import com.hamzalabs.maktaba.desktop.watch.Master
import com.hamzalabs.maktaba.desktop.watch.Rendition
import com.hamzalabs.maktaba.desktop.watch.Vtt
import com.hamzalabs.maktaba.desktop.watch.isMaster
import com.hamzalabs.maktaba.desktop.watch.parseMaster
import com.hamzalabs.maktaba.desktop.watch.rewriteMaster
import com.hamzalabs.maktaba.desktop.watch.segmentUris
import com.sun.net.httpserver.HttpExchange
import com.sun.net.httpserver.HttpServer
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.withContext
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
 *   A channel's live HLS at home (`/stream/channel/…`) is one of them.
 * - A playlist naming the home's own address is rewritten to the proxy's, so every segment
 *   comes back through it; any other host in a playlist is left alone and never sees the token.
 * - A master playlist is read for its audio and subtitle renditions ([master]); JavaFX gets it
 *   with the chosen audio ([audio]) as the only one, and no subtitles (the Player draws them,
 *   [cues]). Story 55.08.
 * - Away from home a channel plays from Cloudflare ([relayLocal]): `…/<secret>/relay/<host>/…`
 *   goes on to that Cloudflare host only, with no header, and its token path segment replaced
 *   by the remote session's newest ([relayToken]), so a new token never reopens the player.
 *   Story 55.07.
 */
class StreamProxy(
    private val client: ApiClient,
    private val http: OkHttpClient = client.streamHttp(),
    /** For Cloudflare: no token of the home, ever. */
    private val relayHttp: OkHttpClient = OkHttpClient(),
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

    private val _master = MutableStateFlow<Master?>(null)

    /** The renditions of the last master playlist JavaFX asked for (null before one). */
    val master: StateFlow<Master?> = _master.asStateFlow()

    /** Where that master playlist is on the home: its renditions' URIs are relative to it. */
    @Volatile private var masterUrl: HttpUrl? = null

    /** The audio rendition (its NAME) JavaFX is given as the only one; null: as the home wrote it. */
    @Volatile var audio: String? = null

    /** The remote session's newest Cloudflare token, for every relay request (null: as asked). */
    @Volatile var relayToken: () -> String? = { null }

    /** Where the proxy answers: `http://127.0.0.1:<port>/<secret>`. */
    val origin: String get() = "http://${server.address.address.hostAddress}:${server.address.port}/$secret"

    /** A new video: its renditions are not known yet, and no audio is chosen. */
    fun forget() {
        _master.value = null
        masterUrl = null
        audio = null
    }

    /** The proxy's address for a stream URL of this home, or null for anything else. */
    fun local(url: HttpUrl): String? {
        if (!client.isStream(url)) return null
        val path = url.encodedPath.removePrefix(client.basePath)
        return origin + path + (url.encodedQuery?.let { "?$it" } ?: "")
    }

    /** The proxy's address for a Cloudflare playback URL (a remote session's), or null for any other host. */
    fun relayLocal(url: HttpUrl): String? {
        if (!url.isHttps || !url.host.endsWith(CF_HOST_SUFFIX)) return null
        return "$origin/relay/${url.host}${url.encodedPath}" + (url.encodedQuery?.let { "?$it" } ?: "")
    }

    /** The home's URL for a request the proxy received, or null when it is not one of its streams. */
    internal fun remote(rawPath: String, rawQuery: String?): HttpUrl? {
        val prefix = "/$secret/"
        if (!rawPath.startsWith(prefix)) return null
        val rest = rawPath.substring(prefix.length - 1)
        if (rest.startsWith("/relay/")) return null
        val url = "$homePrefix$rest${rawQuery?.let { "?$it" } ?: ""}".toHttpUrlOrNull() ?: return null
        return url.takeIf { client.isStream(it) }
    }

    /** The Cloudflare URL for a relay request the proxy received (newest token in it), or null. */
    internal fun relayRemote(rawPath: String, rawQuery: String?): HttpUrl? {
        val prefix = "/$secret/relay/"
        if (!rawPath.startsWith(prefix)) return null
        val rest = rawPath.substring(prefix.length)
        val host = rest.substringBefore('/')
        if (!host.endsWith(CF_HOST_SUFFIX) || host.any { it == ':' || it == '@' }) return null
        val url = "https://$host/${rest.substringAfter('/', "")}${rawQuery?.let { "?$it" } ?: ""}".toHttpUrlOrNull() ?: return null
        if (url.host != host) return null
        return relayToken()?.let { rewriteToken(url, it) } ?: url
    }

    private fun serve(ex: HttpExchange) {
        ex.use {
            val method = ex.requestMethod
            val raw = ex.requestURI.rawPath
            val relay = relayRemote(raw, ex.requestURI.rawQuery)
            val target = relay ?: remote(raw, ex.requestURI.rawQuery)
            if (target == null || (method != "GET" && method != "HEAD")) {
                ex.sendResponseHeaders(404, -1)
                return
            }
            val req = Request.Builder().url(target).method(method, null).apply {
                ex.requestHeaders.getFirst("Range")?.let { header("Range", it) }
            }.build()
            val resp = try {
                (if (relay != null) relayHttp else http).newCall(req).execute()
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
                    var text = body.string()
                    if (relay == null && resp.isSuccessful && isMaster(text)) {
                        masterUrl = target
                        _master.value = parseMaster(text)
                        text = rewriteMaster(text, audio)
                    }
                    val out = (if (relay != null) rewriteRelay(text) else rewrite(text)).toByteArray()
                    ex.responseHeaders.remove("Content-Length")
                    ex.sendResponseHeaders(resp.code, if (out.isEmpty()) -1 else out.size.toLong())
                    if (out.isNotEmpty()) ex.responseBody.write(out)
                    return
                }
                val length = body.contentLength()
                ex.sendResponseHeaders(resp.code, if (length == 0L) -1 else if (length > 0) length else 0)
                if (length != 0L) body.byteStream().use { it.copyTo(ex.responseBody, 64 * 1024) }
            }
        }
    }

    /** A playlist's lines (and `URI="…"` attributes) naming the home's own address now name the proxy's. */
    internal fun rewrite(playlist: String): String = playlist.lineSequence().joinToString("\n") { line ->
        when {
            line.startsWith(homePrefix + "/") -> origin + line.removePrefix(homePrefix)
            line.startsWith("#") && "URI=\"$homePrefix/" in line -> line.replace("URI=\"$homePrefix/", "URI=\"$origin/")
            else -> line
        }
    }

    /** A relay playlist's absolute Cloudflare URLs now name the proxy's relay (relative ones resolve to it already). */
    internal fun rewriteRelay(playlist: String): String = playlist.lineSequence().joinToString("\n") { line ->
        if (line.startsWith("#")) {
            URI_ATTR.replace(line) { m ->
                val u = m.groupValues[1].toHttpUrlOrNull()
                val local = u?.let(::relayLocal)
                if (local != null) "URI=\"$local\"" else m.value
            }
        } else {
            line.trim().toHttpUrlOrNull()?.let(::relayLocal) ?: line
        }
    }

    /**
     * The cues of [rendition] (a subtitle rendition of [master]): its playlist and each WebVTT
     * file, fetched from the home with the token (only this home's `/stream/` is asked).
     */
    suspend fun cues(rendition: Rendition): List<Cue> = withContext(Dispatchers.IO) {
        val base = masterUrl ?: return@withContext emptyList()
        val uri = rendition.uri ?: return@withContext emptyList()
        val list = base.resolve(uri)?.takeIf(client::isStream) ?: return@withContext emptyList()
        val first = fetch(list)
        val files = if (first.trimStart().startsWith("WEBVTT")) {
            listOf(first)
        } else {
            segmentUris(first).mapNotNull { list.resolve(it)?.takeIf(client::isStream) }.map(::fetch)
        }
        Vtt.join(files)
    }

    private fun fetch(url: HttpUrl): String = http.newCall(Request.Builder().url(url).build()).execute().use {
        if (!it.isSuccessful) throw IOException("HTTP ${it.code}")
        it.body.string()
    }

    override fun close() {
        server.stop(0)
        pool.shutdownNow()
    }

    private companion object {
        val PASSED = listOf("Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "Cache-Control")
        val URI_ATTR = Regex("""URI="([^"]+)"""")

        fun isPlaylist(url: HttpUrl, type: String?): Boolean =
            url.encodedPath.endsWith(".m3u8") || type?.lowercase()?.contains("mpegurl") == true
    }
}
