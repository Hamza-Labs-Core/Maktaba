package com.hamzalabs.maktaba.player

import androidx.activity.compose.LocalActivity
import androidx.annotation.OptIn
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.filled.PlayCircle
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LocalContentColor
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.DisposableEffect
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.view.WindowCompat
import androidx.core.view.WindowInsetsCompat
import androidx.core.view.WindowInsetsControllerCompat
import androidx.media3.common.util.UnstableApi
import androidx.media3.ui.PlayerView
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.Video
import com.hamzalabs.maktaba.core.ui.ErrorState
import com.hamzalabs.maktaba.core.ui.InkAndBrass
import com.hamzalabs.maktaba.core.R as CoreR
import com.hamzalabs.maktaba.core.ui.Poster
import com.hamzalabs.maktaba.core.ui.formatDuration

/**
 * The video with its title and a close button over it (Media3's own controls on the video).
 * Full screen (the system bars hidden until a swipe), or one half of a half-folded foldable.
 */
@OptIn(UnstableApi::class)
@Composable
fun WatchScreen(playback: Playback, fullScreen: Boolean, onSignIn: () -> Unit, onChooseHome: () -> Unit) {
    if (fullScreen) ImmersiveWhileShown()
    Box(Modifier.fillMaxSize().background(Color.Black)) {
        val player = playback.player
        val problem = playback.problem
        when {
            player != null -> AndroidView(
                factory = { ctx ->
                    PlayerView(ctx).apply {
                        setShowBuffering(PlayerView.SHOW_BUFFERING_WHEN_PLAYING)
                        setShowSubtitleButton(true)
                        keepScreenOn = true
                    }
                },
                update = { it.player = player },
                onRelease = { it.player = null },
                modifier = Modifier.fillMaxSize(),
            )
            problem != null -> CompositionLocalProvider(LocalContentColor provides Color.White) {
                ErrorState(
                    problem,
                    title = stringResource(CoreR.string.error_playback_title),
                    modifier = Modifier.safeDrawingPadding().padding(top = 48.dp),
                    video = true,
                    onRetry = playback::retry,
                    onSignIn = { playback.stop(); onSignIn() },
                    onChooseHome = { playback.stop(); onChooseHome() },
                    onBack = playback::stop,
                )
            }
            else -> CircularProgressIndicator(Modifier.align(Alignment.Center), color = InkAndBrass.Brass)
        }
        Row(
            Modifier.fillMaxWidth().safeDrawingPadding().padding(12.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            IconButton(
                onClick = playback::stop,
                modifier = Modifier.clip(CircleShape).background(Color.Black.copy(alpha = 0.45f)),
            ) { Icon(Icons.Filled.Close, stringResource(CoreR.string.close), tint = Color.White) }
            Text(
                playback.video?.displayTitle ?: "",
                color = Color.White,
                style = MaterialTheme.typography.titleMedium,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}

/** Hides the status and navigation bars while on screen (a swipe shows them for a moment). */
@Composable
private fun ImmersiveWhileShown() {
    val window = LocalActivity.current?.window ?: return
    DisposableEffect(window) {
        val bars = WindowCompat.getInsetsController(window, window.decorView)
        bars.systemBarsBehavior = WindowInsetsControllerCompat.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE
        bars.hide(WindowInsetsCompat.Type.systemBars())
        onDispose { bars.show(WindowInsetsCompat.Type.systemBars()) }
    }
}

/**
 * A video's page: its poster (tap to play), title, length and description. Playing hands the
 * video to the app's [Playback]; where it shows (full screen, or the other half of a
 * half-folded foldable) is the browser's call.
 */
@kotlin.OptIn(ExperimentalMaterial3Api::class)
@Composable
fun VideoPage(client: ApiClient, video: Video, onPlay: () -> Unit, onBack: (() -> Unit)? = null) {
    val playNamed = stringResource(R.string.play_named, video.displayTitle)
    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(video.displayTitle, maxLines = 1, overflow = TextOverflow.Ellipsis) },
                navigationIcon = { if (onBack != null) BackButton(onBack) },
                actions = { IconButton(onClick = onPlay) { Icon(Icons.Filled.PlayArrow, stringResource(R.string.play)) } },
            )
        },
    ) { padding ->
        Box(Modifier.padding(padding).fillMaxSize(), contentAlignment = Alignment.TopCenter) {
            Column(
                Modifier.widthIn(max = 900.dp).verticalScroll(rememberScrollState()).padding(16.dp),
                verticalArrangement = Arrangement.spacedBy(16.dp),
            ) {
                Box(
                    Modifier.clip(RoundedCornerShape(12.dp)).clickable(onClick = onPlay)
                        .semantics { contentDescription = playNamed },
                    contentAlignment = Alignment.Center,
                ) {
                    Poster(client, video.id, Modifier.fillMaxWidth())
                    Icon(Icons.Filled.PlayCircle, null, tint = InkAndBrass.Brass, modifier = Modifier.size(72.dp))
                }
                Text(video.displayTitle, style = MaterialTheme.typography.headlineSmall)
                video.durationSec?.let {
                    Text(formatDuration(it), style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
                video.description?.takeIf { it.isNotEmpty() }?.let {
                    Text(it, style = MaterialTheme.typography.bodyLarge, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
                Button(onClick = onPlay) {
                    Icon(Icons.Filled.PlayArrow, null)
                    Text(stringResource(R.string.play), Modifier.padding(start = 8.dp))
                }
            }
        }
    }
}
