package com.hamzalabs.maktaba.player.live

import android.content.Context
import androidx.annotation.OptIn
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.viewinterop.AndroidView
import androidx.media3.common.AudioAttributes
import androidx.media3.common.C
import androidx.media3.common.MediaItem
import androidx.media3.common.MimeTypes
import androidx.media3.common.PlaybackException
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.okhttp.OkHttpDataSource
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.DefaultMediaSourceFactory
import androidx.media3.ui.PlayerView
import okhttp3.HttpUrl
import okhttp3.OkHttpClient

/**
 * What plays a channel's HLS: the app's is [ExoLiveEngine] (Media3); the tests give a fake that
 * fetches the playlist from the mock home, so "it plays the mock's HLS" is a request the mock
 * answered. Its state is Compose state: the live player's controls read it.
 */
interface LiveEngine {
    /** The person paused it. */
    val paused: Boolean

    /** Behind the live edge: paused once, until [goLive] (the player then offers "Back to live"). */
    val behindLive: Boolean

    /** Plays [url] through [http] (the token, or the remote session's rewrite); [onError] when it fails. */
    fun play(url: HttpUrl, http: OkHttpClient, onError: (Throwable) -> Unit)

    fun pause()
    fun resume()

    /** Back to the live edge, playing. */
    fun goLive()

    /** Stops and frees the decoder. */
    fun release()

    /** The video, filling [modifier]. */
    @Composable
    fun Video(modifier: Modifier)
}

/** Media3's player for a live HLS: no controls of its own (the live player draws them). */
@OptIn(UnstableApi::class)
class ExoLiveEngine(private val context: Context) : LiveEngine {
    override var paused by mutableStateOf(false)
        private set
    override var behindLive by mutableStateOf(false)
        private set

    private var player by mutableStateOf<ExoPlayer?>(null)
    private var http: OkHttpClient? = null
    private var onError: (Throwable) -> Unit = {}

    private val listener = object : Player.Listener {
        override fun onPlayerError(error: PlaybackException) = onError(error)
    }

    override fun play(url: HttpUrl, http: OkHttpClient, onError: (Throwable) -> Unit) {
        this.onError = onError
        // the data source is bound when the player is built: another client (LAN ↔ remote) builds another
        val p = player.takeIf { this.http === http } ?: newPlayer(http)
        p.setMediaItem(MediaItem.Builder().setUri(url.toString()).setMimeType(MimeTypes.APPLICATION_M3U8).build())
        p.prepare()
        p.playWhenReady = true
        paused = false
        behindLive = false
    }

    private fun newPlayer(client: OkHttpClient): ExoPlayer {
        player?.release()
        http = client
        return ExoPlayer.Builder(context)
            .setMediaSourceFactory(DefaultMediaSourceFactory(context).setDataSourceFactory(OkHttpDataSource.Factory(client)))
            .setAudioAttributes(
                AudioAttributes.Builder().setUsage(C.USAGE_MEDIA).setContentType(C.AUDIO_CONTENT_TYPE_MOVIE).build(),
                true,
            )
            .setHandleAudioBecomingNoisy(true)
            .build()
            .also {
                it.addListener(listener)
                player = it
            }
    }

    override fun pause() {
        player?.playWhenReady = false
        paused = true
        behindLive = true
    }

    override fun resume() {
        player?.playWhenReady = true
        paused = false
    }

    override fun goLive() {
        player?.let {
            it.seekToDefaultPosition()
            it.playWhenReady = true
        }
        paused = false
        behindLive = false
    }

    override fun release() {
        player?.release()
        player = null
        http = null
    }

    @Composable
    override fun Video(modifier: Modifier) {
        val p = player
        AndroidView(
            factory = { ctx ->
                PlayerView(ctx).apply {
                    useController = false
                    setShowBuffering(PlayerView.SHOW_BUFFERING_WHEN_PLAYING)
                    keepScreenOn = true
                }
            },
            update = { it.player = p },
            onRelease = { it.player = null },
            modifier = modifier,
        )
    }
}
