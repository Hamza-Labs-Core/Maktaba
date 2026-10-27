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
                            if (event.action == KeyEvent.ACTION_DOWN && !isControllerFullyVisible) showController()
                            dispatchMediaKeyEvent(event)
                        }
                    }
                },
                update = { view ->
                    if (view.player !== player) view.player = player
                    if (!view.hasFocus()) view.requestFocus()
                },
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
