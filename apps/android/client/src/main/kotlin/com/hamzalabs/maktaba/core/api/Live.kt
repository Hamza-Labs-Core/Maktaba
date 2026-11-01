package com.hamzalabs.maktaba.core.api

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.Interceptor
import okhttp3.OkHttpClient
import okhttp3.Request
import java.time.Instant
import java.time.temporal.ChronoUnit

// Live TV for the native apps (Epic 48 story 48.9): what is on now, the guide, one channel's
// guide, a channel's logo and stream, `GET /api/tv/info`, and away from home the remote
// session (tune, heartbeat, end). The same calls and shapes as the web's `lib/channels.ts` and
// `lib/remoteTv.ts`; the Android Player and Android TV are both built on it.
//
// Every route is any signed-in user's. The home lists only the channels this user may read
// and refuses the rest (a channel of a library they may not read answers 404, never 403, so
// its existence does not leak).

// ---------- the answers ----------

/** A channel as the guide routes describe it. */
@Serializable
data class LiveChannel(
    val id: String,
    val number: Int = 0,
    val name: String = "",
    val slug: String = "",
    /** A logo given as a URL by hand (another host); a file logo is only ever [logoUrl]. */
    val logo: String? = null,
    /** The servable logo, relative to the home (`/api/channels/{id}/logo`), when the channel has one. */
    @SerialName("logo_url") val logoUrl: String? = null,
)

/**
 * One block of a channel's timeline. [start] and [stop] are absolute (RFC 3339), so "on now"
 * and progress follow from the time alone; [isLive] and [progress] are the home's view at
 * the time it answered.
 */
@Serializable
data class Programme(
    @SerialName("channel_id") val channelId: String = "",
    /** program, filler, bumper, slate, station_id, up_next (a newer home may send others) */
    val kind: String = "program",
    val start: String,
    val stop: String,
    val title: String = "",
    @SerialName("sub_title") val subTitle: String? = null,
    val desc: String? = null,
    /** The programme's picture, relative to the home (`/api/videos/{id}/poster`) or a URL. */
    val poster: String? = null,
    val genre: String? = null,
    val rating: String? = null,
    val series: String? = null,
    val season: Int? = null,
    val episode: Int? = null,
    /** The channel strip's line: «series · الحلقة 45 — title». */
    @SerialName("on_air") val onAir: String? = null,
    /** The reference of a Quran verse the title quotes («البقرة ١٨٣»). */
    @SerialName("strip_ref") val stripRef: String? = null,
    @SerialName("is_live") val isLive: Boolean = false,
    val progress: Double = 0.0,
) {
    val startMs: Long? get() = epochMs(start)
    val stopMs: Long? get() = epochMs(stop)

    /** Whether this block is on air at [nowMs] (the server-reconciled time: [NowAnswer.serverNow]). */
    fun isAiring(nowMs: Long): Boolean {
        val s = startMs ?: return false
        val e = stopMs ?: return false
        return nowMs in s until e
    }

    /** How far through it is at [nowMs] (0..1), or null when it is not on air then. */
    fun progressAt(nowMs: Long): Double? {
        val s = startMs ?: return null
        val e = stopMs ?: return null
        if (e <= s || nowMs < s || nowMs >= e) return null
        return ((nowMs - s).toDouble() / (e - s)).coerceIn(0.0, 1.0)
    }

    /** Filler, a bumper or a station id: the guide does not show it as a programme. */
    val isFiller: Boolean get() = kind == "filler" || kind == "bumper" || kind == "slate" || kind == "station_id"

    /** "S2E5 · title", "E5", or the episode's title alone; null when there is none. */
    val episodeLabel: String?
        get() {
            val sub = subTitle?.takeIf { it.isNotBlank() }
            val ep = episode ?: return sub
            val se = if (season != null) "S${season}E$ep" else "E$ep"
            return if (sub != null) "$se · $sub" else se
        }
}

/** A channel with what is on now and next. */
@Serializable
data class NowEntry(
    val channel: LiveChannel,
    val current: Programme? = null,
    val next: Programme? = null,
    /** Encoding now on a streaming host: its playlist answers at once. */
    @SerialName("on_air") val onAir: Boolean = false,
)

/** `GET /api/channels/now`. */
@Serializable
data class NowAnswer(
    @SerialName("server_time") val serverTime: String? = null,
    val channels: List<NowEntry> = emptyList(),
) : ServerTimed {
    override val serverTimeMs: Long? get() = serverTime?.let(::epochMs)
}

/** One channel's row of the guide. */
@Serializable
data class GuideRow(
    val channel: LiveChannel,
    val blocks: List<Programme> = emptyList(),
)

/** `GET /api/channels/guide?start=&end=`. */
@Serializable
data class GuideAnswer(
    @SerialName("server_time") val serverTime: String? = null,
    val start: String? = null,
    val end: String? = null,
    val channels: List<GuideRow> = emptyList(),
) : ServerTimed {
    override val serverTimeMs: Long? get() = serverTime?.let(::epochMs)
}

/** `GET /api/channels/{id}/guide?start=&end=`: one channel, and how far its schedule is built. */
@Serializable
data class ChannelGuide(
    @SerialName("server_time") val serverTime: String? = null,
    val channel: LiveChannel,
    val blocks: List<Programme> = emptyList(),
    @SerialName("horizon_until") val horizonUntil: String? = null,
) : ServerTimed {
    override val serverTimeMs: Long? get() = serverTime?.let(::epochMs)
}

/**
 * The guide answers carry the home's clock, so a phone whose clock is wrong still shows the
 * right programme and progress (27.6 EC4).
 */
interface ServerTimed {
    val serverTimeMs: Long?

    /** How far the home's clock is ahead of this device's, measured at [localMs] (0 when unknown). */
    fun clockOffsetMs(localMs: Long): Long = serverTimeMs?.let { it - localMs } ?: 0L

    /** The home's time now, from this device's [localMs] and the answer's offset taken at [answeredAtLocalMs]. */
    fun serverNow(localMs: Long, answeredAtLocalMs: Long = localMs): Long = localMs + clockOffsetMs(answeredAtLocalMs)
}

/** `GET /api/tv/info`: where the TV streams live, and whether this device is away from home. */
@Serializable
data class TvInfo(
    @SerialName("stream_base_url") val streamBaseUrl: String = "",
    @SerialName("hls_path_template") val hlsPathTemplate: String = "",
    val remote: TvRemote? = null,
)

/** `tv/info`'s remote viewing: [viaRelay] says this request came from away from home. */
@Serializable
data class TvRemote(
    val available: Boolean = false,
    val reason: String? = null,
    @SerialName("via_relay") val viaRelay: Boolean = false,
    @SerialName("max_streams") val maxStreams: Int = 0,
    @SerialName("token_ttl_minutes") val tokenTtlMinutes: Int = 0,
)

/** `POST /api/tv/remote/tune`: 200 `live` with [hlsUrl], or 202 `starting` (ask again after [retryAfter] s). */
@Serializable
data class RemoteTuned(
    @SerialName("session_id") val sessionId: String,
    val state: String = "",
    @SerialName("hls_url") val hlsUrl: String? = null,
    @SerialName("expires_at") val expiresAt: String? = null,
    @SerialName("refresh_after") val refreshAfter: String? = null,
    @SerialName("retry_after") val retryAfter: Int? = null,
)

/** `POST /api/tv/remote/sessions/{id}/heartbeat`: a new [token] (and [hlsUrl]) only when one is due. */
@Serializable
data class RemoteBeat(
    val state: String = "",
    @SerialName("expires_at") val expiresAt: String? = null,
    val token: String? = null,
    @SerialName("hls_url") val hlsUrl: String? = null,
)

/** The time of an RFC 3339 string in epoch milliseconds, or null when it is not one. */
internal fun epochMs(s: String): Long? = runCatching { Instant.parse(s).toEpochMilli() }
    .recoverCatching { java.time.OffsetDateTime.parse(s).toInstant().toEpochMilli() }
    .getOrNull()

// ---------- the calls ----------

/** The remote session's calls: what [LiveSource] needs (a fake stands in for it in tests). */
interface RemoteTv {
    suspend fun info(): TvInfo
    suspend fun tune(channelId: String, device: String): RemoteTuned
    suspend fun heartbeat(sessionId: String): RemoteBeat
    suspend fun end(sessionId: String)
}

/**
 * Live TV's routes on one home. Signed in through [client]: each request carries its current
 * token, and a 401 takes its single refresh ([ApiClient.refreshAfter]) and tries once more.
 */
class LiveApi(
    val client: ApiClient,
    private val http: OkHttpClient = ApiClient.defaultHttp,
) : RemoteTv {
    /** `GET /api/channels/now`: every channel this user may read, with what is on now and next. */
    suspend fun now(category: String? = null): NowAnswer =
        get("/api/channels/now", if (category != null) mapOf("category" to category) else emptyMap())

    /** `GET /api/channels/guide`: every channel's blocks overlapping [start]..[end]. */
    suspend fun guide(start: Instant, end: Instant, category: String? = null): GuideAnswer =
        get("/api/channels/guide", buildMap {
            put("start", rfc3339(start))
            put("end", rfc3339(end))
            if (category != null) put("category", category)
        })

    /** `GET /api/channels/{id}/guide`: one channel's blocks overlapping [start]..[end]. */
    suspend fun channelGuide(id: String, start: Instant, end: Instant): ChannelGuide =
        get("/api/channels/${segment(id)}/guide", mapOf("start" to rfc3339(start), "end" to rfc3339(end)))

    /** `GET /api/tv/info`. */
    override suspend fun info(): TvInfo = get("/api/tv/info")

    /**
     * Where [channel]'s logo loads from: its servable logo on this home (fetch it with the token,
     * [logo]), else a logo given as a URL; null when it has none.
     */
    fun logoUrl(channel: LiveChannel): HttpUrl? {
        channel.logoUrl?.takeIf { it.isNotBlank() }?.let { return resolve(it) }
        return channel.logo?.takeIf { it.startsWith("http://", true) || it.startsWith("https://", true) }?.toHttpUrlOrNull()
    }

    /** A programme's picture on this home (or a URL), null when it has none. */
    fun posterUrl(p: Programme): HttpUrl? = p.poster?.takeIf { it.isNotBlank() }?.let(::resolve)

    /** The channel's logo (`GET /api/channels/{id}/logo`), fetched with the token. */
    suspend fun logo(channelId: String): ByteArray = send(client.request("GET", "/api/channels/${segment(channelId)}/logo"))

    /**
     * The channel's live HLS through the web port, at home: `/stream/channel/{id}/index.m3u8`.
     * Asking for it starts the channel, so the first request may fail while the encoder warms
     * up (the player retries). Play it through [ApiClient.streamHttp], which adds the token.
     */
    fun streamUrl(channelId: String): HttpUrl =
        client.home.newBuilder().encodedPath("${client.basePath}/stream/channel/${segment(channelId)}/index.m3u8").build()

    /** `POST /api/tv/remote/tune`: a 200 (live) or a 202 (starting) both answer a [RemoteTuned]. */
    override suspend fun tune(channelId: String, device: String): RemoteTuned =
        decode(send(client.request("POST", "/api/tv/remote/tune", body = buildJsonObject {
            put("channel_id", channelId)
            put("device", device)
        })))

    /** `POST /api/tv/remote/sessions/{id}/heartbeat`. */
    override suspend fun heartbeat(sessionId: String): RemoteBeat =
        decode(send(client.request("POST", "/api/tv/remote/sessions/${segment(sessionId)}/heartbeat")))

    /** `DELETE /api/tv/remote/sessions/{id}`. */
    override suspend fun end(sessionId: String) {
        send(client.request("DELETE", "/api/tv/remote/sessions/${segment(sessionId)}"))
    }

    /** A path of this home ("/api/…", under its base path) or a URL. */
    private fun resolve(ref: String): HttpUrl? =
        client.home.resolve(if (ref.startsWith("/") && !ref.startsWith("//")) client.basePath + ref else ref)

    private suspend inline fun <reified T> get(path: String, query: Map<String, String> = emptyMap()): T =
        decode(send(client.request("GET", path, query)))

    private inline fun <reified T> decode(bytes: ByteArray): T = try {
        ApiClient.json.decodeFromString<T>(String(bytes))
    } catch (e: Exception) {
        throw ApiException.Decoding(e.message ?: e.toString())
    }

    private suspend fun send(r: Request): ByteArray {
        val token = client.playbackToken(30)
        val first = http.exchange(r.withToken(token))
        if (first.code != 401) return checked(first)
        val again = http.exchange(r.withToken(client.refreshAfter(token)))
        if (again.code == 401) throw ApiException.SignedOut
        return checked(again)
    }

    private fun Request.withToken(t: String) = newBuilder().header("Authorization", "Bearer $t").build()

    private fun checked(a: HttpAnswer): ByteArray {
        if (a.code !in 200..299) throw ApiException.Status(a.code, String(a.body).take(BODY_KEPT), a.retryAfterSec)
        return a.body
    }

    companion object {
        /** RFC 3339 to the second, as the guide routes read it. */
        fun rfc3339(t: Instant): String = t.truncatedTo(ChronoUnit.SECONDS).toString()

        private fun segment(s: String): String =
            java.net.URLEncoder.encode(s, Charsets.UTF_8.name()).replace("+", "%20")
    }
}

/**
 * The stable code of a refused tune or heartbeat (`user_streams_full`, `session_ended` ...): the
 * home's problem `code`, else one read from the status; `network` when the home was not reached.
 */
fun remoteCode(e: Throwable): String = when (e) {
    is ApiException.Status -> e.problem?.code?.takeIf { it.isNotEmpty() } ?: when (e.code) {
        402 -> "budget_exhausted"
        403 -> "remote_not_allowed"
        404 -> "no_such_channel"
        429 -> "user_streams_full"
        503 -> "remote_unavailable"
        else -> "http_${e.code}"
    }
    is ApiException.SignedOut -> "signed_out"
    else -> "network"
}

// ---------- the signed Cloudflare URL ----------

/** Remote playback comes from Cloudflare Stream: `https://customer-x.cloudflarestream.com/<token>/manifest/video.m3u8`. */
const val CF_HOST_SUFFIX = ".cloudflarestream.com"

/** The signed token of a Cloudflare playback URL (its first path segment), or null. */
fun tokenOf(url: HttpUrl): String? =
    if (url.host.endsWith(CF_HOST_SUFFIX)) url.pathSegments.firstOrNull()?.takeIf { it.isNotEmpty() } else null

/**
 * [url] with [token] in its token path segment: every playlist and segment under a Cloudflare
 * playback URL carries it. Any other URL is left alone.
 */
fun rewriteToken(url: HttpUrl, token: String): HttpUrl {
    if (token.isEmpty() || !url.host.endsWith(CF_HOST_SUFFIX)) return url
    if (url.pathSize < 2 || url.pathSegments[0].isEmpty()) return url
    return url.newBuilder().setPathSegment(0, token).build()
}

// ---------- where a channel plays from ----------

/** Where a channel plays: at home through the web port, or away from home from Cloudflare. */
sealed interface LiveStream {
    val channelId: String
    val url: HttpUrl

    /** At home: the web port's HLS (play it through [ApiClient.streamHttp]). */
    data class Lan(override val channelId: String, override val url: HttpUrl) : LiveStream

    /** Away from home: a remote session's signed Cloudflare URL (play it through [LiveSource.interceptor]). */
    data class Relay(override val channelId: String, val sessionId: String, override val url: HttpUrl) : LiveStream
}

/** Why a channel could not start (the screen shows the words for [code]). */
class LiveFailure(val code: String, message: String, val retryable: Boolean = false, cause: Throwable? = null) :
    Exception(message, cause)

/**
 * Picks where a channel plays from and keeps a remote session alive (FR-23, as the web's live
 * player does). [open] reads `GET /api/tv/info`: at home it is the web port's HLS
 * ([LiveApi.streamUrl]); away (`remote.via_relay`) it tunes (`POST /api/tv/remote/tune`, a
 * 202 asked again after its `retry_after` until live, for up to [startTimeoutMs]) and beats
 * every [heartbeatMs].
 *
 * A heartbeat that brings a new token does not reopen the player: [stream] keeps its value,
 * [token] changes, and [interceptor] (added to the player's OkHttp) writes the newest token into
 * every playlist and segment request. Opening another channel, [close], and a session the home
 * ended stop the beat; the first two end the session on the home.
 *
 * Every wait is a coroutine [delay] in [scope], so a test drives the clock.
 */
class LiveSource(
    private val remote: RemoteTv,
    private val lanUrl: (String) -> HttpUrl,
    private val scope: CoroutineScope,
    private val device: String,
    private val heartbeatMs: Long = HEARTBEAT_MS,
    private val startTimeoutMs: Long = START_TIMEOUT_MS,
    /** The home ended the session (it expired, or remote viewing was taken away). */
    private val onEnded: (LiveStream.Relay) -> Unit = {},
) {
    constructor(
        api: LiveApi,
        scope: CoroutineScope,
        device: String,
        onEnded: (LiveStream.Relay) -> Unit = {},
    ) : this(api, api::streamUrl, scope, device, onEnded = onEnded)

    private val lock = Mutex()
    private var beat: Job? = null
    private val _stream = MutableStateFlow<LiveStream?>(null)
    private val _token = MutableStateFlow<String?>(null)

    /** The channel playing and where from; it changes only when a channel is opened or closed. */
    val stream: StateFlow<LiveStream?> = _stream.asStateFlow()

    /** The newest signed token of the remote session (null at home). */
    val token: StateFlow<String?> = _token.asStateFlow()

    /** The newest Cloudflare URL the home gave (the player keeps playing [stream]'s). */
    @Volatile var latestUrl: HttpUrl? = null
        private set

    /** Opens [channelId]: ends the remote session of the channel before it, then picks LAN or relay. */
    suspend fun open(channelId: String): LiveStream = lock.withLock {
        stopLocked(endSession = true)
        val info = remote.info()
        val r = info.remote
        val s = if (r?.viaRelay == true) {
            if (!r.available) throw LiveFailure("remote_unavailable", r.reason ?: "remote viewing is not available")
            tuneLocked(channelId)
        } else {
            LiveStream.Lan(channelId, lanUrl(channelId))
        }
        _stream.value = s
        if (s is LiveStream.Relay) startBeat(s)
        s
    }

    /** Stops: ends the remote session on the home, if there is one. */
    suspend fun close() = lock.withLock { stopLocked(endSession = true) }

    /** [url] with the newest token, when it is the remote session's Cloudflare URL. */
    fun rewrite(url: HttpUrl): HttpUrl = _token.value?.let { rewriteToken(url, it) } ?: url

    /** For the player's OkHttp client: every Cloudflare request carries the newest token. */
    val interceptor: Interceptor = Interceptor { chain ->
        val req = chain.request()
        val to = rewrite(req.url)
        chain.proceed(if (to == req.url) req else req.newBuilder().url(to).build())
    }

    private suspend fun tuneLocked(channelId: String): LiveStream.Relay {
        var waited = 0L
        var session: String? = null
        while (true) {
            val t = try {
                remote.tune(channelId, device)
            } catch (e: CancellationException) {
                session?.let { endQuietly(it) }
                throw e
            } catch (e: Exception) {
                val code = remoteCode(e)
                throw LiveFailure(code, e.message ?: code, retryable = code == "network" || code.startsWith("http_5"), cause = e)
            }
            session = t.sessionId
            val hls = t.hlsUrl?.toHttpUrlOrNull()
            if (t.state == "live" && hls != null) {
                latestUrl = hls
                _token.value = tokenOf(hls)
                return LiveStream.Relay(channelId, t.sessionId, hls)
            }
            if (waited >= startTimeoutMs) {
                endQuietly(t.sessionId)
                throw LiveFailure("remote_timeout", "the remote stream did not start", retryable = true)
            }
            val step = (t.retryAfter ?: 3).coerceAtLeast(1) * 1000L
            try {
                delay(step)
            } catch (e: CancellationException) {
                endQuietly(t.sessionId)
                throw e
            }
            waited += step
        }
    }

    private fun startBeat(s: LiveStream.Relay) {
        beat = scope.launch {
            while (isActive) {
                delay(heartbeatMs)
                val b = try {
                    remote.heartbeat(s.sessionId)
                } catch (e: CancellationException) {
                    throw e
                } catch (e: Exception) {
                    if (remoteCode(e) in ENDED_CODES) {
                        ended(s)
                        return@launch
                    }
                    continue // a blip: the next beat tries again (the home allows 150 s)
                }
                if (b.state == "ended") {
                    ended(s)
                    return@launch
                }
                val url = b.hlsUrl?.toHttpUrlOrNull()
                if (url != null) latestUrl = url
                val tok = b.token?.takeIf { it.isNotEmpty() } ?: url?.let(::tokenOf)
                if (tok != null) _token.value = tok
            }
        }
    }

    private fun ended(s: LiveStream.Relay) {
        if (_stream.value != s) return
        beat = null
        _stream.value = null
        _token.value = null
        onEnded(s)
    }

    private suspend fun stopLocked(endSession: Boolean) {
        beat?.cancel()
        beat = null
        val was = _stream.value
        _stream.value = null
        _token.value = null
        latestUrl = null
        if (endSession && was is LiveStream.Relay) endQuietly(was.sessionId)
    }

    /** Ends a session on the home, even while the caller is being cancelled. */
    private suspend fun endQuietly(sessionId: String) = withContext(NonCancellable) {
        try {
            remote.end(sessionId)
        } catch (_: Exception) {
            // the home lets an unbeaten session lapse on its own
        }
    }

    companion object {
        const val HEARTBEAT_MS = 60_000L
        const val START_TIMEOUT_MS = 90_000L
        private val ENDED_CODES = setOf("session_ended", "no_such_session", "remote_not_allowed", "signed_out")
    }
}
