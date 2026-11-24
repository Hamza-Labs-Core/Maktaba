package com.hamzalabs.maktaba.desktop.live

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.hamzalabs.maktaba.desktop.AudioOption
import com.hamzalabs.maktaba.desktop.Players
import com.hamzalabs.maktaba.desktop.Video
import com.hamzalabs.maktaba.desktop.VideoEvents
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/**
 * A channel's picture (story 13.22): the [Video] the Player's engine makes ([players]:
 * libmpv's, or JavaFX's when libmpv cannot load or JavaFX is chosen), driven by
 * [ChannelPlayback]'s `show` and `hide`. The first stream opens a player; another channel and
 * Back to live load into the same one ([Video.load]: libmpv's `loadfile … replace`, the last
 * frame held meanwhile), or open a new one where the player cannot (JavaFX's).
 *
 * The stream's end is [onEnded]; a failure, or no progress for [STALL_MS] once it has moved
 * and while it is not paused, is [onFailed]. Both may come from any thread, and only from the player on screen now.
 */
class ChannelVideo(
    private val players: Players,
    private val scope: CoroutineScope,
    private val onEnded: () -> Unit = {},
    private val onFailed: () -> Unit = {},
    private val nowMs: () -> Long = System::currentTimeMillis,
) {
    /** The player on screen (null: none). */
    var video by mutableStateOf<Video?>(null)
        private set

    @Volatile private var paused = false
    @Volatile private var progressAt = 0L
    // the stream has moved since it was shown: only then can it stall (a start is the home's to fail)
    @Volatile private var moving = false
    // which player's events count: an older one's never do
    @Volatile private var current = 0
    private var stall: Job? = null

    private fun events(n: Int) = object : VideoEvents {
        override val volume = 1f
        override val muted = false
        override fun ready(durationMs: Long?, audio: List<AudioOption>) = progressed(n)
        override fun position(ms: Long) = progressed(n)
        override fun playing(on: Boolean) {}
        override fun failed(why: String) {
            if (n == current) onFailed()
        }
        override fun ended() {
            if (n == current) onEnded()
        }
    }

    private fun progressed(n: Int) {
        if (n == current) {
            progressAt = nowMs()
            moving = true
        }
    }

    /** Plays [url] (the stream proxy's address) from its live edge. */
    suspend fun show(url: String) {
        paused = false
        moving = false
        progressAt = nowMs()
        val v = video
        if (v == null || !v.load(url)) {
            val n = ++current
            val next = players.openLive(url, events(n))
            v?.dispose()
            video = next
        }
        watch()
    }

    /** Stops it: the player is disposed. */
    fun hide() {
        stall?.cancel()
        current++
        video?.dispose()
        video = null
    }

    fun pause() {
        paused = true
        video?.pause()
    }

    fun play() {
        paused = false
        progressAt = nowMs()
        video?.play()
    }

    /** A stream that stops moving while it should play has failed (a stalled relay, a lost home). */
    private fun watch() {
        stall?.cancel()
        val n = current
        stall = scope.launch {
            while (true) {
                delay(1_000)
                if (n != current || video == null) return@launch
                if (moving && !paused && nowMs() - progressAt > STALL_MS) {
                    onFailed()
                    return@launch
                }
            }
        }
    }

    companion object {
        /** How long a playing channel may make no progress before it has failed (retryable). */
        const val STALL_MS = 15_000L
    }
}
