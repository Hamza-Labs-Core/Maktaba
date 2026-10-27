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
import com.hamzalabs.maktaba.core.api.Failure
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
    /** Opening (or waiting for a fresh transcode): show that something is happening. */
    var opening by mutableStateOf(true)
        private set

    private var session: String? = null
    private var renewAt = Long.MAX_VALUE
    private var openedAt = 0L
    private var ended = false
    private var running: Job? = null
    private val streamHttp by lazy { client.streamHttp() }

    fun start() {
        if (running != null) return
        running = scope.launch {
            open(fromMs = startMs)
            if (player != null) watch()
        }
    }

    /** Stops for good: the last position, then the session's close. */
    fun stop() {
        running?.cancel()
        running = null
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
    }

    /** Tries again after [problem] (a person pressed Try again). */
    fun retry() {
        val at = player?.currentPosition ?: startMs
        stop()
        problem = null
        running = scope.launch {
            open(fromMs = at)
            if (player != null) watch()
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
                val url = s.playbackUrl(client.home) ?: throw IllegalStateException("The home gave no address to play.")
                client.waitUntilPlayable(url, timeoutMs = 60_000)
                currentCoroutineContext().ensureActive()
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
            ended = false
            openedAt = System.currentTimeMillis()
            renewAt = s.expiresMillis?.let { it - 60_000 } ?: Long.MAX_VALUE
            if (old != null && old != s.sessionId) background.launch { client.closeStream(old) }
        } catch (e: CancellationException) {
            throw e
        } catch (e: ApiException.SignedOut) {
            stop()
            signedOut()
        } catch (e: Exception) {
            if (old != null && player?.playerError == null) return // the old stream still plays
            Log.w("MaktabaTV", "playback: ${e.errorDetails()}", e)
            player?.release()
            player = null
            problem = Failure.of(e)
        } finally {
            opening = false
        }
    }

    private fun newPlayer(): ExoPlayer {
        val data = OkHttpDataSource.Factory(streamHttp)
        return ExoPlayer.Builder(context)
            .setMediaSourceFactory(DefaultMediaSourceFactory(context).setDataSourceFactory(data))
            .setAudioAttributes(
                AudioAttributes.Builder().setUsage(C.USAGE_MEDIA).setContentType(C.AUDIO_CONTENT_TYPE_MOVIE).build(),
                true,
            )
            .build()
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
            if (at > 0) client.reportProgress(s, (at / 1000).toInt())
            val failed = p.playerError
            if (failed != null && System.currentTimeMillis() - openedAt < 30_000) {
                p.release()
                player = null
                Log.w("MaktabaTV", "player: ${failed.errorCodeName}", failed)
                problem = tvPlaybackFailure(failed)
                return
            }
            if (failed != null || System.currentTimeMillis() >= renewAt) {
                val wasPlaying = p.playWhenReady
                open(fromMs = at)
                if (!wasPlaying) player?.playWhenReady = false
            }
        }
    }
}
