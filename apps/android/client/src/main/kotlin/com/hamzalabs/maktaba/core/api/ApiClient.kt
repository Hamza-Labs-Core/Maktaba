package com.hamzalabs.maktaba.core.api

import com.hamzalabs.maktaba.core.session.TokenStore
import kotlinx.coroutines.delay
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import okhttp3.Call
import okhttp3.Callback
import okhttp3.Dispatcher
import okhttp3.HttpUrl
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import java.io.IOException
import java.util.Base64
import java.util.concurrent.TimeUnit
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException

/**
 * A request that failed. Its message is for the log and never carries the home's raw answer (a
 * screen shows the sentence for [errorKind]; the body is kept for the error screen's Details).
 */
sealed class ApiException(message: String) : Exception(message) {
    class Transport(why: String) : ApiException("Could not reach the home: $why")

    /** The home answered [code]; [body] is its answer (problem JSON, at most [BODY_KEPT] chars). */
    class Status(val code: Int, val body: String, val retryAfterSec: Long? = null) :
        ApiException("The home answered $code" + (Problem.parse(body)?.slug?.takeIf { it.isNotEmpty() }?.let { " ($it)" } ?: "")) {
        /** The home's problem JSON, when [body] is one. */
        val problem: Problem? by lazy { Problem.parse(body) }
    }

    /** A stream whose playlist did not come in time: a transcode still starting ([body]: the last answer). */
    class NotReady(val code: Int, val body: String) :
        ApiException("The stream was not ready in time (the home answered $code)")

    class Decoding(why: String) : ApiException("Unexpected answer from the home: $why")
    object SignedOut : ApiException("Signed out. Sign in again.") {
        private fun readResolve(): Any = SignedOut
    }
}

/**
 * Sends [r] and reads the whole answer on OkHttp's own thread: the caller may be the main
 * thread, where Android forbids network reads (a chunked body is read as it comes). A failure
 * to reach the home is [ApiException.Transport].
 */
suspend fun OkHttpClient.exchange(r: Request): HttpAnswer = suspendCancellableCoroutine { cont ->
    val call = newCall(r)
    cont.invokeOnCancellation { call.cancel() }
    call.enqueue(object : Callback {
        override fun onFailure(call: Call, e: IOException) {
            cont.resumeWithException(ApiException.Transport(e.message ?: e.toString()))
        }

        override fun onResponse(call: Call, response: Response) {
            val read = runCatching {
                response.use { HttpAnswer(it.code, it.body.bytes(), retryAfterSeconds(it.header("Retry-After"))) }
            }
            read.onSuccess { cont.resume(it) }
            read.onFailure { cont.resumeWithException(ApiException.Transport(it.message ?: it.toString())) }
        }
    })
}

/** An answer: its status, its body and, on a 429 or 503, `Retry-After` in seconds. */
class HttpAnswer(val code: Int, val body: ByteArray, val retryAfterSec: Long? = null) {
    operator fun component1() = code
    operator fun component2() = body
}

/** How much of a failed answer is kept (for Details and the log). */
const val BODY_KEPT = 2000

/** The REST client for one home: base URL, Bearer tokens, one refresh at a time on a 401. */
class ApiClient(
    val home: HttpUrl,
    private val tokens: TokenStore,
    private val http: OkHttpClient = defaultHttp,
    private val now: () -> Long = System::currentTimeMillis,
) {
    private val refreshLock = Mutex()

    // ---------- sign-in ----------

    /**
     * Signs in with a username and password. `X-Maktaba-Client: native` asks the home for the
     * tokens in the body (a browser gets cookies instead).
     */
    suspend fun signIn(username: String, password: String): User? {
        val body = buildJsonObject { put("username", username); put("password", password) }
        val t: AuthTokens = decode(send(request("POST", "/api/auth/login", body = body), authorized = false))
        tokens.save(t)
        return t.user
    }

    /**
     * Signs in with a Maktaba Cloud sign-in assertion (story 42.7, `POST /api/auth/cloud`): the
     * home finds the user the cloud account is linked to and gives this app its own tokens, as
     * a password sign-in does, with that user's role and the link's scope. Refused (401) when
     * the link is gone, the assertion was used or another home's.
     */
    suspend fun signInWithAssertion(assertion: String): User? {
        val body = buildJsonObject { put("assertion", assertion) }
        val t: AuthTokens = decode(send(request("POST", "/api/auth/cloud", body = body), authorized = false))
        tokens.save(t)
        return t.user
    }

    /** `GET /api/auth/me` with the home's name. */
    suspend fun whoami(): Me = get("/api/auth/me")

    /** Keeps tokens a sign-in by code collected (`DeviceSignIn`). */
    fun keep(t: AuthTokens) = tokens.save(t)

    /**
     * A request that carries no token (sign-in by code: asking for a code and polling): the
     * status and body, whatever the status. Unreachable is [ApiException.Transport].
     */
    suspend fun publicPost(path: String, body: JsonObject?): Pair<Int, String> =
        execute(request("POST", path, body = body)).let { (code, bytes) -> code to String(bytes) }

    // ---------- approving a sign-in by code (a signed-in person, for another device) ----------

    /**
     * What is asking to sign in with [code] (`GET /api/pairing/device/{code}`): who to show
     * before Approve. A home that does not know the code (or it ran out) is [PairingLookup.Unknown];
     * a home too old to say is [PairingLookup.Unsupported] (no such route: a plain 404 or 405,
     * not the home's JSON problem).
     */
    suspend fun pairingRequest(code: String): PairingLookup {
        val c = PairingCode.normalize(code) ?: return PairingLookup.Unknown
        return try {
            PairingLookup.Waiting(decode(send(request("GET", "/api/pairing/device/$c"))))
        } catch (e: ApiException.Status) {
            when {
                e.code == 405 || e.code == 501 -> PairingLookup.Unsupported
                e.code == 404 && !e.body.trimStart().startsWith("{") -> PairingLookup.Unsupported
                e.code == 404 || e.code == 410 || e.code == 422 -> PairingLookup.Unknown
                else -> throw e
            }
        }
    }

    /** Approves [code]: the device showing it is signed in as this person (`POST /api/pairing/approve`). */
    suspend fun approvePairing(code: String) {
        val c = PairingCode.normalize(code) ?: throw ApiException.Status(422, "not a sign-in code")
        send(request("POST", "/api/pairing/approve", body = buildJsonObject { put("code", PairingCode.shown(c)) }))
    }

    // ---------- viewer ----------

    suspend fun libraries(): List<Library> = get<Page<Library>>("/api/libraries").items

    suspend fun videos(library: String?, query: String? = null, cursor: String? = null): Page<Video> =
        get("/api/videos", videoListQuery(library, query, cursor))

    suspend fun video(id: String): Video = get("/api/videos/$id")

    /** Opens a stream session (the home picks direct, remux or transcode). */
    /**
     * Opens a stream session. [profile] names what this player can play (the streaming
     * service's profiles: the desktop player's JavaFX `desktop-player`); none lets the home
     * pick for a generic player.
     */
    suspend fun openStream(video: String, startSec: Int = 0, profile: String? = null): StreamSession {
        val body = buildJsonObject {
            put("video_id", video)
            if (startSec > 0) put("start_sec", startSec)
            if (profile != null) put("client_profile", profile)
        }
        return decode(send(request("POST", "/api/stream/sessions", body = body)))
    }

    suspend fun closeStream(session: String) {
        runCatching { send(request("DELETE", "/api/stream/sessions/$session")) }
    }

    suspend fun reportProgress(session: String, positionSec: Int, completed: Boolean = false) {
        val body = buildJsonObject {
            put("position_sec", positionSec)
            if (completed) put("completed", true)
        }
        runCatching { send(request("POST", "/api/stream/sessions/$session/progress", body = body)) }
    }

    /** The video's picture, fetched with the Bearer token (the route needs one). */
    suspend fun poster(video: String): ByteArray = send(request("GET", "/api/videos/$video/poster"))

    // ---------- playback ----------

    /**
     * An access token good for at least [minLifeSec] seconds, for the player's `/stream/`
     * requests (the web port lets only a signed-in caller through): the stored one, or a
     * fresh one when it ends sooner.
     */
    suspend fun playbackToken(minLifeSec: Long = 120): String {
        val access = tokens.access
        val exp = access?.let(::jwtExpiry)
        if (exp != null && exp - now() >= minLifeSec * 1000) return access
        // a token this client got moments ago is fresh, whatever this device's clock says of its
        // `exp` (a clock that runs fast would otherwise refresh on every segment)
        val fresh = lastRefresh
        if (access != null && fresh != null && fresh.first == access && now() - fresh.second < FRESH_MS) return access
        return refreshAfter(access)
    }

    /** The last access token this client refreshed to, and when (this device's clock). */
    @Volatile private var lastRefresh: Pair<String, Long>? = null

    /** Whether [url] is this home's `/stream/`: the only place the player sends the token. */
    fun isStream(url: HttpUrl): Boolean =
        url.scheme == home.scheme && url.host == home.host && url.port == home.port &&
            url.encodedPath.startsWith("$basePath/stream/")

    /** The home's web base path ("" at the root, else like `/maktaba`): every route is under it. */
    val basePath: String get() = home.encodedPath.trimEnd('/')

    /**
     * Waits until an HLS stream can start: a transcode writes its playlists a moment after the
     * session opens, while the master playlist names the rendition at once (each answers 404,
     * `manifest-not-found` or `rendition-not-found`, until then). A 404 is asked again after
     * [intervalMs], then twice that, and so on up to [maxIntervalMs], for up to [timeoutMs] in
     * all; then it is [ApiException.NotReady]. Any other failure is at once.
     */
    suspend fun waitUntilPlayable(
        url: HttpUrl,
        timeoutMs: Long = 30_000,
        intervalMs: Long = 500,
        maxIntervalMs: Long = 2_000,
    ) {
        if (!url.encodedPath.endsWith(".m3u8")) return
        var waited = 0L
        var step = intervalMs
        // the playlist at [target] once it answers 200, waiting out its 404s
        suspend fun ready(target: HttpUrl): String {
            while (true) {
                val (c, body) = fetchStream(target)
                if (c == 200) return body
                if (c != 404) throw ApiException.Status(c, body.take(BODY_KEPT))
                if (waited >= timeoutMs) throw ApiException.NotReady(c, body.take(BODY_KEPT))
                delay(step)
                waited += step
                step = (step * 2).coerceAtMost(maxIntervalMs)
            }
        }
        val master = ready(url)
        val rendition = firstUri(master)?.let { url.resolve(it) } ?: return
        ready(rendition)
    }

    /** A `/stream/` GET of this home with the token, refreshed once on a 401 (as the player's). */
    private suspend fun fetchStream(url: HttpUrl): Pair<Int, String> {
        if (!isStream(url)) throw ApiException.Status(0, "not this home's stream: ${url.host}")
        suspend fun get(token: String) = execute(Request.Builder().url(url).header("Authorization", "Bearer $token").build())
        val token = playbackToken(60)
        var (code, body) = get(token)
        if (code == 401) get(refreshAfter(token)).let { code = it.code; body = it.body }
        return code to String(body)
    }

    // ---------- remote (home admins; the home refuses anyone else) ----------

    suspend fun needsAttention(): List<FailureGroup> = get<FailureGroups>("/api/issues").groups

    suspend fun retry(stage: String, cause: String) {
        send(request("POST", "/api/queues/$stage/retry", body = buildJsonObject { put("cause", cause) }))
    }

    suspend fun queues(): QueueSnapshot = get("/api/queues")

    suspend fun pauseStage(stage: String) { send(request("POST", "/api/queues/$stage/pause")) }
    suspend fun resumeStage(stage: String) { send(request("POST", "/api/queues/$stage/resume")) }
    suspend fun pauseAll() { send(request("POST", "/api/queues/pause-all")) }
    suspend fun resumeAll() { send(request("POST", "/api/queues/resume-all")) }

    suspend fun scanLibrary(id: String) { send(request("POST", "/api/libraries/$id/scan")) }

    suspend fun machines(): ClusterOverview = get("/api/cluster")

    suspend fun setDrain(machine: String, drain: Boolean) {
        send(request("PUT", "/api/cluster/nodes/$machine", body = buildJsonObject { put("drain", drain) }))
    }

    // ---------- the state of running work (home admins; read only) ----------

    /** The stage's failed jobs grouped by cause (`GET /api/queues/{stage}/failures`). */
    suspend fun stageFailures(stage: String): List<FailureGroup> =
        get<FailureGroups>("/api/queues/${segment(stage)}/failures").groups

    /** The jobs running now across every queue; [host] narrows them ("home" or a machine's id). */
    suspend fun runningJobs(host: String? = null): List<JobRef> =
        get<RunningJobs>("/api/queues/running", if (host != null) mapOf("host" to host) else emptyMap()).jobs

    /** The home's updates: the plan, the catalog, the runs, the machines. */
    suspend fun updates(): UpdatesOverview = get("/api/cluster/updates")

    /** An update run with its steps and its machines. */
    suspend fun updateRun(id: String): UpdateRunView = get("/api/cluster/updates/runs/${segment(id)}")

    /** The supervisor's processes and their recent events. */
    suspend fun processes(): ProcessesDoc = get("/api/system/processes")

    // ---------- plumbing ----------

    /** [s] as one encoded path segment. */
    private fun segment(s: String): String =
        java.net.URLEncoder.encode(s, Charsets.UTF_8.name()).replace("+", "%20")


    private suspend inline fun <reified T> get(path: String, query: Map<String, String> = emptyMap()): T =
        decode(send(request("GET", path, query)))

    private inline fun <reified T> decode(bytes: ByteArray): T = try {
        json.decodeFromString<T>(String(bytes))
    } catch (e: Exception) {
        throw ApiException.Decoding(e.message ?: e.toString())
    }

    internal fun request(
        method: String,
        path: String,
        query: Map<String, String> = emptyMap(),
        body: JsonObject? = null,
    ): Request {
        val url = home.newBuilder().encodedPath(basePath + path).apply {
            query.toSortedMap().forEach { (k, v) -> addQueryParameter(k, v) }
        }.build()
        val payload = when {
            body != null -> json.encodeToString(JsonObject.serializer(), body).toRequestBody(JSON)
            method == "POST" || method == "PUT" -> ByteArray(0).toRequestBody(null)
            else -> null
        }
        return Request.Builder().url(url).method(method, payload)
            .header("Accept", "application/json")
            .header(ClientHeader.NAME, ClientHeader.value)
            .build()
    }

    private suspend fun send(r: Request, authorized: Boolean = true): ByteArray {
        val sentWith = if (authorized) tokens.access else null
        val first = exchange(r, sentWith)
        if (first.code == 401 && authorized) {
            if (tokens.refresh == null) {
                tokens.clear()
                throw ApiException.SignedOut
            }
            val again = exchange(r, refreshAfter(sentWith))
            if (again.code == 401) {
                tokens.clear()
                throw ApiException.SignedOut
            }
            return checked(again)
        }
        return checked(first)
    }

    private fun checked(a: HttpAnswer): ByteArray {
        if (a.code !in 200..299) throw ApiException.Status(a.code, String(a.body).take(BODY_KEPT), a.retryAfterSec)
        return a.body
    }

    private suspend fun exchange(r: Request, access: String?): HttpAnswer {
        val req = if (access != null) r.newBuilder().header("Authorization", "Bearer $access").build() else r
        return execute(req)
    }

    /**
     * One refresh at a time: requests that 401 together wait here, and a request whose token
     * ([stale]) was already replaced takes the new one instead of refreshing again. The tokens
     * are cleared only when the home refuses the refresh token, not when it cannot be reached.
     */
    suspend fun refreshAfter(stale: String?): String = refreshLock.withLock {
        tokens.access?.let { if (it != stale) return it }
        val refresh = tokens.refresh ?: throw ApiException.SignedOut
        val body = buildJsonObject { put("refresh_token", JsonPrimitive(refresh)) }
        val answer = exchange(request("POST", "/api/auth/refresh", body = body), null)
        // the home refused the refresh token; a busy (429) or failing home keeps the tokens
        if (answer.code == 400 || answer.code == 401 || answer.code == 403) {
            tokens.clear()
            throw ApiException.SignedOut
        }
        val t: AuthTokens = decode(checked(answer))
        tokens.save(t)
        lastRefresh = t.accessToken to now()
        t.accessToken
    }

    /**
     * Sends [r] and reads the whole answer on OkHttp's own thread: the caller may be the main
     * thread, where Android forbids network reads (a chunked body is read as it comes).
     */
    private suspend fun execute(r: Request): HttpAnswer = http.exchange(r)

    /**
     * An OkHttp client for the player: this home's `/stream/` requests carry the token. Its own
     * dispatcher: a stream request waiting in the interceptor for a refresh must not hold a slot
     * the refresh itself needs (OkHttp runs at most 5 calls per host per dispatcher).
     */
    fun streamHttp(): OkHttpClient = http.newBuilder()
        .dispatcher(Dispatcher())
        .addInterceptor(StreamAuthInterceptor(this))
        .build()

    companion object {
        internal val JSON = "application/json".toMediaType()

        /** How long a just-refreshed access token counts as fresh (it lasts 15 minutes). */
        private const val FRESH_MS = 10 * 60_000L

        val json = Json {
            ignoreUnknownKeys = true
            explicitNulls = false
            coerceInputValues = true
        }

        val defaultHttp: OkHttpClient = OkHttpClient.Builder()
            .connectTimeout(15, TimeUnit.SECONDS)
            .readTimeout(60, TimeUnit.SECONDS)
            .addNetworkInterceptor(SameOriginCredentials)
            .build()

        /**
         * The video list's query: the most recently added first. A search sends no sort: the home
         * orders it by best match, then the most recently added (an older home, by its own
         * default). The home's sorts are updated_at, created_at or title (it answers 400 to
         * anything else, `-created_at` included).
         */
        fun videoListQuery(library: String?, query: String?, cursor: String?): Map<String, String> = buildMap {
            put("limit", "60")
            if (query.isNullOrEmpty()) put("sort", "created_at") else put("q", query)
            if (library != null) put("library", library)
            if (cursor != null) put("cursor", cursor)
        }

        /** The first URI line of an HLS playlist (its first variant or segment). */
        fun firstUri(playlist: String): String? =
            playlist.lineSequence().map { it.trim() }.firstOrNull { it.isNotEmpty() && !it.startsWith("#") }

        /** The `exp` of a JWT in epoch milliseconds (null when it is not one). */
        fun jwtExpiry(jwt: String): Long? {
            val parts = jwt.split('.')
            if (parts.size != 3) return null
            return runCatching {
                val claims = json.parseToJsonElement(String(Base64.getUrlDecoder().decode(parts[1].trimEnd('='))))
                ((claims as JsonObject)["exp"] as JsonPrimitive).content.toDouble().toLong() * 1000
            }.getOrNull()
        }
    }
}
