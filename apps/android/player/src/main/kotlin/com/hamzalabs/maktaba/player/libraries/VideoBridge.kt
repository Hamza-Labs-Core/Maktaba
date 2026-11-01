package com.hamzalabs.maktaba.player.libraries

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Surface
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LifecycleEventEffect
import com.hamzalabs.maktaba.core.api.Failure
import com.hamzalabs.maktaba.core.api.Video
import com.hamzalabs.maktaba.core.api.videoDetail
import com.hamzalabs.maktaba.core.ui.ErrorState
import com.hamzalabs.maktaba.player.Playback
import com.hamzalabs.maktaba.player.PlayerApplication
import com.hamzalabs.maktaba.player.R
import com.hamzalabs.maktaba.player.VideoPage
import com.hamzalabs.maktaba.player.WatchScreen
import com.hamzalabs.maktaba.player.shell.LocalPlayerHome

// How the Libraries screens open a video. The video's page (48.28) and the player route (48.32)
// are placeholders until those stories merge, and Libraries is the section that ships, so until
// then a video opens today's page and player here, over the screen: a TestFlight build between
// the two never loses playback. When 48.28 merges, [VideoHost] provides
// `{ nav.open(Route.Video(it)) }` instead, and the rest of this file goes.

/** Opens the video with this id; provided by [VideoHost] around each screen of the section. */
val LocalOpenVideo = staticCompositionLocalOf<(String) -> Unit> { {} }

/** Gives [content] a way to open a video ([LocalOpenVideo]), and shows the opened one over it. */
@Composable
internal fun VideoHost(content: @Composable () -> Unit) {
    var opened by rememberSaveable { mutableStateOf<String?>(null) }
    CompositionLocalProvider(LocalOpenVideo provides { id -> opened = id }) {
        Box(Modifier.fillMaxSize()) {
            content()
            opened?.let { id -> TodaysVideo(id, onClose = { opened = null }) }
        }
    }
}

/** Today's video page ([VideoPage]) for [id], and the player full screen once it plays. */
@Composable
private fun TodaysVideo(id: String, onClose: () -> Unit) {
    val home = LocalPlayerHome.current
    val scope = rememberCoroutineScope()
    val app = LocalContext.current.applicationContext as PlayerApplication
    val playback = remember(home.client) { Playback(app, home.client, scope, app.background, home.session::signedOut) }
    var video by remember(id) { mutableStateOf<Video?>(null) }
    var problem by remember(id) { mutableStateOf<Failure?>(null) }

    DisposableEffect(playback) {
        onDispose {
            playback.stop()
            home.onPlaying(false)
        }
    }
    LifecycleEventEffect(Lifecycle.Event.ON_STOP) { playback.player?.pause() }
    LaunchedEffect(playback.video != null) { home.onPlaying(playback.video != null) }
    LaunchedEffect(id) {
        loading(home.session::signedOut, "player video", { problem = it }) {
            val d = home.client.videoDetail(id)
            video = Video(d.id, d.libraryId, d.filename, d.displayTitle, d.description, d.durationSec)
        }
    }

    BackHandler(onBack = onClose)
    Surface(Modifier.fillMaxSize()) {
        val v = video
        val f = problem
        when {
            v != null -> VideoPage(home.client, v, onPlay = { playback.play(v) }, onBack = onClose)
            f != null -> ErrorState(
                f,
                title = stringResource(R.string.libraries_not_available),
                onRetry = null,
                onChooseHome = home.session::switchHome,
                onBack = onClose,
            )
            else -> Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) { CircularProgressIndicator() }
        }
    }
    if (playback.video != null) {
        BackHandler { playback.stop() }
        WatchScreen(playback, fullScreen = true, onSignIn = home.session::signedOut, onChooseHome = home.session::switchHome)
    }
}
