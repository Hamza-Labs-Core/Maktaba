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
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch

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

    /** Opens (or reopens) the stream at [fromMs]. */
    private suspend fun open(fromMs: Long) {
        val v = video ?: return
        try {
            val s = client.openStream(v.id, (fromMs / 1000).toInt())
            val url = s.playbackUrl(client.home)
            if (url == null) {
                problem = "The home gave no address to play."
                return
            }
            session?.takeIf { it != s.sessionId }?.let { old -> background.launch { client.closeStream(old) } }
            session = s.sessionId
            client.waitUntilPlayable(url)
            openedAt = System.currentTimeMillis()
            renewAt = (s.expiresMillis ?: Long.MAX_VALUE).let { if (it == Long.MAX_VALUE) it else it - 60_000 }
            val item = MediaItem.Builder()
                .setUri(url.toString())
                .apply { if (s.isHls) setMimeType(MimeTypes.APPLICATION_M3U8) }
                .build()
            val p = player ?: newPlayer()
            p.setMediaItem(item, fromMs)
            p.prepare()
            p.playWhenReady = true
            player = p
        } catch (e: CancellationException) {
            throw e
        } catch (e: ApiException.SignedOut) {
            stop()
            signedOut()
        } catch (e: Exception) {
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
