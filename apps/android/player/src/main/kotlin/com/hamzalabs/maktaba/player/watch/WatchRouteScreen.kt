package com.hamzalabs.maktaba.player.watch

import androidx.activity.compose.BackHandler
import androidx.annotation.OptIn
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.runtime.staticCompositionLocalOf
import android.content.Context
import android.net.ConnectivityManager
import androidx.compose.ui.platform.LocalContext
import androidx.core.net.ConnectivityManagerCompat
import com.hamzalabs.maktaba.player.profile.playbackSettings
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.ExoPlayer
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.Video
import com.hamzalabs.maktaba.core.api.VideoDetail
import com.hamzalabs.maktaba.core.api.videoDetail
import com.hamzalabs.maktaba.player.Playback
import com.hamzalabs.maktaba.player.PlayerApplication
import com.hamzalabs.maktaba.player.WatchScreen
import com.hamzalabs.maktaba.player.shell.LocalPlayerHome
import com.hamzalabs.maktaba.player.shell.PlayerNavigator
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob

/**
 * The player's last touches for [WatchRouteScreen]: the app's are none; a test gives a clock
 * and renderers that run on the JVM.
 */
@OptIn(UnstableApi::class)
val LocalPlayerBuild = staticCompositionLocalOf<(ExoPlayer.Builder) -> ExoPlayer.Builder> { { it } }

/**
 * The player opened as a route (48.32): [videoId] from [startSec], or, when null, where the
 * person left it (`playback_state.position`, FR-32; a finished video starts again). Full screen
 * with the shell's navigation hidden; Close and Back return to the screen that opened it. The
 * series' next episode is looked up for Next and Up next (FR-33), with Settings' "play the next
 * episode automatically" (FR-31).
 */
@Composable
fun WatchRouteScreen(videoId: String, startSec: Int?, nav: PlayerNavigator) {
    val home = LocalPlayerHome.current
    val client = home.client
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val build = LocalPlayerBuild.current
    // the one playback store, which Profile's Settings writes (FR-31)
    val prefs = playbackSettings()
    val settings by prefs.value.collectAsState()
    val playback = remember(client) {
        // work that outlives the screen (the last position, the session's close): the app's
        val background = (context.applicationContext as? PlayerApplication)?.background
            ?: home.keep("watch.background") { CoroutineScope(SupervisorJob() + Dispatchers.Default) }
        Playback(context.applicationContext, client, scope, background, home.session::signedOut, build)
    }
    var next by remember { mutableStateOf<Video?>(null) }

    DisposableEffect(playback) {
        home.onPlaying(true)
        onDispose {
            playback.stop()
            home.onPlaying(false)
        }
    }
    // the screen off or the app left: the sound goes on (FR-39, the media session's service), and
    // the notification pauses it; closing the picture-in-picture window pauses it (WatchScreen)
    LaunchedEffect(settings) { playback.prefs = settings }
    LaunchedEffect(videoId, startSec) {
        val detail = try {
            client.videoDetail(videoId)
        } catch (e: CancellationException) {
            throw e
        } catch (_: ApiException) {
            null // the stream's own answer says why (Playback shows it)
        }
        playback.prefs = prefs.current
        playback.startAt(prefs.current.startKbps(metered(context)))
        playback.play(detail?.asVideo() ?: Video(videoId), startSec ?: resumeSec(detail))
    }
    // the video after the one playing (it changes when Up next or Next plays the next)
    LaunchedEffect(playback.video?.id) {
        next = null
        next = playback.video?.id?.let { client.nextVideo(it) }
    }

    val close = { playback.stop(); nav.back(); Unit }
    BackHandler(onBack = close)
    WatchScreen(
        playback,
        fullScreen = true,
        onSignIn = home.session::signedOut,
        onChooseHome = home.session::switchHome,
        onClose = close,
        next = next,
        autoplayNext = settings.autoplayNext,
        onPlayNext = { playback.play(it) },
        subtitleScale = settings.subtitleSize.scale,
    )
}

/** True on a metered network (mobile data): Settings' mobile quality applies. */
fun metered(context: Context): Boolean = runCatching {
    context.getSystemService(ConnectivityManager::class.java)?.let { ConnectivityManagerCompat.isActiveNetworkMetered(it) } ?: false
}.getOrDefault(false)

/** Where a video opened without a position starts: its resume point, else the start. */
fun resumeSec(detail: VideoDetail?): Int = detail?.resumeAtSec?.toInt() ?: 0

private fun VideoDetail.asVideo() = Video(
    id = id,
    libraryId = libraryId,
    filename = filename,
    title = displayTitle,
    description = description,
    durationSec = durationSec,
)
