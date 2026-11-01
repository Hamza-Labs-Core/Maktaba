package com.hamzalabs.maktaba.player.media

import android.app.Activity
import android.app.PendingIntent
import android.app.PictureInPictureParams
import android.app.RemoteAction
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.content.pm.PackageManager
import android.graphics.Rect
import android.graphics.drawable.Icon
import android.os.Build
import android.util.Rational
import androidx.activity.ComponentActivity
import androidx.activity.compose.LocalActivity
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.core.app.PictureInPictureModeChangedInfo
import androidx.core.content.ContextCompat
import androidx.core.util.Consumer
import androidx.lifecycle.Lifecycle
import androidx.media3.common.Player
import com.hamzalabs.maktaba.player.R

/** The buttons of the picture-in-picture window (FR-37), in their order on screen. */
enum class PipAction(val code: Int) { Back(1), Play(2), Pause(3), Forward(4) }

/** Back 10 s, Play or Pause (what a tap would do now), Forward 10 s. */
fun pipActions(playing: Boolean): List<PipAction> =
    listOf(PipAction.Back, if (playing) PipAction.Pause else PipAction.Play, PipAction.Forward)

/** The window's shape: the video's, within what Android allows (2.39:1 either way); 16:9 until known. */
fun pipAspect(width: Int, height: Int): Rational {
    if (width <= 0 || height <= 0) return Rational(16, 9)
    val max = 2.39f
    val r = width.toFloat() / height
    return when {
        r > max -> Rational(239, 100)
        r < 1 / max -> Rational(100, 239)
        else -> Rational(width, height)
    }
}

/** Does what a picture-in-picture button asks of [player]. */
fun Player.perform(action: PipAction) {
    when (action) {
        PipAction.Back -> seekTo((currentPosition - SEEK_INCREMENT_MS).coerceAtLeast(0))
        PipAction.Forward -> {
            val d = duration
            val to = currentPosition + SEEK_INCREMENT_MS
            seekTo(if (d > 0) to.coerceAtMost(d) else to)
        }
        PipAction.Play -> {
            if (playbackState == Player.STATE_ENDED) seekTo(0)
            play()
        }
        PipAction.Pause -> pause()
    }
}

/** Where the activity's picture-in-picture settings go: the activity's own; a test keeps them. */
val LocalPipParams = staticCompositionLocalOf<(Activity, PictureInPictureParams) -> Unit> {
    { activity, params -> runCatching { activity.setPictureInPictureParams(params) } }
}

private const val ACTION_PIP = "com.hamzalabs.maktaba.player.PIP"
private const val EXTRA_ACTION = "action"

/**
 * Picture in picture for the playing video (FR-37): going Home while it plays shrinks it into a
 * window (Android 12+ by itself; before, on the leave hint), with Back 10 s, Play / Pause and
 * Forward 10 s. Off while [enabled] is false (nothing plays here: casting, an error).
 *
 * Returns true while the activity is in the window: the screen then shows the video alone.
 * [onDismissed] runs when the person closes the window (the video should stop playing then).
 */
@Composable
fun pictureInPicture(
    player: Player?,
    playing: Boolean,
    videoWidth: Int,
    videoHeight: Int,
    sourceRect: Rect?,
    enabled: Boolean,
    onDismissed: () -> Unit,
): Boolean {
    val activity = LocalActivity.current as? ComponentActivity ?: return false
    if (!activity.packageManager.hasSystemFeature(PackageManager.FEATURE_PICTURE_IN_PICTURE)) return false
    val apply = LocalPipParams.current
    val currentPlayer by rememberUpdatedState(player)
    val dismissed by rememberUpdatedState(onDismissed)
    var inPip by remember { mutableStateOf(activity.isInPictureInPictureMode) }

    // the window's buttons
    DisposableEffect(activity) {
        val receiver = object : BroadcastReceiver() {
            override fun onReceive(context: Context, intent: Intent) {
                val code = intent.getIntExtra(EXTRA_ACTION, 0)
                val action = PipAction.entries.firstOrNull { it.code == code } ?: return
                currentPlayer?.perform(action)
            }
        }
        ContextCompat.registerReceiver(activity, receiver, IntentFilter(ACTION_PIP), ContextCompat.RECEIVER_NOT_EXPORTED)
        onDispose { activity.unregisterReceiver(receiver) }
    }
    // in the window or not; closed by the person (the activity stops as the window goes)
    DisposableEffect(activity) {
        val listener = Consumer<PictureInPictureModeChangedInfo> { info ->
            inPip = info.isInPictureInPictureMode
            if (!info.isInPictureInPictureMode && !activity.lifecycle.currentState.isAtLeast(Lifecycle.State.STARTED)) dismissed()
        }
        activity.addOnPictureInPictureModeChangedListener(listener)
        onDispose { activity.removeOnPictureInPictureModeChangedListener(listener) }
    }

    val auto = enabled && playing
    val params = remember(auto, playing, videoWidth, videoHeight, sourceRect) {
        pipParams(activity, auto, pipActions(playing), pipAspect(videoWidth, videoHeight), sourceRect)
    }
    DisposableEffect(params) {
        apply(activity, params)
        onDispose { }
    }
    // left the screen: no window on Home any more
    DisposableEffect(activity) {
        onDispose { apply(activity, pipParams(activity, false, emptyList(), Rational(16, 9))) }
    }
    // before Android 12 the app asks for the window itself, as the person leaves
    if (Build.VERSION.SDK_INT < Build.VERSION_CODES.S) {
        val latest by rememberUpdatedState(params to auto)
        DisposableEffect(activity) {
            val leaving = Runnable {
                val (p, on) = latest
                if (on) runCatching { activity.enterPictureInPictureMode(p) }
            }
            activity.addOnUserLeaveHintListener(leaving)
            onDispose { activity.removeOnUserLeaveHintListener(leaving) }
        }
    }
    return inPip
}

/**
 * The window's settings: its shape, its buttons, where the video is on screen, and whether Home
 * opens it by itself (12+).
 */
fun pipParams(context: Context, autoEnter: Boolean, actions: List<PipAction>, aspect: Rational, sourceRect: Rect? = null): PictureInPictureParams {
    val b = PictureInPictureParams.Builder()
        .setAspectRatio(aspect)
        .setActions(actions.map { it.remote(context) })
    // the video's place on screen: the window grows out of it, and shrinks back into it
    if (sourceRect != null && !sourceRect.isEmpty) b.setSourceRectHint(sourceRect)
    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
        b.setAutoEnterEnabled(autoEnter)
        b.setSeamlessResizeEnabled(true)
    }
    return b.build()
}

private fun PipAction.remote(context: Context): RemoteAction {
    val (icon, label) = when (this) {
        PipAction.Back -> android.R.drawable.ic_media_rew to R.string.watch_back_10
        PipAction.Play -> android.R.drawable.ic_media_play to R.string.watch_play
        PipAction.Pause -> android.R.drawable.ic_media_pause to R.string.watch_pause
        PipAction.Forward -> android.R.drawable.ic_media_ff to R.string.watch_forward_10
    }
    val intent = Intent(ACTION_PIP).setPackage(context.packageName).putExtra(EXTRA_ACTION, code)
    val pending = PendingIntent.getBroadcast(context, code, intent, PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
    val text = context.getString(label)
    return RemoteAction(Icon.createWithResource(context, icon), text, text, pending)
}
