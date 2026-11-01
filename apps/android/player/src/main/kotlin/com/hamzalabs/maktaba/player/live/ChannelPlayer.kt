package com.hamzalabs.maktaba.player.live

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.hamzalabs.maktaba.core.api.ApiClient
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

    val source = LiveSource(remote, lanUrl, scope, device, heartbeatMs = heartbeatMs, onEnded = { sessionEnded = true })

    private var job: Job? = null

    /** Plays [id]: the channel before it stops (its remote session ends). */
    fun open(id: String) {
        job?.cancel()
        channelId = id
        stream = null
        problem = null
        sessionEnded = false
        tuning = true
        job = scope.launch {
            try {
                val s = source.open(id)
                when (s) {
                    is LiveStream.Lan -> {
                        waitPlayable(s.url)
                        start(s, s.url, lanHttp(), attempt = 0)
                    }
                    is LiveStream.Relay -> start(s, s.url, relayHttp(source), attempt = RETRIES)
                }
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                tuning = false
                problem = e
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
        engine.release()
        channelId = null
        stream = null
        background.launch { source.close() }
    }

    companion object {
        /** A LAN start that fails is asked again this many times (the web's PREPARE_RETRIES). */
        const val RETRIES = 3
        const val RETRY_MS = 2_000L

        /** A player for [api]'s home, its waits in [scope]. */
        fun of(api: LiveApi, scope: CoroutineScope, engine: LiveEngine, device: String): ChannelPlayer {
            val streamHttp by lazy { api.client.streamHttp() }
            return ChannelPlayer(
                remote = api,
                lanUrl = api::streamUrl,
                scope = scope,
                engine = engine,
                device = device,
                waitPlayable = { api.client.waitUntilPlayable(it) },
                lanHttp = { streamHttp },
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
