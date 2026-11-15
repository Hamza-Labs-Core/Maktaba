package com.hamzalabs.maktaba.core.guest

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.intOrNull
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put
import okhttp3.Call
import okhttp3.Callback
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import java.io.IOException
import java.util.concurrent.TimeUnit
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException

/**
 * The edge's guest API (`docs/broadcast-edge.md`, "Viewer access" and "Guest viewers"), as
 * MaktabaKit's `GuestClient`: redeem an invite or a short code, the lineup and guide, a play with
 * the pass as the Bearer and its heartbeat, and a TV's pairing with its long-poll and poll key.
 * Every refusal is a [GuestError]; no answer at all is [GuestError.Offline].
 */
class GuestClient(
    val watch: HttpUrl = GuestEdge.PRODUCTION.toHttpUrl(),
    private val http: OkHttpClient = defaultHttp,
    /** The app's language (`ar` or `en`): the edge answers its messages in it. */
    private val language: () -> String = { "en" },
    /** A line for the app's log (never a credential). */
    private val log: (String) -> Unit = {},
) {
    /** Redeems an invite (link or token): one device use; the pass this device keeps. */
    suspend fun redeem(invite: String): GuestPass = pass(call("POST", "/api/v1/redeem", body = mapOf("invite" to invite)))

    /** Redeems the publisher's short code (`MK-XXXX`). */
    suspend fun redeem(publisher: String, code: String): GuestPass =
        pass(call("POST", "/api/v1/redeem", body = mapOf("publisher" to publisher, "code" to code)))

    suspend fun lineup(pass: GuestPass): GuestLineup =
        decode(call("GET", "/api/v1/lineup", query = mapOf("publisher" to pass.publisher), bearer = pass.credential)) {
            json.decodeFromString(GuestLineup.serializer(), it)
        }

    suspend fun guide(pass: GuestPass, channel: String): List<GuestProgramme> =
        decode(call("GET", "/api/v1/guide", query = mapOf("publisher" to pass.publisher, "channel" to channel), bearer = pass.credential)) {
            json.decodeFromString(Guide.serializer(), it).programmes
        }

    suspend fun play(pass: GuestPass, channel: String): GuestPlay {
        val a = call("POST", "/api/v1/play", body = mapOf("publisher" to pass.publisher, "channel" to channel), bearer = pass.credential)
        val j = obj(a.body)
        val session = j.str("session_id")
        val key = j.str("beat_key")
        val master = j.str("master_url")
        if (session == null || key == null || master == null) throw GuestError.BadAnswer
        return GuestPlay(
            session, key, master, j.int("refresh_after") ?: 300,
            GuestViewerState.of(200, channelState = j.str("channel_state")),
            a.renewedPass?.takeIf { it.isNotBlank() },
        )
    }

    suspend fun beat(pass: GuestPass, session: String, beatKey: String): GuestBeat {
        val a = call("POST", "/api/v1/play/$session/beat", body = mapOf("publisher" to pass.publisher, "beat_key" to beatKey), bearer = pass.credential)
        val j = obj(a.body)
        return GuestBeat(GuestViewerState.of(200, channelState = j.str("channel_state")), j.str("master_url"), j.int("refresh_after") ?: 300)
    }

    /** A TV's new pairing: the QR code and the code to show. */
    suspend fun pair(): GuestPairing = decode(call("POST", "/api/v1/pair", body = emptyMap())) { json.decodeFromString(GuestPairing.serializer(), it) }

    /** One long-poll of a pairing (the edge holds it up to [wait] seconds). */
    suspend fun poll(pairing: GuestPairing, wait: Int = 25): GuestPairPoll {
        val a = try {
            call(
                "GET", "/api/v1/pair/${pairing.nonce}", query = mapOf("wait" to wait.toString()),
                headers = mapOf("X-Maktaba-Pair-Key" to pairing.pollKey), timeoutSec = wait + 15L,
            )
        } catch (e: GuestError) {
            if (e.status == 410) return GuestPairPoll.Gone
            throw e
        }
        if (a.code == 204 || a.body.isBlank()) return GuestPairPoll.Waiting
        val j = obj(a.body)
        val pub = j.str("publisher") ?: return GuestPairPoll.Waiting
        val cred = j.str("credential") ?: return GuestPairPoll.Waiting
        val kind = when (j.str("kind")) {
            "open" -> GuestPass.Kind.Open
            "pass" -> GuestPass.Kind.Pass
            else -> if (cred.startsWith("mkbo1.")) GuestPass.Kind.Open else GuestPass.Kind.Pass
        }
        val exp = edgeTime(j.str("expires_at"))?.epochSecond ?: GuestPass.claimedExpiry(cred) ?: (System.currentTimeMillis() / 1000 + 3600)
        return GuestPairPoll.Paired(GuestPass(pub, credential = cred, kind = kind, channels = j.strings("channels"), expiresAt = exp))
    }

    // region plumbing

    @kotlinx.serialization.Serializable
    private class Guide(val programmes: List<GuestProgramme> = emptyList())

    private class Answer(val code: Int, val body: String, val renewedPass: String?)

    private fun pass(a: Answer): GuestPass {
        val j = obj(a.body)
        val pub = j.str("publisher")
        val cred = j.str("pass")
        if (pub == null || cred == null) throw GuestError.BadAnswer
        val exp = edgeTime(j.str("expires_at"))?.epochSecond ?: GuestPass.claimedExpiry(cred) ?: (System.currentTimeMillis() / 1000 + 86_400)
        return GuestPass(pub, credential = cred, kind = GuestPass.Kind.Pass, channels = j.strings("channels"), expiresAt = exp)
    }

    private fun obj(body: String): JsonObject = runCatching { json.parseToJsonElement(body) as JsonObject }.getOrElse { throw GuestError.BadAnswer }

    private fun <T> decode(a: Answer, read: (String) -> T): T = try {
        read(a.body)
    } catch (e: Exception) {
        throw GuestError.BadAnswer
    }

    private suspend fun call(
        method: String,
        path: String,
        query: Map<String, String> = emptyMap(),
        body: Map<String, String>? = null,
        bearer: String? = null,
        headers: Map<String, String> = emptyMap(),
        timeoutSec: Long = 20,
    ): Answer {
        val url = watch.newBuilder().encodedPath("/").addPathSegments(path.trimStart('/')).apply {
            query.toSortedMap().forEach { (k, v) -> addQueryParameter(k, v) }
        }.build()
        val r = Request.Builder().url(url)
            .header("Accept", "application/json")
            .header("Accept-Language", language())
            .apply {
                bearer?.let { header("Authorization", "Bearer $it") }
                headers.forEach { (k, v) -> header(k, v) }
                if (body != null) {
                    val text = buildJsonObject { body.forEach { (k, v) -> put(k, v) } }.toString()
                    method(method, text.toRequestBody("application/json".toMediaType()))
                } else {
                    method(method, null)
                }
            }
            .build()
        val client = if (timeoutSec == 20L) http else http.newBuilder().readTimeout(timeoutSec, TimeUnit.SECONDS).callTimeout(timeoutSec + 5, TimeUnit.SECONDS).build()
        val a = try {
            client.send(r)
        } catch (e: CancellationException) {
            throw e
        } catch (e: IOException) {
            log("guest $method $path: ${e.message ?: e.javaClass.simpleName}")
            throw GuestError.Offline
        }
        if (a.code !in 200..299) {
            val code = runCatching { (json.parseToJsonElement(a.body) as JsonObject).str("error") }.getOrNull().orEmpty()
            log("guest $method $path: ${a.code} $code")
            throw GuestError(a.code, code)
        }
        return a
    }

    private suspend fun OkHttpClient.send(r: Request): Answer = suspendCancellableCoroutine { cont ->
        val call = newCall(r)
        cont.invokeOnCancellation { call.cancel() }
        call.enqueue(object : Callback {
            override fun onFailure(call: Call, e: IOException) = cont.resumeWithException(e)
            override fun onResponse(call: Call, response: Response) {
                runCatching { response.use { Answer(it.code, it.body.string(), it.header("X-Maktaba-Pass")) } }
                    .onSuccess { cont.resume(it) }
                    .onFailure { cont.resumeWithException(it as? IOException ?: IOException(it)) }
            }
        })
    }

    // endregion

    companion object {
        private val json = Json { ignoreUnknownKeys = true; explicitNulls = false; coerceInputValues = true }
        val defaultHttp: OkHttpClient by lazy {
            OkHttpClient.Builder().connectTimeout(15, TimeUnit.SECONDS).readTimeout(20, TimeUnit.SECONDS).build()
        }

        private fun JsonObject.str(k: String): String? = (this[k] as? JsonPrimitive)?.takeIf { it.isString }?.contentOrNull
        private fun JsonObject.int(k: String): Int? = (this[k] as? JsonPrimitive)?.intOrNull
        private fun JsonObject.strings(k: String): List<String> =
            (this[k] as? JsonArray)?.mapNotNull { (it as? JsonPrimitive)?.takeIf { p -> p.isString }?.content }.orEmpty()
    }
}

/**
 * The guest space's ways in, apart from any screen: the saved broadcasts, redeeming an invite or a
 * short code, a phone's pairing, Remove. The words of [problem] are keys (`guest.badCode`,
 * `broadcast.invite.used`) the app turns into its language.
 */
class GuestSpace(val store: GuestPassStore, val client: GuestClient) {
    private val _saved = MutableStateFlow(store.list())
    val saved: StateFlow<List<GuestPass>> = _saved.asStateFlow()

    private val _busy = MutableStateFlow(false)

    /** A request is under way (the Watch button waits). */
    val busy: StateFlow<Boolean> = _busy.asStateFlow()

    private val _problem = MutableStateFlow<String?>(null)

    /** Why the last way in failed: the message's key. */
    val problem: StateFlow<String?> = _problem.asStateFlow()

    fun reload() {
        _saved.value = store.list()
    }

    fun clearProblem() {
        _problem.value = null
    }

    /** An invite link or token: redeemed, saved, returned. */
    suspend fun redeemInvite(text: String): GuestPass? {
        val token = GuestInput.invite(text) ?: run {
            _problem.value = "guest.badInvite"
            return null
        }
        return keep { client.redeem(token) }
    }

    /** The publisher's handle and a short code (`MK-` and four letters or digits). */
    suspend fun redeemCode(publisher: String, code: String): GuestPass? {
        val pub = GuestInput.publisher(publisher)
        val c = GuestInput.code(code)
        if (pub == null || c == null) {
            _problem.value = "guest.badCode"
            return null
        }
        return keep { client.redeem(pub, c) }
    }

    /** A phone's pairing brought this pass. */
    fun paired(pass: GuestPass) {
        store.save(pass)
        reload()
    }

    fun remove(pass: GuestPass) {
        store.remove(pass.publisher)
        reload()
    }

    /** The publisher's channels; its name is kept with the pass. */
    suspend fun lineup(pass: GuestPass): Result<GuestLineup> = try {
        val l = client.lineup(pass)
        if (l.publisher.name.isNotEmpty() && l.publisher.name != pass.name) {
            store.name(pass.publisher, l.publisher.name)
            reload()
        }
        Result.success(l)
    } catch (e: CancellationException) {
        throw e
    } catch (e: GuestError) {
        Result.failure(e)
    }

    private suspend fun keep(redeem: suspend () -> GuestPass): GuestPass? {
        _busy.value = true
        _problem.value = null
        try {
            val p = redeem()
            store.save(p)
            reload()
            return store.pass(p.publisher) ?: p
        } catch (e: CancellationException) {
            throw e
        } catch (e: GuestError) {
            _problem.value = e.messageKey
        } finally {
            _busy.value = false
        }
        return null
    }
}

/**
 * A TV's pairing: a QR code for a phone, the code to compare, the long-poll until the phone sends
 * a credential (story 50.27 §2). A pairing that ends is replaced by a new one while [run] runs.
 */
class GuestPairingRun(private val client: GuestClient, private val pause: suspend (Long) -> Unit = { delay(it) }) {
    private val _pairing = MutableStateFlow<GuestPairing?>(null)
    val pairing: StateFlow<GuestPairing?> = _pairing.asStateFlow()

    private val _problem = MutableStateFlow<String?>(null)

    /** Why the edge could not be asked (a message key); null while all is well. */
    val problem: StateFlow<String?> = _problem.asStateFlow()

    /** Runs until the phone pairs (the pass); cancelling the caller ends it. */
    suspend fun run(): GuestPass {
        while (true) {
            try {
                val p = _pairing.value ?: client.pair().also { _pairing.value = it }
                when (val answer = client.poll(p)) {
                    GuestPairPoll.Waiting -> Unit
                    GuestPairPoll.Gone -> _pairing.value = null
                    is GuestPairPoll.Paired -> return answer.pass
                }
                _problem.value = null
            } catch (e: CancellationException) {
                throw e
            } catch (e: GuestError) {
                _problem.value = e.messageKey
                pause(5_000)
            }
        }
    }

    /** A new code now (Get a new code). */
    fun renew() {
        _pairing.value = null
    }
}

/**
 * One guest channel playing, apart from the player: the play, the heartbeat every minute (each new
 * viewer token goes to [GuestLive], a renewed pass to the store), and the state the guest sees.
 * The app's player plays [Shown.live]'s master and asks it to [GuestLive.rewrite] each request.
 */
class GuestWatch(
    pass: GuestPass,
    private val client: GuestClient,
    private val store: GuestPassStore,
    private val scope: CoroutineScope,
    /** How often the heartbeat goes (the edge ends a session after 150 s without one). */
    private val beatEveryMs: Long = 60_000,
) {
    /** What shows: the channel, its state (null while starting), a refusal, and what plays. */
    data class Shown(
        val channel: String? = null,
        val state: GuestViewerState? = null,
        val problem: GuestError? = null,
        /** The channel's edge address with the newest token; null while nothing plays. */
        val live: GuestLive? = null,
        /** Moves with each new play, so the player starts again only for a new play. */
        val play: Int = 0,
    ) {
        /** The key of the words over the picture ("" while live). */
        val messageKey: String get() = problem?.messageKey ?: state?.key.orEmpty()

        /** Try again helps. */
        val canRetry: Boolean get() = problem?.state?.retry ?: state?.retry ?: false

        /** The picture plays. */
        val plays: Boolean get() = live != null && problem == null && state?.plays != false
    }

    var pass: GuestPass = pass
        private set

    private val _shown = MutableStateFlow(Shown())
    val shown: StateFlow<Shown> = _shown.asStateFlow()

    private var job: Job? = null
    private var plays = 0

    /** Plays [channel] (a channel change ends the last one's session: the edge keeps one per credential). */
    fun play(channel: String) {
        stop()
        val n = ++plays
        _shown.value = Shown(channel = channel, play = n)
        job = scope.launch {
            try {
                val p = client.play(pass, channel)
                p.renewedPass?.let {
                    store.renew(pass.publisher, it)
                    pass = store.pass(pass.publisher) ?: pass
                }
                if (!p.state.plays) {
                    _shown.value = Shown(channel, p.state, play = n)
                    return@launch
                }
                val live = GuestLive.of(p.masterUrl) ?: throw GuestError.BadAnswer
                _shown.value = Shown(channel, p.state, live = live, play = n)
                beat(p.session, p.beatKey, live, n)
            } catch (e: CancellationException) {
                throw e
            } catch (e: GuestError) {
                if (plays == n) _shown.value = Shown(channel, problem = e, play = n)
            }
        }
    }

    /** Try again: the same channel from the start. */
    fun retry() {
        _shown.value.channel?.let(::play)
    }

    /** Stops the heartbeat (the player stops itself when [Shown.live] goes). */
    fun stop() {
        job?.cancel()
        job = null
    }

    private suspend fun beat(session: String, key: String, live: GuestLive, n: Int) {
        while (scope.isActive) {
            delay(beatEveryMs)
            try {
                val b = client.beat(pass, session, key)
                b.masterUrl?.let(live::update)
                if (plays == n) _shown.value = _shown.value.copy(state = b.state)
            } catch (e: CancellationException) {
                throw e
            } catch (e: GuestError) {
                // no network for a moment: the next beat tries again (150 s before the edge ends it)
                if (e.status == 0) continue
                // the session ended (an expired invite, a withdrawn pass): the state shows at once
                if (plays == n) _shown.value = _shown.value.copy(problem = e)
                return
            }
        }
    }
}
