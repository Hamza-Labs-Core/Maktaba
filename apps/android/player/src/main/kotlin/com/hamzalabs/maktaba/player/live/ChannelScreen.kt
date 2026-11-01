package com.hamzalabs.maktaba.player.live

import android.view.KeyEvent as AndroidKeyEvent
import androidx.activity.compose.LocalActivity
import androidx.compose.foundation.background
import androidx.compose.foundation.focusable
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.gestures.detectVerticalDragGestures
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.KeyboardArrowDown
import androidx.compose.material.icons.filled.KeyboardArrowUp
import androidx.compose.material.icons.filled.Pause
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LocalContentColor
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.onKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextDirection
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.core.view.WindowCompat
import androidx.core.view.WindowInsetsCompat
import androidx.core.view.WindowInsetsControllerCompat
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.repeatOnLifecycle
import com.hamzalabs.maktaba.core.api.Failure
import com.hamzalabs.maktaba.core.api.LiveFailure
import com.hamzalabs.maktaba.core.api.NowEntry
import com.hamzalabs.maktaba.core.ui.ErrorState
import com.hamzalabs.maktaba.core.ui.InkAndBrass
import com.hamzalabs.maktaba.player.R
import com.hamzalabs.maktaba.player.shell.LocalPlayerHome
import com.hamzalabs.maktaba.player.shell.PlayerNavigator
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlin.math.abs

/**
 * A channel's live player (FR-23), full screen with the navigation hidden: the channel, what is
 * on and its progress, and the mini-guide (now / next) over the video. A swipe up or ▲ (a
 * keyboard's arrow or channel key) is the next channel, down or ▼ the previous; digits typed on a
 * keyboard tune that number. After a pause, "Back to live" returns to the live edge. Leaving
 * stops it, and a remote session ends on the home.
 */
@Composable
fun ChannelScreen(id: String, nav: PlayerNavigator) {
    val home = LocalPlayerHome.current
    val live = home.liveTv()
    val env = LocalLiveEnv.current
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val player = remember(id) { ChannelPlayer.of(live.api, scope, env.engine(context), env.device) }
    var nowMs by remember { mutableLongStateOf(live.serverNow(env.nowMs())) }

    LaunchedEffect(player) { player.open(id) }
    DisposableEffect(player) {
        home.onPlaying(true)
        onDispose {
            player.leave()
            home.onPlaying(false)
        }
    }
    // the lineup and the mini-guide: what is on now, refreshed once a minute while shown
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    LaunchedEffect(live, lifecycle) {
        lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) {
            while (true) {
                live.loadNow(env.nowMs())
                nowMs = live.serverNow(env.nowMs())
                delay(env.refreshMs)
            }
        }
    }
    ImmersiveWhileShown()

    var controls by remember { mutableStateOf(true) }
    var shown by remember { mutableIntStateOf(0) } // each show restarts the 4 s
    LaunchedEffect(controls, shown, player.engine.paused) {
        if (controls && !player.engine.paused) {
            delay(CONTROLS_MS)
            controls = false
        }
    }
    fun showControls() { controls = true; shown++ }

    var typed by remember { mutableStateOf("") }
    LaunchedEffect(typed) {
        if (typed.isNotEmpty()) {
            delay(TYPED_MS)
            player.tune(typed.toInt(), live.lineup)
            typed = ""
        }
    }

    val focus = remember { FocusRequester() }
    LaunchedEffect(Unit) { runCatching { focus.requestFocus() } }
    val swipe = with(LocalDensity.current) { SWIPE.toPx() }

    Box(
        Modifier
            .fillMaxSize()
            .background(Color.Black)
            .testTag("channel-player")
            .focusRequester(focus)
            .focusable()
            .onKeyEvent { e ->
                if (e.type != KeyEventType.KeyDown) return@onKeyEvent false
                val code = e.nativeKeyEvent.keyCode
                when {
                    code == AndroidKeyEvent.KEYCODE_DPAD_UP || code == AndroidKeyEvent.KEYCODE_CHANNEL_UP -> { player.next(live.lineup); showControls(); true }
                    code == AndroidKeyEvent.KEYCODE_DPAD_DOWN || code == AndroidKeyEvent.KEYCODE_CHANNEL_DOWN -> { player.previous(live.lineup); showControls(); true }
                    code in AndroidKeyEvent.KEYCODE_0..AndroidKeyEvent.KEYCODE_9 -> { typed = (typed + (code - AndroidKeyEvent.KEYCODE_0)).takeLast(4); true }
                    code in AndroidKeyEvent.KEYCODE_NUMPAD_0..AndroidKeyEvent.KEYCODE_NUMPAD_9 -> { typed = (typed + (code - AndroidKeyEvent.KEYCODE_NUMPAD_0)).takeLast(4); true }
                    code == AndroidKeyEvent.KEYCODE_SPACE || code == AndroidKeyEvent.KEYCODE_MEDIA_PLAY_PAUSE -> { player.togglePause(); showControls(); true }
                    else -> false
                }
            }
            .pointerInput(player) { detectTapGestures { if (controls) controls = false else showControls() } }
            .pointerInput(player) {
                var dy = 0f
                detectVerticalDragGestures(
                    onDragStart = { dy = 0f },
                    onDragEnd = {
                        if (abs(dy) >= swipe) {
                            if (dy < 0) player.next(live.lineup) else player.previous(live.lineup)
                            showControls()
                        }
                    },
                ) { _, amount -> dy += amount }
            },
    ) {
        player.engine.Video(Modifier.fillMaxSize())
        val entry = player.channelId?.let(live::entry)
        CompositionLocalProvider(LocalContentColor provides Color.White) {
            val problem = player.problem
            when {
                problem != null -> Problem(problem, onRetry = player::retry, onBack = { nav.back() })
                player.sessionEnded -> Problem(null, onRetry = player::retry, onBack = { nav.back() })
                player.tuning -> Column(
                    Modifier.align(Alignment.Center),
                    horizontalAlignment = Alignment.CenterHorizontally,
                    verticalArrangement = Arrangement.spacedBy(12.dp),
                ) {
                    CircularProgressIndicator(color = InkAndBrass.Brass)
                    Text(stringResource(R.string.live_tuning), modifier = Modifier.testTag("tuning"))
                }
            }
            if (typed.isNotEmpty()) {
                Text(
                    typed,
                    Modifier.align(Alignment.TopEnd).safeDrawingPadding().padding(24.dp).testTag("typed-number"),
                    style = MaterialTheme.typography.displaySmall,
                    fontWeight = FontWeight.Bold,
                )
            }
            if (controls) {
                Controls(
                    entry = entry,
                    nowMs = nowMs,
                    paused = player.engine.paused,
                    onClose = { nav.back() },
                    onPause = { player.togglePause(); showControls() },
                    onNext = { player.next(live.lineup); showControls() },
                    onPrevious = { player.previous(live.lineup); showControls() },
                )
            }
            if (player.engine.behindLive && player.problem == null) {
                Button(
                    onClick = { player.backToLive(); showControls() },
                    modifier = Modifier.align(Alignment.Center).padding(top = 120.dp).testTag("back-to-live"),
                ) { Text(stringResource(R.string.live_back_to_live)) }
            }
        }
    }
}

/** The close button and channel at the top; the mini-guide and the buttons at the bottom. */
@Composable
private fun Controls(
    entry: NowEntry?,
    nowMs: Long,
    paused: Boolean,
    onClose: () -> Unit,
    onPause: () -> Unit,
    onNext: () -> Unit,
    onPrevious: () -> Unit,
) {
    val time = rememberTimeFormat()
    Box(Modifier.fillMaxSize().testTag("live-controls")) {
        Row(
            Modifier.fillMaxWidth().background(Color.Black.copy(alpha = 0.45f)).safeDrawingPadding().padding(12.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            IconButton(onClick = onClose, modifier = Modifier.clip(CircleShape).background(Color.Black.copy(alpha = 0.45f)).testTag("live-close")) {
                Icon(Icons.Filled.Close, stringResource(R.string.live_close), tint = Color.White)
            }
            entry?.channel?.let { c ->
                if (c.number > 0) Text("${c.number}", style = MaterialTheme.typography.titleMedium, color = InkAndBrass.Brass, fontWeight = FontWeight.Bold)
                Text(c.name, style = MaterialTheme.typography.titleMedium, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f, fill = false).testTag("live-channel-name"))
            }
            Text(
                stringResource(R.string.live_badge),
                Modifier.clip(RoundedCornerShape(4.dp)).background(Color(0xFFB6253A)).padding(horizontal = 6.dp, vertical = 2.dp),
                style = MaterialTheme.typography.labelSmall,
                fontWeight = FontWeight.Bold,
            )
        }
        Column(
            Modifier.align(Alignment.BottomCenter).fillMaxWidth().background(Color.Black.copy(alpha = 0.55f)).safeDrawingPadding().padding(16.dp),
            verticalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            // the mini-guide: now, with its progress, and next
            entry?.current?.let { p ->
                Column(Modifier.widthIn(max = 640.dp).testTag("mini-guide"), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                    Text(stringResource(R.string.live_now), style = MaterialTheme.typography.labelMedium, color = InkAndBrass.Brass)
                    Text(p.title, style = MaterialTheme.typography.titleMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    Text(timeRange(p), style = MaterialTheme.typography.bodySmall.copy(textDirection = TextDirection.Ltr), color = Color.White.copy(alpha = 0.75f))
                    p.progressAt(nowMs)?.let { ProgrammeProgress(it.toFloat()) }
                    entry.next?.let { n ->
                        Text(
                            stringResource(R.string.live_next, n.startMs?.let(time) ?: "", n.title),
                            style = MaterialTheme.typography.bodySmall,
                            color = Color.White.copy(alpha = 0.75f),
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                        )
                    }
                }
            }
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.Center, verticalAlignment = Alignment.CenterVertically) {
                IconButton(onClick = onPrevious, modifier = Modifier.testTag("live-previous")) {
                    Icon(Icons.Filled.KeyboardArrowDown, stringResource(R.string.live_previous_channel), tint = Color.White)
                }
                IconButton(onClick = onPause, modifier = Modifier.testTag("live-pause")) {
                    Icon(
                        if (paused) Icons.Filled.PlayArrow else Icons.Filled.Pause,
                        stringResource(if (paused) R.string.live_play else R.string.live_pause),
                        tint = Color.White,
                    )
                }
                IconButton(onClick = onNext, modifier = Modifier.testTag("live-next")) {
                    Icon(Icons.Filled.KeyboardArrowUp, stringResource(R.string.live_next_channel), tint = Color.White)
                }
            }
        }
    }
}

/** Why the channel does not play, with Try again and Back. */
@Composable
private fun Problem(problem: Throwable?, onRetry: () -> Unit, onBack: () -> Unit) {
    val words = when {
        problem == null -> stringResource(R.string.live_session_ended)
        problem is LiveFailure -> liveFailureWords(problem.code)
        else -> null
    }
    if (problem != null && words == null) {
        ErrorState(
            Failure.of(problem),
            title = stringResource(R.string.live_cant_play),
            modifier = Modifier.safeDrawingPadding().padding(top = 48.dp),
            video = true,
            onRetry = onRetry,
            onBack = onBack,
        )
        return
    }
    Column(
        Modifier.fillMaxSize().safeDrawingPadding().padding(24.dp).testTag("live-problem"),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(12.dp, Alignment.CenterVertically),
    ) {
        Text(stringResource(R.string.live_cant_play), style = MaterialTheme.typography.titleMedium)
        Text(words ?: "", style = MaterialTheme.typography.bodyMedium, color = Color.White.copy(alpha = 0.8f))
        Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            Button(onClick = onRetry) { Text(stringResource(R.string.live_try_again)) }
            OutlinedButton(onClick = onBack) { Text(stringResource(R.string.live_close), color = Color.White) }
        }
    }
}

/** The words for a refused tune ([LiveFailure.code]); null when there are none (a generic failure shows instead). */
@Composable
private fun liveFailureWords(code: String): String? = when (code) {
    "remote_unavailable" -> stringResource(R.string.live_remote_unavailable)
    "user_streams_full" -> stringResource(R.string.live_streams_full)
    "budget_exhausted" -> stringResource(R.string.live_budget)
    "remote_timeout" -> stringResource(R.string.live_remote_timeout)
    "remote_not_allowed" -> stringResource(R.string.live_not_allowed)
    "no_such_channel" -> stringResource(R.string.live_no_such_channel)
    else -> null
}

/** Hides the status and navigation bars while shown (a swipe shows them for a moment). */
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

/** The controls hide after this long while playing. */
private const val CONTROLS_MS = 4_000L

/** A typed channel number tunes this long after its last digit. */
private const val TYPED_MS = 1_500L

/** A vertical swipe this long changes the channel. */
private val SWIPE = 64.dp
