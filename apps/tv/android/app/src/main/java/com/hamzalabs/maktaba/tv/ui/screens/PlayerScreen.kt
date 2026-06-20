package com.hamzalabs.maktaba.tv.ui.screens

import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.viewinterop.AndroidView
import androidx.media3.common.MediaItem
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.ui.PlayerView
import com.hamzalabs.maktaba.tv.data.repository.MediaRepository
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch

/**
 * Full-screen HLS player. Media3's [PlayerView] provides the native TV
 * transport controls (play/pause/seek with the D-pad) so we don't
 * re-implement them. The [ExoPlayer] is built once and released in the
 * [DisposableEffect] teardown to avoid leaking the codec/surface.
 *
 * On top of playback this screen drives the watch-analytics lifecycle
 * (Story 29.1): it opens a session, beats every 30 s with the current
 * position, and closes the session on playback-end or screen teardown.
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
        // A scope detached from composition so the final stop() in
        // onDispose still runs after the screen is torn down. The
        // heartbeat loop and end-listener share it; it is cancelled in
        // onDispose after the closing stop() is dispatched.
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
        // Holds the live session id. null = tracking paused or closed.
        var sessionId: String? = null

        fun positionSec(): Long = (player.currentPosition / 1000).coerceAtLeast(0)

        // Close once; idempotent via the id-null guard. Reads position on
        // the player's (main) thread before dispatching the POST.
        fun stop() {
            val id = sessionId ?: return
            sessionId = null
            val pos = positionSec()
            scope.launch { repository.watchStop(id, pos) }
        }

        // Open the session, then beat every 30 s while it is open.
        val beatJob = scope.launch {
            sessionId = repository.watchStart(videoId).getOrNull()
            if (sessionId != null) {
                while (isActive && sessionId != null) {
                    delay(30_000)
                    val id = sessionId ?: break
                    repository.watchHeartbeat(id, positionSec())
                }
            }
        }

        val listener = object : Player.Listener {
            override fun onPlaybackStateChanged(state: Int) {
                if (state == Player.STATE_ENDED) stop()
            }
        }
        player.addListener(listener)

        onDispose {
            beatJob.cancel()
            stop() // dispatched on `scope`, which we leave alive briefly
            player.removeListener(listener)
            player.release()
            // The stop() launch above runs on Main.immediate, so it has
            // already been dispatched; cancel only the still-pending beat
            // structure, not the in-flight stop request body.
        }
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
