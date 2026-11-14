package com.hamzalabs.maktaba.player.media

import android.content.Context
import android.util.Log
import androidx.annotation.StringRes
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.media3.common.MediaItem
import com.google.android.gms.cast.CastDevice
import com.google.android.gms.cast.CastMediaControlIntent
import com.google.android.gms.cast.framework.CastContext
import com.google.android.gms.cast.framework.CastSession
import com.google.android.gms.cast.framework.SessionManagerListener
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.Problem
import com.hamzalabs.maktaba.core.queue.ApiQueueTransport
import com.hamzalabs.maktaba.core.session.HomeInfo
import com.hamzalabs.maktaba.core.session.HomeProbe
import com.hamzalabs.maktaba.player.R
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.put
import java.time.Instant

/** How this home casts, once [CastSender.start] has asked it. */
enum class CastMode {
    /** Not known yet (the home is being asked). */
    Unknown,

    /** Maktaba's receiver, with a cast grant: the receiver reports the progress itself. */
    Grant,

    /**
     * A home older than the cast grant (`POST /api/cast/grants` is not a route there): Google's
     * Default Media Receiver with the session's plain signed URL, as the Player always cast;
     * the phone reports the progress, read from the receiver.
     */
    OldHome,

    /** A home with the cast grant but no address a TV can reach (an IP over plain HTTP alone): no Cast. */
    Unreachable,
}

/** The video or the play queue (Epic 56) a cast names, and where in it. */
data class CastTarget(val videoId: String, val queueId: String? = null, val queuePos: Int? = null) {
    /** One grant covers both: the same queue, or the same video outside a queue. */
    fun sameGrant(other: CastTarget): Boolean = if (queueId != null) queueId == other.queueId else other.queueId == null && videoId == other.videoId
}

/** What [CastSender.load] made of an item. */
sealed interface CastLoad {
    /** The item with the grant's custom data: hand it to Maktaba's receiver. */
    data class Grant(val item: MediaItem) : CastLoad

    /** A home older than the cast grant: hand the item as it is (its signed URL). */
    data object Plain : CastLoad

    /** The home refused the grant, or it could not be had: nothing is cast ([CastSender.error] says why). */
    data object Refused : CastLoad
}

/**
 * The Cast session as the sender needs it: Google's (`CastContext`'s current session) on a
 * device, a fake in the tests.
 */
interface CastChannel {
    /** The receiver's name while a cast session is on, else null. */
    val device: String?

    /** Sends [message] (JSON) to the receiver on [CAST_NAMESPACE]. */
    fun send(message: String)

    /** Ends the cast session; the receiver stops. */
    fun end()

    /** The receiver the next session launches ([appId]). */
    fun useReceiver(appId: String)

    /** Hears the session start and end, and the receiver's messages on [CAST_NAMESPACE]. */
    fun listen(sender: CastSender)
}

/**
 * The phone's side of a cast to Maktaba's receiver (story 58.06), one per home, outliving the
 * player's screen so a long cast keeps its grant while the app runs:
 * - [start] asks the home how it casts ([mode]): its addresses (`GET /api/discovery/home`) and
 *   whether it has the cast grant at all (`POST /api/cast/grants` with no item: a home with the
 *   route answers its JSON problem, 422; an older one a plain 404). An older home's sessions
 *   launch the Default Media Receiver ([CastChannel.useReceiver]): the choice is made before a
 *   session starts, so the TV never shows Maktaba's receiver for a load it cannot use.
 * - [load] mints the grant for an item (the person's own sign-in; the home's role and parental
 *   checks decide) and puts the custom data on it; a grant refused (403, 404) casts nothing.
 * - A fresh grant goes to the receiver over [CAST_NAMESPACE] [GRANT_REFRESH_BEFORE_MS] before the
 *   one it has ends, while the cast is on.
 * - The receiver's errors (`{"type":"error"}`) show as [error].
 */
class CastSender(
    private val client: ApiClient,
    private val channel: CastChannel?,
    /** Outlives the screens: the grant's refresh. */
    private val scope: CoroutineScope,
    /** The app's language (en or ar): the receiver's screens follow it. */
    private val lang: () -> String,
    private val appId: String = CAST_APP_ID,
    private val nowMs: () -> Long = System::currentTimeMillis,
    /** What the home says about itself (`GET /api/discovery/home`): its addresses and level. */
    private val discover: suspend () -> HomeInfo? = { runCatching { HomeProbe().probe(client.home.toString()) }.getOrNull() },
    /** One authorized call to the home: the status and the body. */
    private val call: suspend (method: String, path: String, body: String?) -> Pair<Int, String> = ApiQueueTransport(client)::send,
) {
    var mode by mutableStateOf(CastMode.Unknown)
        private set

    /** Why the last cast did not start, or what the receiver said went wrong (a string). */
    @get:StringRes
    var error by mutableStateOf<Int?>(null)
        private set

    /** The receiver's name while a cast is on. */
    var device by mutableStateOf<String?>(null)
        private set

    private var started: Job? = null
    private var urls: List<String> = emptyList()
    private var level = 1
    private var refresh: Job? = null

    /** The target whose grant [refresh] renews. */
    private var granted: CastTarget? = null

    init {
        channel?.listen(this)
    }

    /** Asks the home how it casts, once. */
    fun start(): Job {
        started?.let { return it }
        val job = scope.launch(start = CoroutineStart.LAZY) { ask() }
        started = job
        job.start()
        return job
    }

    private suspend fun ask() {
        val probe = try {
            call("POST", GRANTS, "{}")
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            // the home could not be asked (offline, signed out): asked again at the next cast
            Log.i("Maktaba", "cast: the home could not be asked how it casts (${e.javaClass.simpleName})")
            started = null
            return
        }
        if (probe.first == 404 && Problem.parse(probe.second) == null) {
            Log.i("Maktaba", "cast: the home is older than the cast grant: Google's Default Media Receiver")
            channel?.useReceiver(CastMediaControlIntent.DEFAULT_MEDIA_RECEIVER_APPLICATION_ID)
            mode = CastMode.OldHome
            return
        }
        val info = discover()
        urls = info?.urls.orEmpty()
        level = info?.apiLevel?.coerceAtLeast(1) ?: 1
        channel?.useReceiver(appId)
        mode = if (dataFor(CastTarget("probe"), "cast_probe", 0) == null) CastMode.Unreachable else CastMode.Grant
    }

    /** True while a cast session is on. */
    val sessionOn: Boolean get() = channel?.device != null

    private fun dataFor(target: CastTarget, grant: String, startMs: Long): JsonObject? = castData(
        CastLoadInput(
            senderBase = client.home.toString(),
            discoveryUrls = urls,
            grant = grant,
            videoId = target.videoId,
            queueId = target.queueId,
            queuePos = target.queuePos,
            startSec = startMs / 1000.0,
            lang = lang(),
            apiLevel = level,
        ),
    )

    private class Minted(val grant: String, val expiresAtMs: Long?)

    /** A grant for [target]; null with [error] set when the home refused it or could not be asked. */
    private suspend fun mint(target: CastTarget): Minted? {
        val body = buildJsonObject {
            if (target.queueId != null) put("queue_id", target.queueId) else put("video_id", target.videoId)
        }.toString()
        val (code, text) = try {
            call("POST", GRANTS, body)
        } catch (e: CancellationException) {
            throw e
        } catch (e: ApiException.SignedOut) {
            error = R.string.cast_error_failed
            return null
        } catch (e: Exception) {
            Log.w("Maktaba", "cast: the grant could not be asked for (${e.javaClass.simpleName})")
            error = R.string.cast_error_failed
            return null
        }
        if (code in 200..299) {
            val o = runCatching { Json.parseToJsonElement(text) as JsonObject }.getOrNull()
            val grant = (o?.get("grant") as? JsonPrimitive)?.contentOrNull
            if (grant.isNullOrEmpty()) {
                error = R.string.cast_error_failed
                return null
            }
            val ends = (o["expires_at"] as? JsonPrimitive)?.contentOrNull?.let { runCatching { Instant.parse(it).toEpochMilli() }.getOrNull() }
            return Minted(grant, ends)
        }
        val problem = Problem.parse(text)
        Log.i("Maktaba", "cast: the home refused the grant ($code ${problem?.code.orEmpty()})")
        error = when {
            problem?.code == "cast_via_cloud" -> R.string.cast_error_cloud
            code == 403 || code == 404 -> R.string.cast_error_refused
            else -> R.string.cast_error_failed
        }
        return null
    }

    /**
     * What to hand the receiver for [item] ([target], from [startMs]): the item with the grant's
     * custom data, the item as it is for an older home, or nothing when the grant was refused.
     */
    suspend fun load(item: MediaItem, target: CastTarget, startMs: Long): CastLoad {
        start().join()
        error = null
        when (mode) {
            CastMode.OldHome -> return CastLoad.Plain
            CastMode.Unreachable, CastMode.Unknown -> {
                error = R.string.cast_error_failed
                return CastLoad.Refused
            }
            CastMode.Grant -> {}
        }
        val m = mint(target) ?: return CastLoad.Refused
        val data = dataFor(target, m.grant, startMs) ?: run {
            error = R.string.cast_error_failed
            return CastLoad.Refused
        }
        keep(target, m)
        return CastLoad.Grant(item.buildUpon().setMediaId(target.videoId).build().withCastData(data))
    }

    /** Mints the next grant for [target] before [m] ends and sends it to the receiver. */
    private fun keep(target: CastTarget, m: Minted) {
        granted = target
        refresh?.cancel()
        val ends = m.expiresAtMs ?: return
        val wait = maxOf(60_000L, ends - GRANT_REFRESH_BEFORE_MS - nowMs())
        refresh = scope.launch {
            delay(wait)
            if (granted?.sameGrant(target) != true || !sessionOn) return@launch
            val next = mint(target) ?: return@launch // it plays out on the grant it has; the TV then asks for a new cast
            channel?.send(buildJsonObject { put("type", "grant"); put("grant", next.grant) }.toString())
            keep(target, next)
        }
    }

    /** The target the receiver now plays (it walked its queue): the refresh follows it. */
    fun follow(target: CastTarget) {
        // a queue's grant covers each of its items
        if (granted?.queueId != null && granted?.sameGrant(target) == true) granted = target
    }

    /** Ends the cast (a refused grant: the phone plays on). */
    fun end() {
        channel?.end()
    }

    fun clearError() {
        error = null
    }

    // ---------- from the Cast session ----------

    /** A session started or resumed on the receiver named [name]. */
    fun connected(name: String?) {
        device = name ?: ""
    }

    /** The session ended: nothing is renewed any more. */
    fun ended() {
        refresh?.cancel()
        refresh = null
        granted = null
        device = null
    }

    /** A message from the receiver on [CAST_NAMESPACE]. */
    fun heard(raw: String) {
        val o = runCatching { Json.parseToJsonElement(raw) as? JsonObject }.getOrNull() ?: return
        if ((o["type"] as? JsonPrimitive)?.contentOrNull != "error") return
        RECEIVER_ERRORS[(o["error"] as? JsonPrimitive)?.contentOrNull]?.let { error = it }
    }

    companion object {
        /** A fresh grant is minted this long before the current one ends. */
        const val GRANT_REFRESH_BEFORE_MS = 30 * 60_000L

        private const val GRANTS = "/api/cast/grants"

        private val RECEIVER_ERRORS = mapOf(
            "home" to R.string.cast_error_home,
            "format" to R.string.cast_error_format,
            "grant" to R.string.cast_error_grant_ended,
            "update" to R.string.cast_error_update,
        )
    }
}

/** One sender per home, for the app's life (null without a receiver application ID: no Cast). */
object CastSenders {
    private val byHome = mutableMapOf<String, CastSender>()
    private val scope by lazy { CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate) }

    fun of(context: Context, client: ApiClient, appId: String = CAST_APP_ID): CastSender? {
        if (!castStarted(context, appId)) return null
        val app = context.applicationContext
        return synchronized(byHome) {
            byHome.getOrPut(client.home.toString()) {
                CastSender(client, GoogleCastChannel(app), scope, lang = { appLanguage(app) }, appId = appId)
            }
        }
    }
}

/** The app's language as the person chose it (Android's per-app language, or the phone's): en or ar. */
fun appLanguage(context: Context): String =
    if (context.resources.configuration.locales.get(0)?.language == "ar") "ar" else "en"

/** The Cast framework's session, as [CastChannel]: needs [castStarted]. */
private class GoogleCastChannel(private val context: Context) : CastChannel {
    private val cast: CastContext? get() = runCatching { CastContext.getSharedInstance() }.getOrNull()
    private val session: CastSession? get() = cast?.sessionManager?.currentCastSession

    override val device: String? get() = session?.takeIf { it.isConnected }?.castDevice?.friendlyName

    override fun send(message: String) {
        session?.sendMessage(CAST_NAMESPACE, message)
    }

    override fun end() {
        cast?.sessionManager?.endCurrentSession(true)
    }

    override fun useReceiver(appId: String) {
        // Play services' cast-framework 23 has CastContext.setReceiverApplicationId: the next
        // session launches this receiver (an older home's: the Default Media Receiver)
        runCatching { cast?.setReceiverApplicationId(appId) }
    }

    override fun listen(sender: CastSender) {
        val manager = cast?.sessionManager ?: return
        val messages = { _: CastDevice, _: String, message: String -> sender.heard(message) }
        fun attach(s: CastSession) {
            runCatching { s.setMessageReceivedCallbacks(CAST_NAMESPACE, messages) }
            sender.connected(s.castDevice?.friendlyName)
        }
        manager.currentCastSession?.takeIf { it.isConnected }?.let(::attach)
        manager.addSessionManagerListener(object : SessionManagerListener<CastSession> {
            override fun onSessionStarted(session: CastSession, sessionId: String) = attach(session)
            override fun onSessionResumed(session: CastSession, wasSuspended: Boolean) = attach(session)
            override fun onSessionEnded(session: CastSession, error: Int) = sender.ended()
            override fun onSessionStarting(session: CastSession) {}
            override fun onSessionStartFailed(session: CastSession, error: Int) {}
            override fun onSessionEnding(session: CastSession) {}
            override fun onSessionResuming(session: CastSession, sessionId: String) {}
            override fun onSessionResumeFailed(session: CastSession, error: Int) {}
            override fun onSessionSuspended(session: CastSession, reason: Int) {}
        }, CastSession::class.java)
    }
}
