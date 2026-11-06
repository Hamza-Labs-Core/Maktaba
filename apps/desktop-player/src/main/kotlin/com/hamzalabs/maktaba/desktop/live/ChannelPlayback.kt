package com.hamzalabs.maktaba.desktop.live

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.LiveChannel
import com.hamzalabs.maktaba.core.api.LiveFailure
import com.hamzalabs.maktaba.core.api.LiveSource
import com.hamzalabs.maktaba.core.api.LiveStream
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
) {
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
        hide()
        withContext(NonCancellable) { runCatching { source.close() } }
    }

    private suspend fun start(id: String) {
        status = ChannelStatus.Tuning
        paused = false
        try {
            val s = source.open(id)
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
            hide()
            status = if (e.errorKind() == ErrorKind.Off) ChannelStatus.Off else ChannelStatus.Failed(null, retryable = true)
        }
    }
}
