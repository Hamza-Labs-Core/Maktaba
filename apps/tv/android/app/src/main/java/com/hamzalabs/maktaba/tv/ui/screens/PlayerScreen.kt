package com.hamzalabs.maktaba.tv.ui.screens

import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.viewinterop.AndroidView
import androidx.media3.common.MediaItem
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.ui.PlayerView
import com.hamzalabs.maktaba.tv.data.repository.MediaRepository

/**
 * Full-screen HLS player. Media3's [PlayerView] provides the native TV
 * transport controls (play/pause/seek with the D-pad) so we don't
 * re-implement them. The [ExoPlayer] is built once and released in the
 * [DisposableEffect] teardown to avoid leaking the codec/surface.
 */
@androidx.annotation.OptIn(UnstableApi::class)
@Composable
fun PlayerScreen(
    repository: MediaRepository,
    videoId: String,
    hlsUrl: String? = null,
) {
    val context = LocalContext.current
    val manifest = hlsUrl ?: repository.hlsUrl(videoId)

    val player = remember {
        ExoPlayer.Builder(context).build().apply {
            setMediaItem(MediaItem.fromUri(manifest))
            prepare()
            playWhenReady = true
        }
    }

    DisposableEffect(Unit) {
        onDispose { player.release() }
    }

    AndroidView(
        modifier = Modifier.fillMaxSize(),
        factory = { ctx ->
            PlayerView(ctx).apply {
                this.player = player
                useController = true
            }
        },
    )
}
