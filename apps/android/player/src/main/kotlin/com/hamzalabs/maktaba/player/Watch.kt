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
import androidx.compose.material.icons.filled.Cast
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
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.layout.boundsInWindow
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.media3.common.C
import com.hamzalabs.maktaba.player.media.pictureInPicture
import com.hamzalabs.maktaba.player.watch.PlayerClock
import com.hamzalabs.maktaba.player.watch.PlayerControls
import com.hamzalabs.maktaba.player.watch.UpNext
import com.hamzalabs.maktaba.player.watch.upNextDue
import kotlinx.coroutines.delay
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.view.WindowCompat
import androidx.core.view.WindowInsetsCompat
import androidx.core.view.WindowInsetsControllerCompat
import androidx.media3.common.util.UnstableApi
import androidx.media3.ui.PlayerView
import androidx.media3.ui.SubtitleView
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.Video
import com.hamzalabs.maktaba.core.ui.ErrorState
import com.hamzalabs.maktaba.core.ui.InkAndBrass
import com.hamzalabs.maktaba.core.R as CoreR
import com.hamzalabs.maktaba.core.ui.Poster
import com.hamzalabs.maktaba.core.ui.formatDuration

/**
 * The video with the player's own controls over it ([PlayerControls]: close and the title,
 * subtitles and audio, quality, play / pause, ±10 s, the scrubber, Next) and Up next near the
 * end. Full screen (the system bars hidden until a swipe), or one half of a half-folded foldable.
 *
 * [next] is the video after this one (the series' next episode), if any: the Next button plays
 * it, and so does Up next's countdown when [autoplayNext] (Settings, FR-31) is on.
 *
 * Full screen, going Home while it plays shrinks it into a picture-in-picture window (FR-37,
 * 48.33), which shows the video alone; closing the window pauses it. While a Cast receiver plays
 * it (FR-38), the screen says so and the controls drive the receiver.
 */
@OptIn(UnstableApi::class)
@Composable
fun WatchScreen(
    playback: Playback,
    fullScreen: Boolean,
    onSignIn: () -> Unit,
    onChooseHome: () -> Unit,
    onClose: () -> Unit = playback::stop,
    next: Video? = null,
    autoplayNext: Boolean = false,
    onPlayNext: (Video) -> Unit = { playback.play(it) },
    /** The subtitles' size, as a scale of the player's own (Settings, FR-31). */
    subtitleScale: Float = 1f,
) {
    if (fullScreen) ImmersiveWhileShown()
    // where the video is drawn, in the window: the picture-in-picture window grows out of it
    var videoRect by remember { mutableStateOf<android.graphics.Rect?>(null) }
    val inPip = if (fullScreen) {
        pictureInPicture(
            player = playback.control,
            playing = playback.isPlaying,
            videoWidth = playback.videoSize.width,
            videoHeight = playback.videoSize.height,
            sourceRect = videoRect,
            enabled = playback.player != null && !playback.casting,
            onDismissed = { playback.control?.pause() },
        )
    } else {
        false
    }
    Box(Modifier.fillMaxSize().background(Color.Black)) {
        val player = playback.player
        val control = playback.control ?: player
        val problem = playback.problem
        val title = playback.video?.displayTitle ?: ""
        when {
            player != null -> {
                AndroidView(
                    factory = { ctx ->
                        PlayerView(ctx).apply {
                            // the controls are the app's own (PlayerControls); the view draws the video and its subtitles
                            useController = false
                            setShowBuffering(PlayerView.SHOW_BUFFERING_WHEN_PLAYING)
                            keepScreenOn = true
                        }
                    },
                    update = {
                        it.player = player
                        it.subtitleView?.setFractionalTextSize(SubtitleView.DEFAULT_TEXT_SIZE_FRACTION * subtitleScale)
                    },
                    onRelease = { it.player = null },
                    modifier = Modifier.fillMaxSize().onGloballyPositioned { coordinates ->
                        val r = coordinates.boundsInWindow()
                        val rect = android.graphics.Rect(r.left.toInt(), r.top.toInt(), r.right.toInt(), r.bottom.toInt())
                        if (rect != videoRect) videoRect = rect
                    },
                )
                val clock = remember(player) { PlayerClock() }
                // where it plays: here, or on the receiver while casting
                LaunchedEffect(control) {
                    val c = control ?: return@LaunchedEffect
                    while (true) {
                        clock.positionMs = c.currentPosition
                        clock.durationMs = c.duration.takeIf { it != C.TIME_UNSET && it > 0 }
                            ?: playback.video?.durationSec?.let { (it * 1000).toLong() } ?: 0L
                        delay(500)
                    }
                }
                if (playback.casting) Casting(title, Modifier.align(Alignment.Center))
                // the window shows the video alone
                if (inPip) return@Box
                PlayerControls(
                    playback,
                    clock,
                    title = title,
                    onClose = onClose,
                    hasNext = next != null,
                    onNext = { next?.let(onPlayNext) },
                )
                UpNext(
                    next = next,
                    due = upNextDue(clock.positionMs, clock.durationMs, playback.ended),
                    autoplay = autoplayNext,
                    onPlay = onPlayNext,
                    modifier = Modifier.align(Alignment.BottomEnd).safeDrawingPadding().padding(16.dp).padding(bottom = 72.dp),
                )
            }
            problem != null -> CompositionLocalProvider(LocalContentColor provides Color.White) {
                ErrorState(
                    problem,
                    title = stringResource(CoreR.string.error_playback_title),
                    modifier = Modifier.safeDrawingPadding().padding(top = 48.dp),
                    video = true,
                    onRetry = playback::retry,
                    onSignIn = { playback.stop(); onSignIn() },
                    onChooseHome = { playback.stop(); onChooseHome() },
                    onBack = onClose,
                )
            }
            else -> CircularProgressIndicator(Modifier.align(Alignment.Center), color = InkAndBrass.Brass)
        }
        // while it opens, or failed: close and the title (the controls carry them once it plays)
        if (player == null && !inPip) {
            Row(
                Modifier.fillMaxWidth().safeDrawingPadding().padding(12.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(12.dp),
            ) {
                IconButton(
                    onClick = onClose,
                    modifier = Modifier.clip(CircleShape).background(Color.Black.copy(alpha = 0.45f)),
                ) { Icon(Icons.Filled.Close, stringResource(CoreR.string.close), tint = Color.White) }
                Text(
                    title,
                    color = Color.White,
                    style = MaterialTheme.typography.titleMedium,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
    }
}

/** A Cast receiver plays the video: the phone says so (the controls over it drive the receiver). */
@Composable
private fun Casting(title: String, modifier: Modifier) {
    Column(
        modifier.padding(32.dp).testTag("casting"),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Icon(Icons.Filled.Cast, null, tint = InkAndBrass.Brass, modifier = Modifier.size(56.dp))
        Text(stringResource(R.string.watch_casting), color = Color.White, style = MaterialTheme.typography.titleMedium, textAlign = TextAlign.Center)
        Text(title, color = Color.White.copy(alpha = 0.7f), style = MaterialTheme.typography.bodyMedium, textAlign = TextAlign.Center)
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
