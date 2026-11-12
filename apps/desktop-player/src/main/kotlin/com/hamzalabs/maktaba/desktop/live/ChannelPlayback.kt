package com.hamzalabs.maktaba.desktop.live

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.LiveChannel
import com.hamzalabs.maktaba.core.api.LiveFailure
import com.hamzalabs.maktaba.core.api.LiveSource
import com.hamzalabs.maktaba.core.api.LiveStream
import com.hamzalabs.maktaba.core.api.programmeRefusal
import com.hamzalabs.maktaba.core.api.errorKind
import com.hamzalabs.maktaba.core.api.ErrorKind
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/** The words for a refused tune ([LiveFailure.code]), as the Android Player's; null: the general sentence. */
fun liveFailureKey(code: String): String? = when (code) {
    "remote_unavailable" -> "live.remoteUnavailable"
    "user_streams_full" -> "live.streamsFull"
    "budget_exhausted" -> "live.budget"
    "remote_timeout" -> "live.remoteTimeout"
    "remote_not_allowed" -> "live.notAllowed"
    "no_such_channel" -> "live.noSuchChannel"
    "session_ended" -> "live.sessionEnded"
    else -> null
}

/** What the channel screen shows. */
sealed interface ChannelStatus {
    data object Tuning : ChannelStatus
    data object Playing : ChannelStatus
    /** It could not start: [key] is its sentence (null: "This channel can't play"); Try again when [retryable]. */
    data class Failed(val key: String?, val retryable: Boolean) : ChannelStatus
    /** The home ended the remote session. */
    data object Ended : ChannelStatus
    /** Live TV is off on the home (a stale page: `409 component_off`). */
    data object Off : ChannelStatus
    /**
     * "Not available right now" (Epic 57 story 57.10): the programme on air is above the
     * account's limit; the home is asked again at [retryAt] (every minute when it named no time).
     */
    data class NotNow(val retryAt: java.time.Instant?) : ChannelStatus
}

/**
 * A channel's playback on the desktop (story 55.07), apart from [com.hamzalabs.maktaba.desktop.Playback]
 * (videos): [source] picks where it plays — at home the web port's HLS, away from home a remote
 * session's Cloudflare URL (tuned, a 202 asked again until live, a heartbeat that may bring a
 * new token) — and [local] gives JavaFX its address through the stream proxy, which writes the
 * newest token into every relay request. A new token never reopens the player; only another
 * channel, Back to live or Try again do ([opened] counts them).
 */
class ChannelPlayback(
    private val source: LiveSource,
    private val scope: CoroutineScope,
    /** The proxy's address for a stream (null: not one it serves). */
    private val local: (LiveStream) -> String?,
    /** Shows [url] in the player (JavaFX), from its live edge. */
    private val show: suspend (url: String) -> Unit,
    /** Stops the player. */
    private val hide: () -> Unit = {},
    private val signedOut: () -> Unit = {},
    /** Whether this sign-in is under a parental limit now (only then is [gate] asked). */
    private val limited: () -> Boolean = { false },
    /** Asks the home to play a channel for a limited account: its signed address, or "not now". */
    private val gate: (suspend (String) -> com.hamzalabs.maktaba.core.api.ChannelNow)? = null,
    private val nowMs: () -> Long = System::currentTimeMillis,
) {
    private var recheck: Job? = null

    var channelId by mutableStateOf<String?>(null)
        private set
    var status by mutableStateOf<ChannelStatus>(ChannelStatus.Tuning)
        private set
    var paused by mutableStateOf(false)
        private set

    /** How many times a stream was given to the player. */
    var opened = 0
        private set

    private var job: Job? = null
    private var url: String? = null

    /** Opens [id]: ends the remote session of the channel before it, then tunes. */
    fun open(id: String) {
        channelId = id
        job?.cancel()
        recheck?.cancel()
        job = scope.launch { start(id) }
    }

    fun retry() {
        channelId?.let(::open)
    }

    fun next(lineup: List<LiveChannel>) = step(lineup, +1)

    fun previous(lineup: List<LiveChannel>) = step(lineup, -1)

    private fun step(lineup: List<LiveChannel>, by: Int) {
        neighbour(lineup, channelId, by)?.let { open(it.id) }
    }

    /** The home ended the remote session (LiveSource's onEnded). */
    fun ended() {
        hide()
        status = ChannelStatus.Ended
    }

    fun pause() {
        paused = true
    }

    fun resume() {
        paused = false
    }

    /** Back to live after a pause: the stream again from its live edge (the same session). */
    fun backToLive() {
        val u = url ?: return
        paused = false
        scope.launch {
            opened++
            show(u)
        }
    }

    /** The screen is left (or the window closed): the remote session is ended on the home. */
    suspend fun close() {
        job?.cancel()
        recheck?.cancel()
        hide()
        withContext(NonCancellable) { runCatching { source.close() } }
    }

    private suspend fun start(id: String) {
        status = ChannelStatus.Tuning
        paused = false
        try {
            var s = source.open(id)
            val ask = gate.takeIf { s is LiveStream.Lan && limited() }
            if (ask != null) {
                when (val now = ask(id)) {
                    is com.hamzalabs.maktaba.core.api.ChannelNow.NotNow -> {
                        notNow(id, now.retryAt, now.clock)
                        return
                    }
                    is com.hamzalabs.maktaba.core.api.ChannelNow.Playing -> {
                        s = LiveStream.Lan(id, now.url)
                        askAgain(id, now.until, allowed = true, now.clock)
                    }
                }
            }
            val u = local(s) ?: throw LiveFailure("network", "not a stream this player can play")
            url = u
            opened++
            show(u)
            status = ChannelStatus.Playing
        } catch (e: CancellationException) {
            throw e
        } catch (e: LiveFailure) {
            hide()
            status = when (e.code) {
                "session_ended" -> ChannelStatus.Ended
                "component_off" -> ChannelStatus.Off
                else -> ChannelStatus.Failed(liveFailureKey(e.code), e.retryable)
            }
        } catch (e: ApiException.SignedOut) {
            signedOut()
        } catch (e: Exception) {
            val programme = e.programmeRefusal()
            if (programme != null) {
                notNow(id, programme.retryAt)
                return
            }
            hide()
            status = if (e.errorKind() == ErrorKind.Off) ChannelStatus.Off else ChannelStatus.Failed(null, retryable = true)
        }
    }

    /** The programme on air is above the limit: nothing plays, and the home is asked again later. */
    private fun notNow(id: String, retryAt: java.time.Instant?, clock: com.hamzalabs.maktaba.core.api.HomeClock? = null) {
        if (channelId != id) return
        hide()
        status = ChannelStatus.NotNow(retryAt)
        askAgain(id, retryAt, allowed = false, clock)
    }

    /**
     * Asks the home about [id] again when it said the answer may change. [clock] is the home's
     * clock by the answer that named [at] (null: it sent none).
     */
    private fun askAgain(id: String, at: java.time.Instant?, allowed: Boolean, clock: com.hamzalabs.maktaba.core.api.HomeClock? = null) {
        recheck?.cancel()
        val ask = gate ?: return
        val wait = com.hamzalabs.maktaba.core.api.channelAskAgainMs(at, allowed, nowMs(), clock) ?: return
        recheck = scope.launch {
            kotlinx.coroutines.delay(wait)
            if (channelId != id) return@launch
            if (!allowed) {
                open(id)
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
                is com.hamzalabs.maktaba.core.api.ChannelNow.NotNow -> {
                    job?.cancel()
                    notNow(id, now.retryAt, now.clock)
                }
                is com.hamzalabs.maktaba.core.api.ChannelNow.Playing -> askAgain(id, now.until, allowed = true, now.clock)
            }
        }
    }
}
