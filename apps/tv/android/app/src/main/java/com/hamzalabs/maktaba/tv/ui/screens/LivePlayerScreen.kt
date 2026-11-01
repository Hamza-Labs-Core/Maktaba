package com.hamzalabs.maktaba.tv.ui.screens

import android.content.Context
import androidx.activity.compose.BackHandler
import androidx.annotation.OptIn
import androidx.compose.foundation.background
import androidx.compose.foundation.focusable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
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
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.Failure
import com.hamzalabs.maktaba.core.api.LiveApi
import com.hamzalabs.maktaba.core.api.LiveFailure
import com.hamzalabs.maktaba.core.api.LiveSource
import com.hamzalabs.maktaba.core.api.LiveStream
import com.hamzalabs.maktaba.core.api.NowAnswer
import com.hamzalabs.maktaba.core.api.NowEntry
import com.hamzalabs.maktaba.core.diagnostics.AppLog
import com.hamzalabs.maktaba.tv.MaktabaTVApp
import com.hamzalabs.maktaba.tv.R
import com.hamzalabs.maktaba.tv.data.TvApi
import com.hamzalabs.maktaba.tv.playback.tvPlaybackFailure
import com.hamzalabs.maktaba.tv.ui.components.MaktabaButton
import com.hamzalabs.maktaba.tv.ui.components.ReadingPanel
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaPalette
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/**
 * The words for a channel that could not start, by [LiveFailure.code] (the web's `live.remote.*`);
 * null: a failure like any other (the error screen's words for its kind).
 */
fun liveFailureWords(code: String): Int? = when (code) {
    "budget_exhausted" -> R.string.live_remote_budget
    "user_streams_full" -> R.string.live_remote_user_full
    "remote_channels_full", "remote_storage_full" -> R.string.live_remote_channels_full
    "remote_unavailable", "remote_not_configured" -> R.string.live_remote_not_set_up
    "remote_not_allowed" -> R.string.live_remote_not_allowed
    "remote_timeout" -> R.string.live_remote_timeout
    "no_such_channel" -> R.string.live_not_found
    else -> null
}

/**
 * One channel playing on the TV (FR-23). [LiveSource] picks where from: at home the web port's
 * HLS, away from home a remote session it tunes and keeps beating; the player's OkHttp is the
 * core's stream client (the token on every request to the home, `StreamAuthInterceptor`) with
 * the source's interceptor (the newest signed token on every Cloudflare request). A channel
 * just asked for may answer with an error while its encoder warms up, so a failure is tried
 * again a few times before the error screen.
 */
@OptIn(UnstableApi::class)
class LivePlayback(
    private val context: Context,
    client: ApiClient,
    live: LiveApi,
    private val scope: CoroutineScope,
    /** Outlives the screen: the remote session still ends on the home. */
    private val background: CoroutineScope,
    device: String,
    private val signedOut: () -> Unit,
) {
    var player by mutableStateOf<ExoPlayer?>(null)
        private set
    var channelId by mutableStateOf<String?>(null)
        private set
    /** Asking the home for the channel (or waiting for a remote session to start). */
    var tuning by mutableStateOf(false)
        private set
    /** Playing from a remote session, away from home. */
    var relay by mutableStateOf(false)
        private set
    /** Why nothing plays: a failure the error screen words by its kind. */
    var failure by mutableStateOf<Failure?>(null)
        private set
    /** Why nothing plays: a refused remote session, in its own words. */
    var refusal by mutableStateOf<Int?>(null)
        private set

    private val source = LiveSource(live, scope, device, onEnded = {
        player?.stop()
        refusal = R.string.live_remote_ended
    })
    private val http = client.streamHttp().newBuilder().addInterceptor(source.interceptor).build()
    private var job: Job? = null
    private var retries = 0

    /** Tunes [id]: the channel before it stops (its remote session ends on the home). */
    fun tune(id: String) {
        job?.cancel()
        channelId = id
        failure = null
        refusal = null
        tuning = true
        val mine = scope.launch {
            try {
                val s = source.open(id)
                relay = s is LiveStream.Relay
                val p = player ?: newPlayer().also { player = it }
                retries = 0
                p.setMediaItem(MediaItem.Builder().setUri(s.url.toString()).setMimeType(MimeTypes.APPLICATION_M3U8).build())
                p.prepare()
                p.playWhenReady = true
            } catch (e: CancellationException) {
                throw e
            } catch (e: ApiException.SignedOut) {
                signedOut()
            } catch (e: LiveFailure) {
                AppLog.failure("tv live tune", e)
                player?.stop()
                val words = liveFailureWords(e.code)
                if (words != null) refusal = words else failure = Failure.of(e.cause ?: e)
            } catch (e: Exception) {
                AppLog.failure("tv live tune", e)
                player?.stop()
                failure = Failure.of(e)
            }
        }
        job = mine
        mine.invokeOnCompletion { if (job === mine) tuning = false }
    }

    /** Tries the channel again (a person pressed Try again). */
    fun retry() {
        channelId?.let(::tune)
    }

    /** Back to the live edge after a pause. */
    fun backToLive() {
        val p = player ?: return
        p.seekToDefaultPosition()
        p.playWhenReady = true
    }

    /** Stops for good: the player goes, and a remote session ends on the home. */
    fun stop() {
        job?.cancel()
        job = null
        player?.release()
        player = null
        background.launch { source.close() }
    }

    private fun newPlayer(): ExoPlayer {
        val data = OkHttpDataSource.Factory(http)
        return ExoPlayer.Builder(context)
            .setMediaSourceFactory(DefaultMediaSourceFactory(context).setDataSourceFactory(data))
            .setAudioAttributes(
                AudioAttributes.Builder().setUsage(C.USAGE_MEDIA).setContentType(C.AUDIO_CONTENT_TYPE_MOVIE).build(),
                true,
            )
            .build()
            .also { p ->
                p.addListener(object : Player.Listener {
                    override fun onPlayerError(error: PlaybackException) {
                        if (retries < WARM_UP_RETRIES) {
                            retries++
                            scope.launch {
                                delay(WARM_UP_DELAY_MS)
                                if (player === p) p.prepare()
                            }
                        } else {
                            AppLog.failure("tv live player", error)
                            failure = tvPlaybackFailure(error)
                        }
                    }
                })
            }
    }

    private companion object {
        const val WARM_UP_RETRIES = 6
        const val WARM_UP_DELAY_MS = 2_000L
    }
}

/**
 * A channel full screen, driven by the D-pad: ▲ and ▼ (and the remote's channel keys) change
 * channel, Select opens the mini-guide (what is on every channel now; ▲ ▼ and Select tune), and
 * after a pause Select goes back to live. Back closes the mini-guide, then leaves.
 */
@OptIn(UnstableApi::class)
@Composable
fun LivePlayerScreen(app: MaktabaTVApp, api: TvApi, channelId: String, onBack: () -> Unit, signedOut: () -> Unit) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val device = stringResource(R.string.tv_label)
    val playback = remember(channelId) { LivePlayback(context, api.client, api.live, scope, app.background, device, signedOut) }
    DisposableEffect(playback) {
        playback.tune(channelId)
        onDispose { playback.stop() }
    }
    // Home, standby or another app: leave the channel (its remote session ends)
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    val leave by rememberUpdatedState(onBack)
    DisposableEffect(lifecycle) {
        val observer = LifecycleEventObserver { _, event -> if (event == Lifecycle.Event.ON_STOP) leave() }
        lifecycle.addObserver(observer)
        onDispose { lifecycle.removeObserver(observer) }
    }

    // what is on every channel, for the banner and the mini-guide; refreshed every minute
    var now by remember { mutableStateOf<Timed<NowAnswer>?>(null) }
    LaunchedEffect(api) {
        while (true) {
            now = runLoad("tv live now", signedOut) { Timed<NowAnswer>(api.live.now()) }.let { (it as? Load.Ready)?.value } ?: now
            delay(60_000)
        }
    }
    val serverNow = rememberServerNow(now, tickMs = 5_000)
    val channels = remember(now) { now?.value?.channels.orEmpty().sortedWith(compareBy({ it.channel.number }, { it.channel.name })) }
    val ids = remember(channels) { channels.map { it.channel.id } }

    var banner by remember { mutableIntStateOf(0) } // bumped to show the banner again
    var showBanner by remember { mutableStateOf(true) }
    var mini by remember { mutableStateOf<Int?>(null) }
    var paused by remember { mutableStateOf(false) }
    var behind by remember { mutableStateOf(false) }
    LaunchedEffect(banner, playback.channelId) {
        showBanner = true
        delay(5_000)
        showBanner = false
    }
    LaunchedEffect(playback.player) {
        val p = playback.player ?: return@LaunchedEffect
        while (true) {
            paused = !p.playWhenReady
            behind = p.isCurrentMediaItemLive && p.currentLiveOffset != C.TIME_UNSET && p.currentLiveOffset > BEHIND_LIVE_MS
            delay(1_000)
        }
    }
    BackHandler(enabled = mini != null) { mini = null }

    fun tune(step: Int) {
        stepChannel(ids, playback.channelId, step)?.let { if (it != playback.channelId) playback.tune(it) }
        banner++
    }

    val failure = playback.failure
    val refusal = playback.refusal
    Box(Modifier.fillMaxSize().background(Color.Black)) {
        val player = playback.player
        if (player != null) {
            AndroidView(
                modifier = Modifier.fillMaxSize(),
                factory = { ctx ->
                    PlayerView(ctx).apply {
                        useController = false
                        keepScreenOn = true
                        isFocusable = false
                    }
                },
                update = { view -> if (view.player !== player) view.player = player },
            )
        }
        when {
            failure != null -> TvErrorState(
                failure,
                title = stringResource(R.string.live_play_failed),
                video = true,
                onRetry = playback::retry,
                onSignIn = signedOut,
                onChooseHome = { app.session.switchHome() },
                onBack = onBack,
            )
            refusal != null -> Refused(refusal, onRetry = playback::retry, onBack = onBack)
            else -> {
                val focus = remember { FocusRequester() }
                LaunchedEffect(Unit) { runCatching { focus.requestFocus() } }
                Box(
                    Modifier
                        .fillMaxSize()
                        .testTag("live-player")
                        .focusRequester(focus)
                        .onPreviewKeyEvent { e ->
                            if (e.type != KeyEventType.KeyDown) return@onPreviewKeyEvent false
                            val m = mini
                            if (m != null) {
                                when (e.key) {
                                    Key.DirectionUp -> mini = (m - 1).coerceAtLeast(0)
                                    Key.DirectionDown -> mini = (m + 1).coerceAtMost(channels.lastIndex)
                                    Key.DirectionCenter, Key.Enter, Key.NumPadEnter -> {
                                        channels.getOrNull(m)?.let { if (it.channel.id != playback.channelId) playback.tune(it.channel.id) }
                                        mini = null
                                        banner++
                                    }
                                    else -> return@onPreviewKeyEvent false
                                }
                                return@onPreviewKeyEvent true
                            }
                            when (e.key) {
                                Key.DirectionUp, Key.ChannelUp -> tune(1)
                                Key.DirectionDown, Key.ChannelDown -> tune(-1)
                                Key.DirectionCenter, Key.Enter, Key.NumPadEnter ->
                                    if (paused || behind) {
                                        playback.backToLive()
                                        banner++
                                    } else if (channels.isNotEmpty()) {
                                        mini = ids.indexOf(playback.channelId).coerceAtLeast(0)
                                    }
                                Key.MediaPlayPause, Key.Spacebar -> {
                                    playback.player?.let { it.playWhenReady = !it.playWhenReady }
                                    banner++
                                }
                                Key.MediaPlay -> playback.player?.playWhenReady = true
                                Key.MediaPause -> {
                                    playback.player?.playWhenReady = false
                                    banner++
                                }
                                Key.DirectionLeft, Key.DirectionRight -> banner++
                                else -> return@onPreviewKeyEvent false
                            }
                            true
                        }
                        .focusable(),
                ) {
                    val current = channels.firstOrNull { it.channel.id == playback.channelId }
                    if (playback.tuning) {
                        Text(
                            stringResource(if (playback.relay) R.string.live_remote_starting else R.string.live_tuning) +
                                (current?.let { "\n${it.channel.number}  ${it.channel.name}" } ?: ""),
                            style = MaterialTheme.typography.titleLarge,
                            color = MaktabaPalette.Text,
                            modifier = Modifier.align(Alignment.Center),
                        )
                    }
                    val m = mini
                    if (m != null) {
                        MiniGuide(api, channels, m, playback.channelId, serverNow, Modifier.align(Alignment.CenterStart))
                    } else if (showBanner || paused || behind) {
                        if (current != null) {
                            Banner(api, current, serverNow, playback.relay, paused || behind, Modifier.align(Alignment.BottomCenter))
                        }
                    }
                }
            }
        }
    }
}

/** How far behind the live edge counts as paused away from live (the "Back to live" offer). */
private const val BEHIND_LIVE_MS = 30_000L

/** The channel's strip along the bottom: who it is, what is on and how far through, what is next. */
@Composable
private fun Banner(api: TvApi, e: NowEntry, nowMs: Long, relay: Boolean, offerLive: Boolean, modifier: Modifier) {
    val time = rememberTimeFormat()
    val current = e.current?.takeIf { it.isAiring(nowMs) } ?: e.next?.takeIf { it.isAiring(nowMs) } ?: e.current
    val next = if (current === e.next) null else e.next
    ReadingPanel(modifier.fillMaxWidth().padding(Gutter).testTag("live-banner"), contentPadding = 24.dp) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(20.dp)) {
            ChannelBadge(api.live, e.channel, size = 72.dp)
            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                    Text("${e.channel.number}  ${e.channel.name}", style = MaterialTheme.typography.titleMedium, color = MaktabaPalette.Muted)
                    Pill(stringResource(R.string.live_label), MaktabaPalette.Live)
                    if (relay) Pill(stringResource(R.string.live_remote_badge), MaktabaPalette.Gold)
                }
                Text(
                    current?.title?.takeIf { it.isNotBlank() } ?: stringResource(R.string.live_no_content),
                    style = MaterialTheme.typography.headlineSmall,
                    color = MaktabaPalette.Text,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
                current?.progressAt(nowMs)?.let { LiveProgress(it) }
                if (next != null) {
                    Text(
                        stringResource(R.string.live_next, next.startMs?.let(time)?.let { "$it  ${next.title}" } ?: next.title),
                        style = MaterialTheme.typography.labelLarge,
                        color = MaktabaPalette.Faint,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                }
                Text(stringResource(R.string.live_player_hint), style = MaterialTheme.typography.labelMedium, color = MaktabaPalette.Faint)
            }
            if (offerLive) {
                // Select presses it (the player's key handler): it is drawn as the focused button
                Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Text(stringResource(R.string.live_paused), style = MaterialTheme.typography.labelLarge, color = MaktabaPalette.Muted)
                    Box(
                        Modifier.background(MaktabaPalette.Gold, RoundedCornerShape(50)).padding(horizontal = 20.dp, vertical = 10.dp).testTag("back-to-live"),
                    ) {
                        Text(stringResource(R.string.live_back_to_live), style = MaterialTheme.typography.labelLarge, color = MaktabaPalette.OnGold)
                    }
                }
            }
        }
    }
}

@Composable
private fun Pill(text: String, color: Color) {
    Box(Modifier.background(color.copy(alpha = 0.2f), RoundedCornerShape(50)).padding(horizontal = 10.dp, vertical = 2.dp)) {
        Text(text, style = MaterialTheme.typography.labelMedium, color = color)
    }
}

/** What is on every channel now, down the start side; the highlighted one tunes on Select. */
@Composable
private fun MiniGuide(api: TvApi, channels: List<NowEntry>, selected: Int, playing: String?, nowMs: Long, modifier: Modifier) {
    val list = rememberLazyListState()
    LaunchedEffect(selected) { list.animateScrollToItem((selected - 2).coerceAtLeast(0)) }
    ReadingPanel(modifier.fillMaxHeight().width(520.dp).padding(vertical = Gutter).padding(start = Gutter).testTag("mini-guide"), contentPadding = 16.dp) {
        Text(stringResource(R.string.live_now), style = MaterialTheme.typography.titleLarge, color = MaktabaPalette.Gold, modifier = Modifier.padding(8.dp))
        LazyColumn(state = list, userScrollEnabled = false, verticalArrangement = Arrangement.spacedBy(6.dp)) {
            itemsIndexed(channels, key = { _, e -> e.channel.id }) { i, e ->
                val on = i == selected
                val current = e.current?.takeIf { it.isAiring(nowMs) } ?: e.next?.takeIf { it.isAiring(nowMs) } ?: e.current
                Row(
                    Modifier
                        .fillMaxWidth()
                        .background(if (on) MaktabaPalette.Gold else Color.Transparent, RoundedCornerShape(10.dp))
                        .padding(10.dp),
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(12.dp),
                ) {
                    ChannelBadge(api.live, e.channel, size = 44.dp)
                    Column(Modifier.weight(1f)) {
                        Text(
                            "${e.channel.number}  ${e.channel.name}" + if (e.channel.id == playing) "  ●" else "",
                            style = MaterialTheme.typography.labelLarge,
                            color = if (on) MaktabaPalette.OnGold else MaktabaPalette.Muted,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                        )
                        Text(
                            current?.title?.takeIf { it.isNotBlank() } ?: stringResource(R.string.live_no_content),
                            style = MaterialTheme.typography.titleSmall,
                            color = if (on) MaktabaPalette.OnGold else MaktabaPalette.Text,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                        )
                    }
                }
            }
        }
    }
}

/** A remote session the home refused (or ended), in its own words, with Try again and Back. */
@Composable
private fun Refused(words: Int, onRetry: () -> Unit, onBack: () -> Unit) {
    val first = remember { FocusRequester() }
    LaunchedEffect(words) { runCatching { first.requestFocus() } }
    Centered {
        Column(Modifier.widthIn(max = 720.dp).testTag("live-refused"), horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(16.dp)) {
            Text(stringResource(R.string.live_play_failed), style = MaterialTheme.typography.headlineSmall, color = MaktabaPalette.Text)
            Text(stringResource(words), style = MaterialTheme.typography.titleMedium, color = MaktabaPalette.Muted)
            Row(Modifier.padding(top = 8.dp), horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                MaktabaButton(onClick = onRetry, modifier = Modifier.focusRequester(first)) { Text(stringResource(R.string.try_again)) }
                MaktabaButton(onClick = onBack) { Text(stringResource(com.hamzalabs.maktaba.core.R.string.error_back)) }
            }
        }
    }
}
