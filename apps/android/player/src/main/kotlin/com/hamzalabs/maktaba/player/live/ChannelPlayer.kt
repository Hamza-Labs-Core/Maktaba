package com.hamzalabs.maktaba.player.live

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ChannelNow
import com.hamzalabs.maktaba.core.api.GatedReopen
import com.hamzalabs.maktaba.core.api.channelAskAgainMs
import com.hamzalabs.maktaba.core.api.channelNow
import com.hamzalabs.maktaba.core.api.programmeRefusal
import java.time.Instant
import com.hamzalabs.maktaba.core.api.LiveApi
import com.hamzalabs.maktaba.core.api.LiveChannel
import com.hamzalabs.maktaba.core.api.LiveSource
import com.hamzalabs.maktaba.core.api.LiveStream
import com.hamzalabs.maktaba.core.api.RemoteTv
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import okhttp3.HttpUrl
import okhttp3.OkHttpClient

/**
 * A channel's live player (FR-23), apart from how it looks: which channel plays, from where, and
 * what went wrong.
 *
 * - **Where from:** [LiveSource] reads `GET /api/tv/info`. At home it is the web port's
 *   `/stream/channel/{id}/index.m3u8`: asking for it starts the channel, so the player waits out
 *   its 404s while the encoder warms ([waitPlayable]), then retries a failed start
 *   [RETRIES] times [RETRY_MS] apart with a cache-buster, as the web's live player does. Away
 *   (`via_relay`) it tunes, beats every 60 s in [scope], and ends the session when the channel
 *   changes or the person leaves ([leave]).
 * - **Which channel:** [next] / [previous] walk the lineup by number, wrapping; [tune] goes to a
 *   number typed on a keyboard.
 * - **Back to live:** after a pause the engine is behind the live edge; [backToLive] returns.
 *
 * - **Under a parental limit** (Epic 57 story 57.10, [limited]): the home is asked to play the
 *   channel ([gate], `POST /api/channels/{id}/play`). A programme above the limit is no error:
 *   [notNow] holds until the time the home named, when it is asked again; while the channel
 *   plays it is asked again when the home said the answer may change, and the video stops if
 *   the next programme is above the limit. A tune or a stream the home refuses the same way
 *   (403 `restricted`, `programme`) is "not now" too.
 *
 * Every wait is a coroutine delay in [scope], so a test drives the clock.
 */
class ChannelPlayer(
    remote: RemoteTv,
    lanUrl: (String) -> HttpUrl,
    private val scope: CoroutineScope,
    val engine: LiveEngine,
    device: String,
    /** Waits until the LAN playlist answers (its 404s while the encoder warms). */
    private val waitPlayable: suspend (HttpUrl) -> Unit,
    /** The client the LAN stream plays through (it adds the user's token). */
    private val lanHttp: () -> OkHttpClient,
    /** The client a remote session's stream plays through, given the source's token rewrite. */
    private val relayHttp: (LiveSource) -> OkHttpClient = { s -> ApiClient.defaultHttp.newBuilder().addInterceptor(s.interceptor).build() },
    /** Outlives the screen: the remote session's end still goes out after the person left. */
    private val background: CoroutineScope = LiveBackground,
    heartbeatMs: Long = LiveSource.HEARTBEAT_MS,
    /** Whether this sign-in is under a parental limit now (only then is [gate] asked). */
    private val limited: () -> Boolean = { false },
    /** Asks the home to play a channel for a limited account: its signed address, or "not now". */
    private val gate: (suspend (String) -> ChannelNow)? = null,
    private val nowMs: () -> Long = System::currentTimeMillis,
) {
    /** The channel shown (playing, tuning, or why it does not play). */
    var channelId by mutableStateOf<String?>(null)
        private set

    /** Where it plays from, once it does. */
    var stream by mutableStateOf<LiveStream?>(null)
        private set

    /** Tuning: asking the home, waiting for the encoder or the remote session. */
    var tuning by mutableStateOf(false)
        private set

    /** Why the channel does not play: a [com.hamzalabs.maktaba.core.api.LiveFailure], or any failure. */
    var problem by mutableStateOf<Throwable?>(null)
        private set

    /** The home ended the remote session (it expired, or remote viewing was taken away). */
    var sessionEnded by mutableStateOf(false)
        private set

    /**
     * "Not available right now": the programme on air is above this account's limit. The home is
     * asked again at [NotNow.retryAt] (every minute when it named no time).
     */
    var notNow by mutableStateOf<NotNow?>(null)
        private set

    private var recheck: Job? = null

    val source = LiveSource(remote, lanUrl, scope, device, heartbeatMs = heartbeatMs, onEnded = { sessionEnded = true })

    private var job: Job? = null

    /** One gated re-open per failure of a limited account's stream. */
    private val reopen = GatedReopen(nowMs)

    /** Plays [id]: the channel before it stops (its remote session ends). */
    fun open(id: String) {
        reopen.reset()
        tune(id)
    }

    private fun tune(id: String) {
        job?.cancel()
        recheck?.cancel()
        channelId = id
        stream = null
        problem = null
        notNow = null
        sessionEnded = false
        tuning = true
        job = scope.launch {
            try {
                val s = source.open(id)
                when (s) {
                    is LiveStream.Lan -> {
                        val ask = gate.takeIf { limited() }
                        if (ask == null) {
                            waitPlayable(s.url)
                            start(s, s.url, lanHttp(), attempt = 0)
                        } else when (val now = ask(id)) {
                            is ChannelNow.NotNow -> refused(id, now.retryAt)
                            is ChannelNow.Playing -> {
                                // the signed address, through the same client (it adds the token too)
                                val signed = LiveStream.Lan(id, now.url)
                                waitPlayable(signed.url)
                                start(signed, signed.url, lanHttp(), attempt = 0)
                                askAgain(id, now.until, allowed = true)
                            }
                        }
                    }
                    is LiveStream.Relay -> start(s, s.url, relayHttp(source), attempt = RETRIES)
                }
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                val programme = e.programmeRefusal()
                if (programme != null) refused(id, programme.retryAt) else {
                    tuning = false
                    problem = e
                }
            }
        }
    }

    /** The programme on air is above the limit: nothing plays, and the home is asked again later. */
    private fun refused(id: String, retryAt: Instant?) {
        if (channelId != id) return
        engine.release()
        tuning = false
        stream = null
        problem = null
        notNow = NotNow(retryAt)
        askAgain(id, retryAt, allowed = false)
    }

    /**
     * Asks the home about [id] again when it said the answer may change: a refused channel is
     * opened again then; a playing one stops when the next programme is above the limit.
     */
    private fun askAgain(id: String, at: Instant?, allowed: Boolean) {
        recheck?.cancel()
        val ask = gate ?: return
        val wait = channelAskAgainMs(at, allowed, nowMs()) ?: return
        recheck = scope.launch {
            delay(wait)
            if (channelId != id) return@launch
            if (!allowed) {
                tune(id)
                return@launch
            }
            val now = try {
                ask(id)
            } catch (e: CancellationException) {
                throw e
            } catch (_: Exception) {
                // no answer: what plays goes on (the home's streaming service stops it itself)
                return@launch
            }
            when (now) {
                is ChannelNow.NotNow -> {
                    job?.cancel()
                    refused(id, now.retryAt)
                }
                is ChannelNow.Playing -> askAgain(id, now.until, allowed = true)
            }
        }
    }

    private fun start(s: LiveStream, url: HttpUrl, http: OkHttpClient, attempt: Int) {
        stream = s
        engine.play(url, http) { e ->
            scope.launch {
                if (channelId != s.channelId || stream != s) return@launch
                if (attempt < RETRIES) {
                    // the encoder may still be warming: ask again, past any cached failure
                    delay(RETRY_MS)
                    if (channelId == s.channelId && stream == s) {
                        start(s, s.url.newBuilder().setQueryParameter("retry", "${attempt + 1}").build(), http, attempt + 1)
                    }
                } else if (gate != null && limited() && s is LiveStream.Lan && reopen.allowed()) {
                    // a limited account's stream that stopped: the programme may have changed.
                    // Once: a stream the home says plays and that fails again shows its error
                    tune(s.channelId)
                } else {
                    problem = e
                    stream = null
                }
            }
        }
        tuning = false
    }

    /** Tries the shown channel again (after a problem or an ended session). */
    fun retry() {
        channelId?.let(::open)
    }

    /** The channel after the shown one in [lineup] (by number), wrapping to the first. */
    fun next(lineup: List<LiveChannel>) = step(lineup, +1)

    /** The channel before the shown one, wrapping to the last. */
    fun previous(lineup: List<LiveChannel>) = step(lineup, -1)

    private fun step(lineup: List<LiveChannel>, by: Int) {
        neighbour(lineup, channelId, by)?.let { open(it.id) }
    }

    /** Goes to channel [number] in [lineup]; false when there is none. */
    fun tune(number: Int, lineup: List<LiveChannel>): Boolean {
        val c = lineup.firstOrNull { it.number == number } ?: return false
        if (c.id != channelId) open(c.id)
        return true
    }

    fun togglePause() = if (engine.paused) engine.resume() else engine.pause()

    /** Back to the live edge after a pause. */
    fun backToLive() = engine.goLive()

    /** The person left: the player stops and a remote session ends on the home. */
    fun leave() {
        job?.cancel()
        job = null
        recheck?.cancel()
        recheck = null
        engine.release()
        channelId = null
        stream = null
        notNow = null
        background.launch { source.close() }
    }

    companion object {
        /** A LAN start that fails is asked again this many times (the web's PREPARE_RETRIES). */
        const val RETRIES = 3
        const val RETRY_MS = 2_000L

        /** A player for [api]'s home, its waits in [scope]. */
        fun of(
            api: LiveApi,
            scope: CoroutineScope,
            engine: LiveEngine,
            device: String,
            limited: () -> Boolean = { false },
        ): ChannelPlayer {
            val streamHttp by lazy { api.client.streamHttp() }
            return ChannelPlayer(
                remote = api,
                lanUrl = api::streamUrl,
                scope = scope,
                engine = engine,
                device = device,
                waitPlayable = { api.client.waitUntilPlayable(it) },
                lanHttp = { streamHttp },
                limited = limited,
                gate = { api.client.channelNow(it) },
            )
        }
    }
}

/** The channel [by] steps from [currentId] in [lineup] (sorted by number), wrapping; null in an empty lineup. */
fun neighbour(lineup: List<LiveChannel>, currentId: String?, by: Int): LiveChannel? {
    if (lineup.isEmpty()) return null
    val sorted = lineup.sortedBy { it.number }
    val at = sorted.indexOfFirst { it.id == currentId }
    if (at < 0) return sorted.first()
    return sorted[Math.floorMod(at + by, sorted.size)]
}

/** Where a remote session's end goes out after its screen is gone. */
internal val LiveBackground = CoroutineScope(SupervisorJob() + Dispatchers.IO)

/** Live TV's "not available right now": when the home said to ask again (null: it named no time). */
data class NotNow(val retryAt: Instant?)
