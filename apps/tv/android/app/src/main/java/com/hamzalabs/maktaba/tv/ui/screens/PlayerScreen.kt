package com.hamzalabs.maktaba.tv.ui.screens

import android.view.KeyEvent
import androidx.annotation.OptIn
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.viewinterop.AndroidView
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.media3.common.util.UnstableApi
import androidx.media3.ui.PlayerView
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.tv.MaktabaTVApp
import com.hamzalabs.maktaba.tv.R
import com.hamzalabs.maktaba.tv.playback.TvPlayback
import com.hamzalabs.maktaba.core.R as CoreR
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaPalette
import androidx.compose.foundation.layout.padding
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.unit.dp
import androidx.media3.common.Player
import com.hamzalabs.maktaba.core.queue.PlayQueueModel
import com.hamzalabs.maktaba.core.queue.QueueItem
import com.hamzalabs.maktaba.tv.ui.screens.library.PlaybackPrefs
import com.hamzalabs.maktaba.tv.ui.screens.library.QueueBar
import com.hamzalabs.maktaba.tv.ui.screens.library.UpNextCard
import com.hamzalabs.maktaba.tv.ui.screens.library.upNextState
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/**
 * Full screen playback. Media3's [PlayerView] is the transport (play/pause, seek with the
 * D-pad, the audio and subtitle choices); [TvPlayback] owns the stream session. Back stops it:
 * the last position goes to the home, so Continue Watching and Resume pick up here.
 */
@OptIn(UnstableApi::class)
@Composable
fun PlayerScreen(
    app: MaktabaTVApp,
    client: ApiClient,
    video: String,
    title: String,
    startMs: Long,
    onBack: () -> Unit,
    signedOut: () -> Unit,
    /** The server queue this video plays in (Epic 56), and how to play another of its items. */
    queue: PlayQueueModel? = null,
    onQueueItem: (QueueItem) -> Unit = {},
) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val playback = remember(video, startMs) { TvPlayback(context, client, video, startMs, scope, app.background, signedOut) }
    DisposableEffect(playback) {
        playback.start()
        onDispose { playback.stop() }
    }
    // Home, standby or another app: leave the player (its position goes to the home, the
    // session closes); coming back finds the video page with Resume
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    val leave by rememberUpdatedState(onBack)
    DisposableEffect(lifecycle) {
        val observer = LifecycleEventObserver { _, event -> if (event == Lifecycle.Event.ON_STOP) leave() }
        lifecycle.addObserver(observer)
        onDispose { lifecycle.removeObserver(observer) }
    }

    // Epic 56 stories 56.02 and 56.11: the queue's Previous / Next / Shuffle again, and Up next
    var barOpen by remember(video) { mutableStateOf(false) }
    var next by remember(video) { mutableStateOf<QueueItem?>(null) }
    var cancelled by remember(video) { mutableStateOf(false) }
    var upNext by remember(video) { mutableStateOf<Int?>(null) }
    val autoplay = remember { PlaybackPrefs.autoplay(context) }
    fun go(move: suspend PlayQueueModel.() -> QueueItem?) {
        val q = queue ?: return
        scope.launch { runCatching { q.move() }.getOrNull()?.let(onQueueItem) }
    }
    LaunchedEffect(video, queue) {
        if (queue == null) return@LaunchedEffect
        var endedAt: Long? = null
        while (true) {
            delay(500)
            val p = playback.player ?: continue
            val dur = p.duration
            if (dur <= 0) continue
            val ended = p.playbackState == Player.STATE_ENDED
            if (ended && endedAt == null) endedAt = System.currentTimeMillis()
            val remaining = (dur - p.currentPosition) / 1000.0
            val state = upNextState(remaining, endedAt?.let { (System.currentTimeMillis() - it) / 1000.0 }, autoplay)
            if (state != null && next == null && !cancelled) next = runCatching { queue.peekNext() }.getOrNull()
            upNext = if (cancelled || next == null) null else state
            if (state == 0 && next != null && !cancelled) {
                go { next() }
                return@LaunchedEffect
            }
            if (ended && next == null && cancelled) return@LaunchedEffect
        }
    }

    Box(Modifier.fillMaxSize().background(Color.Black)) {
        val player = playback.player
        if (player != null) {
            AndroidView(
                modifier = Modifier.fillMaxSize(),
                factory = { ctx ->
                    PlayerView(ctx).apply {
                        useController = true
                        setShowSubtitleButton(true)
                        controllerShowTimeoutMs = 4_000
                        keepScreenOn = true
                        isFocusable = true
                        isFocusableInTouchMode = true
                        // the remote's keys go to the transport; Back leaves (Compose's BackHandler)
                        setOnKeyListener { _, code, event ->
                            if (code == KeyEvent.KEYCODE_BACK) return@setOnKeyListener false
                            if (queue != null && event.action == KeyEvent.ACTION_DOWN) {
                                when (code) {
                                    KeyEvent.KEYCODE_MEDIA_NEXT -> { go { next() }; return@setOnKeyListener true }
                                    KeyEvent.KEYCODE_MEDIA_PREVIOUS -> { go { previous() }; return@setOnKeyListener true }
                                    KeyEvent.KEYCODE_MENU, KeyEvent.KEYCODE_INFO -> { barOpen = !barOpen; return@setOnKeyListener true }
                                }
                            }
                            if (event.action == KeyEvent.ACTION_DOWN && !isControllerFullyVisible) showController()
                            dispatchMediaKeyEvent(event)
                        }
                    }
                },
                update = { view ->
                    if (view.player !== player) view.player = player
                    // the queue bar and the Up next card take focus while shown
                    if (!view.hasFocus() && !barOpen && upNext == null) view.requestFocus()
                },
            )
        }
        val nextItem = next
        val left = upNext
        if (nextItem != null && left != null) {
            UpNextCard(
                nextItem, left,
                onPlayNow = { go { next() } },
                onCancel = { cancelled = true; upNext = null },
                modifier = Modifier.align(Alignment.BottomEnd).padding(48.dp),
            )
        }
        if (barOpen && queue != null) {
            QueueBar(
                shuffled = queue.doc?.shuffle == true,
                onPrevious = { barOpen = false; go { previous() } },
                onNext = { barOpen = false; go { next() } },
                onReshuffle = { barOpen = false; scope.launch { runCatching { queue.reshuffle() } } },
                modifier = Modifier.align(Alignment.TopCenter).padding(32.dp),
            )
        }
        val problem = playback.problem
        when {
            problem != null -> TvErrorState(
                problem,
                title = stringResource(CoreR.string.error_playback_title),
                video = true,
                onRetry = playback::retry,
                onSignIn = signedOut,
                onChooseHome = { app.session.switchHome() },
                onBack = onBack,
            )
            playback.opening -> Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                Text(
                    stringResource(R.string.preparing) + "\n" + title,
                    style = MaterialTheme.typography.titleLarge,
                    color = MaktabaPalette.Text,
                )
            }
        }
    }
}
