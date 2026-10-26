package com.hamzalabs.maktaba.player

import android.content.Context
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
import com.hamzalabs.maktaba.core.api.Video
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/**
 * What is playing, for the whole app: the watch screen may be full screen (folded, open flat,
 * a tablet) or one half of a half-folded foldable with the library on the other, so the stream
 * lives here and not in a screen.
 *
 * Opens a stream session, plays its signed URL (every `/stream/` request carries the user's
 * token as it is now: `StreamAuthInterceptor`), reports progress every 10 s, reopens before the
 * signed URL ends (the home signs it for the video's length plus an hour, so normally never)
 * or when the player fails, and closes the session when stopped.
 */
@OptIn(UnstableApi::class)
class Playback(
    private val context: Context,
    val client: ApiClient,
    private val scope: CoroutineScope,
    /** Outlives the screen: a stopped video's last position and its session's close still go out. */
    private val background: CoroutineScope,
    private val signedOut: () -> Unit,
) {
    var video by mutableStateOf<Video?>(null)
        private set
    var player by mutableStateOf<ExoPlayer?>(null)
        private set
    var problem by mutableStateOf<String?>(null)
        private set

    private var session: String? = null
    /** When to reopen: just before the signed URL ends. */
    private var renewAt = Long.MAX_VALUE
    private var openedAt = 0L
    /** The video played to its end (and the home was told). */
    private var ended = false
    private var running: Job? = null
    private val streamHttp by lazy { client.streamHttp() }

    fun play(v: Video) {
        if (v.id == video?.id && problem == null) return
        stop()
        video = v
        running = scope.launch { run() }
    }

    fun stop() {
        running?.cancel()
        running = null
        val p = player
        val s = session
        if (s != null) {
            val at = p?.currentPosition?.div(1000)?.toInt() ?: 0
            background.launch {
                if (at > 0) client.reportProgress(s, at)
                client.closeStream(s)
            }
        }
        p?.release()
        session = null
        player = null
        problem = null
        video = null
    }

    private suspend fun run() {
        open(fromMs = 0)
        if (player != null) watch()
    }

    /**
     * Opens (or reopens) the stream at [fromMs]. The session in use is closed only once the new
     * one plays; a reopen that fails leaves the old stream playing (it shows why only when there
     * is nothing left to play).
     */
    private suspend fun open(fromMs: Long) {
        val v = video ?: return
        val old = session
        try {
            // a session the home opened is always closed, even when the player stopped meanwhile
            val s = withContext(NonCancellable) { client.openStream(v.id, (fromMs / 1000).toInt()) }
            try {
                currentCoroutineContext().ensureActive()
                val url = s.playbackUrl(client.home) ?: throw IllegalStateException("The home gave no address to play.")
                client.waitUntilPlayable(url)
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
            renewAt = (s.expiresMillis ?: Long.MAX_VALUE).let { if (it == Long.MAX_VALUE) it else it - 60_000 }
            if (old != null && old != s.sessionId) background.launch { client.closeStream(old) }
        } catch (e: CancellationException) {
            throw e
        } catch (e: ApiException.SignedOut) {
            stop()
            signedOut()
        } catch (e: Exception) {
            if (old != null && player?.playerError == null) return
            player?.release()
            player = null
            problem = e.message ?: e.toString()
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
            .setHandleAudioBecomingNoisy(true)
            .build()
    }

    /**
     * Every 10 s while playing: the position, so "continue watching" resumes here; a reopen where
     * it is before the URL ends, or when the player failed (not again within 30 s of opening:
     * then it shows why).
     */
    private suspend fun watch() {
        while (scope.isActive) {
            delay(10_000)
            val p = player ?: return
            val s = session ?: return
            val at = p.currentPosition
            if (p.playbackState == Player.STATE_ENDED) {
                // watched to the end: said once, not again every 10 s
                if (!ended) client.reportProgress(s, (at / 1000).toInt(), completed = true)
                ended = true
                continue
            }
            if (at > 0) client.reportProgress(s, (at / 1000).toInt())
            val failed = p.playerError
            if (failed != null && System.currentTimeMillis() - openedAt < 30_000) {
                p.release()
                player = null
                problem = failed.message ?: "The video stopped playing."
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
