package com.hamzalabs.maktaba.tv.playback

import android.content.Context
import android.util.Log
import androidx.annotation.OptIn
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.media3.common.AudioAttributes
import androidx.media3.common.C
import androidx.media3.common.MediaItem
import androidx.media3.common.MimeTypes
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.okhttp.OkHttpDataSource
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.DefaultMediaSourceFactory
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.ErrorKind
import com.hamzalabs.maktaba.core.api.Failure
import com.hamzalabs.maktaba.core.api.ProgressAnswer
import com.hamzalabs.maktaba.core.api.TimeReason
import com.hamzalabs.maktaba.core.api.TimeRefusal
import com.hamzalabs.maktaba.core.api.timeRefusal
import kotlinx.coroutines.Dispatchers
import com.hamzalabs.maktaba.core.api.busyRetryMs
import com.hamzalabs.maktaba.core.api.errorDetails
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.delay
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/**
 * One video playing on the TV, from [startMs] (a resume) or its start.
 *
 * Opens a stream session (the home picks direct, remux or transcode), waits until a fresh
 * transcode's rendition playlist is written (`ApiClient.waitUntilPlayable`: it answers 404 until
 * then), and plays the signed URL through the core's stream client, which puts the user's token
 * as it is now on every playlist and segment request (`StreamAuthInterceptor`; the web port
 * refuses `/stream/` without it). Reports the position every 10 s, so Continue Watching picks up
 * here; reopens before the signed URL ends or when the player fails; closes the session on
 * [stop]. A transcode opened at a position keeps the video's times, so the player seeks there.
 * A home with no transcode slot free (503 `transcode_busy`, Epic 59 story 59.05) is no error:
 * [waiting] carries the sentence while the stream is asked for again after the home's
 * `Retry-After` (10 s), as the phone Player does.
 *
 * Epic 57 (stories 57.14, 57.15): the progress post is also the stream's heartbeat for an
 * account with time rules, so it goes out every 10 s while playing and at once on pause and
 * resume, and its answer is read. When the home says the account's time is over (on opening,
 * renewing or a progress post, or the stream itself is refused so) the video stops and [timeUp]
 * says why; when it says the stream is gone, [endedByHome]. Neither is asked about again until
 * the person does ([checkAgain]).
 *
 * The same lifecycle as the phone Player's `Playback` (apps/android/player), for one screen.
 */
@OptIn(UnstableApi::class)
class TvPlayback(
    private val context: Context,
    private val client: ApiClient,
    private val video: String,
    private val startMs: Long,
    private val scope: CoroutineScope,
    /** Outlives the screen: the last position and the session's close still go out. */
    private val background: CoroutineScope,
    private val signedOut: () -> Unit,
) {
    var player by mutableStateOf<ExoPlayer?>(null)
        private set
    /** Why nothing plays (the error screen): what it means to a person, and the home's own words. */
    var problem by mutableStateOf<Failure?>(null)
        private set
    /**
     * The home has no transcode slot free (503 `transcode_busy`, story 59.05): no error screen,
     * the sentence shown while the stream is asked for again after the home's `Retry-After`.
     */
    var waiting by mutableStateOf<Failure?>(null)
        private set
    /**
     * The home said this account's watching time is over (403 `parental-time`): the video has
     * stopped at [TvTimeUp.positionMs], and the home is not asked again until [checkAgain].
     */
    var timeUp by mutableStateOf<TvTimeUp?>(null)
        private set
    /** The home ended the stream while it played (a progress post answered 404). Nothing asks again. */
    var endedByHome by mutableStateOf(false)
        private set
    /** The home could not check the account's time at the last progress post (503); the next one asks again. */
    var cannotCheck by mutableStateOf(false)
        private set
    /** Opening (or waiting for a fresh transcode): show that something is happening. */
    var opening by mutableStateOf(true)
        private set

    private var session: String? = null
    private var renewAt = Long.MAX_VALUE
    private var openedAt = 0L
    private var ended = false
    private var running: Job? = null
    /** How many sessions were opened; the reopen after a lapsed lease (`no_heartbeat`), and when the last one took. */
    private var opens = 0
    private var lapseReopen: Job? = null
    private var lapseReopenedAt = 0L
    private val streamHttp by lazy { client.streamHttp() }

    fun start() {
        if (running != null) return
        running = scope.launch {
            openWhenFree(fromMs = startMs)
            if (player != null) watch()
        }
    }

    /** Stops for good: the last position, then the session's close. */
    fun stop() {
        running?.cancel()
        running = null
        lapseReopen?.cancel()
        lapseReopen = null
        lapseReopenedAt = 0L
        val p = player
        val s = session
        if (s != null) {
            val at = p?.currentPosition?.div(1000)?.toInt() ?: 0
            background.launch {
                if (at > 0 && !ended) client.reportProgress(s, at)
                client.closeStream(s)
            }
        }
        p?.release()
        session = null
        player = null
        waiting = null
        cannotCheck = false
    }

    /** The person's own press on the time's-up screen (or after a parent gave more time): from where it stopped. */
    fun checkAgain() {
        val at = timeUp?.positionMs ?: startMs
        stop()
        timeUp = null
        endedByHome = false
        problem = null
        opening = true
        running = scope.launch {
            openWhenFree(fromMs = at)
            if (player != null) watch()
        }
    }

    /** Tries again after [problem] (a person pressed Try again). */
    fun retry() {
        val at = player?.currentPosition ?: startMs
        stop()
        problem = null
        running = scope.launch {
            openWhenFree(fromMs = at)
            if (player != null) watch()
        }
    }

    /**
     * Opens at [fromMs]; while the home has no transcode slot free ([waiting]), asks again after
     * its `Retry-After` (10 s) until one is: the same wait as the phone Player's.
     */
    private suspend fun openWhenFree(fromMs: Long) {
        while (true) {
            open(fromMs)
            val w = waiting ?: return
            delay(busyRetryMs(w.retryAfterSec))
        }
    }

    private suspend fun open(fromMs: Long) {
        val old = session
        opening = player == null
        try {
            // a session the home opened is always closed, even when the screen went away meanwhile
            val s = withContext(NonCancellable) { client.openStream(video, (fromMs / 1000).toInt()) }
            try {
                currentCoroutineContext().ensureActive()
                // the video stopped for time, or the home ended it, while this open was on its
                // way: nothing starts under that screen (the session is closed below)
                if (stoppedByHome()) throw CancellationException("The video stopped while it was being opened.")
                val url = s.playbackUrl(client.home) ?: throw IllegalStateException("The home gave no address to play.")
                client.waitUntilPlayable(url, timeoutMs = 60_000)
                currentCoroutineContext().ensureActive()
                if (stoppedByHome()) throw CancellationException("The video stopped while it was being opened.")
                val item = MediaItem.Builder()
                    .setUri(url.toString())
                    .apply { if (s.isHls) setMimeType(MimeTypes.APPLICATION_M3U8) }
                    .build()
                val p = player ?: newPlayer()
                p.setMediaItem(item, fromMs)
                p.prepare()
                p.playWhenReady = true
                player = p
            } catch (e: Throwable) {
                background.launch { client.closeStream(s.sessionId) }
                throw e
            }
            session = s.sessionId
            opens++
            waiting = null
            ended = false
            openedAt = System.currentTimeMillis()
            renewAt = s.expiresMillis?.let { it - 60_000 } ?: Long.MAX_VALUE
            if (old != null && old != s.sessionId) background.launch { client.closeStream(old) }
        } catch (e: CancellationException) {
            // stopped by the home meanwhile: this open ends quietly, its caller goes on
            if (!stoppedByHome()) throw e
        } catch (e: ApiException.SignedOut) {
            stop()
            signedOut()
        } catch (e: Exception) {
            val over = e.timeRefusal()
            if (over != null) {
                // the account's time is over: nothing plays on, not the stream before either
                stopForTime(over, fromMs)
                return
            }
            if (old != null && player?.playerError == null) return // the old stream still plays
            Log.w("MaktabaTV", "playback: ${e.errorDetails()}", e)
            player?.release()
            player = null
            val f = Failure.of(e)
            if (f.kind == ErrorKind.TranscodeBusy) {
                waiting = f
            } else {
                waiting = null
                problem = f
            }
        } finally {
            opening = false
        }
    }

    /** The time's-up screen or "ended by the home" shows: nothing may start playing under it. */
    private fun stoppedByHome(): Boolean = timeUp != null || endedByHome

    private fun newPlayer(): ExoPlayer {
        val data = OkHttpDataSource.Factory(streamHttp)
        return ExoPlayer.Builder(context)
            .setMediaSourceFactory(DefaultMediaSourceFactory(context).setDataSourceFactory(data))
            .setAudioAttributes(
                AudioAttributes.Builder().setUsage(C.USAGE_MEDIA).setContentType(C.AUDIO_CONTENT_TYPE_MOVIE).build(),
                true,
            )
            .build()
            .also { p ->
                p.addListener(object : Player.Listener {
                    override fun onPlayWhenReadyChanged(playWhenReady: Boolean, reason: Int) {
                        // paused: Continue Watching resumes here. Resumed: the home hears of it at once,
                        // as a time-limited account's stream is served only while it does (57.14)
                        val s = session ?: return
                        val at = p.currentPosition
                        if (at <= 0 || p.playbackState == Player.STATE_ENDED) return
                        background.launch {
                            val answer = client.reportProgress(s, (at / 1000).toInt())
                            scope.launch(Dispatchers.Main) { heard(s, answer, at, completed = false) }
                        }
                    }
                })
            }
    }

    /** What the home said to a progress post of session [s] at [atMs]. */
    private fun heard(s: String, answer: ProgressAnswer, atMs: Long, completed: Boolean) {
        // a post of a session since closed or replaced says nothing about what plays now
        if (session != s) return
        when (answer) {
            ProgressAnswer.Ok -> cannotCheck = false
            is ProgressAnswer.TimeOver -> refusedForTime(answer.refusal, atMs)
            ProgressAnswer.Ended -> if (!completed && !ended) {
                Log.i("MaktabaTV", "playback: the home ended the stream")
                session = null
                player?.release()
                player = null
                waiting = null
                endedByHome = true
            }
            is ProgressAnswer.CannotCheck -> cannotCheck = true
            is ProgressAnswer.Failed -> {}
        }
    }

    /**
     * The home refused the stream that plays, for time. `no_heartbeat` is not the time being
     * over: the stream sent no progress for a minute (a long pause) and its lease lapsed. It is
     * opened again once, where it is; the home's answer to that open says whether there is time
     * ([open] stops for it when there is not). A lapse again right after such a reopen stops.
     * The same as the phone Player's.
     */
    private fun refusedForTime(refusal: TimeRefusal, atMs: Long) {
        val s = session
        val sinceReopen = System.currentTimeMillis() - lapseReopenedAt
        if (refusal.reason != TimeReason.NO_HEARTBEAT || s == null || sinceReopen < LAPSE_REOPEN_ONCE_MS) {
            stopForTime(refusal, atMs)
            return
        }
        if (lapseReopen?.isActive == true) return
        Log.i("MaktabaTV", "playback: the stream's lease lapsed; opening it again")
        val wasPlaying = player?.playWhenReady == true
        lapseReopen = scope.launch(Dispatchers.Main.immediate) {
            val before = opens
            open(fromMs = atMs.coerceAtLeast(0))
            if (opens != before) lapseReopenedAt = System.currentTimeMillis()
            if (!wasPlaying) player?.playWhenReady = false
        }
    }

    /** Stops for [refusal] at [atMs]: the stream is closed and nothing asks the home again by itself. */
    private fun stopForTime(refusal: TimeRefusal, atMs: Long) {
        Log.i("MaktabaTV", "playback: the account's time is over (${refusal.reason})")
        val s = session
        session = null
        if (s != null) background.launch { client.closeStream(s) }
        player?.release()
        player = null
        waiting = null
        problem = null
        cannotCheck = false
        timeUp = TvTimeUp(refusal, atMs.coerceAtLeast(0))
    }

    private suspend fun watch() {
        while (scope.isActive) {
            delay(10_000)
            val p = player ?: return
            val s = session ?: return
            val at = p.currentPosition
            if (p.playbackState == Player.STATE_ENDED) {
                if (!ended) client.reportProgress(s, (at / 1000).toInt(), completed = true)
                ended = true
                continue
            }
            // while it plays only: a paused video costs a time-limited account nothing
            if (at > 0 && p.isPlaying) {
                heard(s, client.reportProgress(s, (at / 1000).toInt()), at, completed = false)
                if (player == null) return
            }
            val failed = p.playerError
            // the streaming service refused the stream itself because the time is over
            failed?.let(::tvPlaybackTimeRefusal)?.let {
                refusedForTime(it, at)
                if (player == null) return
                continue
            }
            // the stream is being opened again after a lapsed lease: that open is the only one
            if (lapseReopen?.isActive == true) continue
            if (failed != null && System.currentTimeMillis() - openedAt < 30_000) {
                p.release()
                player = null
                Log.w("MaktabaTV", "player: ${failed.errorCodeName}", failed)
                problem = tvPlaybackFailure(failed)
                return
            }
            if (failed != null || System.currentTimeMillis() >= renewAt) {
                val wasPlaying = p.playWhenReady
                openWhenFree(fromMs = at)
                if (!wasPlaying) player?.playWhenReady = false
            }
        }
    }
}

/** A lease that lapses again this soon after it was reopened for a lapse stops the video. */
const val LAPSE_REOPEN_ONCE_MS = 30_000L

/** Why and where a video stopped for time: the home's refusal and the position it stopped at. */
data class TvTimeUp(val refusal: TimeRefusal, val positionMs: Long = 0)
