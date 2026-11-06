package com.hamzalabs.maktaba.tv.ui.screens.library

import android.content.Context
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import com.hamzalabs.maktaba.core.queue.QueueItem
import com.hamzalabs.maktaba.tv.R
import com.hamzalabs.maktaba.tv.ui.components.MaktabaButton
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaPalette

/** The TV's Playback setting: play the next video automatically (on by default, story 56.11). */
object PlaybackPrefs {
    private fun prefs(c: Context) = c.getSharedPreferences("playback", Context.MODE_PRIVATE)
    fun autoplay(c: Context): Boolean = prefs(c).getBoolean("autoplay_next", true)
    fun setAutoplay(c: Context, on: Boolean) = prefs(c).edit().putBoolean("autoplay_next", on).apply()
}

/** Seconds the Up next card counts down from after the video ends. */
const val UP_NEXT_SEC = 10
/** How long before the end the card shows. */
const val UP_NEXT_BEFORE_END_SEC = 30

/**
 * Where the countdown stands (a pure rule, story 56.11 T01): null while the card is not shown;
 * the seconds left, or -1 when the card shows with no countdown (autoplay off, or not ended yet).
 */
fun upNextState(remainingSec: Double, endedForSec: Double?, autoplay: Boolean): Int? = when {
    endedForSec != null && autoplay -> (UP_NEXT_SEC - endedForSec.toInt()).coerceAtLeast(0)
    endedForSec != null -> -1
    remainingSec in 0.0..UP_NEXT_BEFORE_END_SEC.toDouble() -> -1
    else -> null
}

/**
 * The Up next card at the bottom end corner (the bottom left in Arabic, as the layout mirrors):
 * the next item's name and series, the countdown, Play now (focused) and Cancel.
 */
@Composable
fun UpNextCard(next: QueueItem, secondsLeft: Int, onPlayNow: () -> Unit, onCancel: () -> Unit, modifier: Modifier = Modifier) {
    val focus = remember { FocusRequester() }
    LaunchedEffect(next.id) { runCatching { focus.requestFocus() } }
    Column(
        modifier.width(420.dp).background(MaktabaPalette.Raised, RoundedCornerShape(16.dp)).padding(20.dp).testTag("up-next"),
        verticalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        Text(
            if (secondsLeft >= 0) stringResource(R.string.player_up_next_in, secondsLeft) else stringResource(R.string.player_up_next),
            style = MaterialTheme.typography.titleMedium, color = MaktabaPalette.Gold,
        )
        Text(next.name, style = MaterialTheme.typography.titleLarge, color = MaktabaPalette.Text, maxLines = 2)
        next.seriesName?.let { Text(it, style = MaterialTheme.typography.bodyMedium, color = MaktabaPalette.Muted) }
        Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            MaktabaButton(onClick = onPlayNow, modifier = Modifier.focusRequester(focus).testTag("up-next-play")) { Text(stringResource(R.string.player_play_now)) }
            MaktabaButton(onClick = onCancel) { Text(stringResource(R.string.common_cancel)) }
        }
    }
}

/** The player's queue bar (Menu or Info on the remote): Previous, Next and, when shuffled, Shuffle again. */
@Composable
fun QueueBar(shuffled: Boolean, onPrevious: () -> Unit, onNext: () -> Unit, onReshuffle: () -> Unit, modifier: Modifier = Modifier) {
    val focus = remember { FocusRequester() }
    LaunchedEffect(Unit) { runCatching { focus.requestFocus() } }
    Row(modifier.background(MaktabaPalette.Raised, RoundedCornerShape(16.dp)).padding(16.dp).testTag("queue-bar"), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
        MaktabaButton(onClick = onPrevious) { Text("⏮ " + stringResource(R.string.player_previous)) }
        MaktabaButton(onClick = onNext, modifier = Modifier.focusRequester(focus)) { Text(stringResource(R.string.player_next) + " ⏭") }
        if (shuffled) MaktabaButton(onClick = onReshuffle) { Text("⤮ " + stringResource(R.string.player_shuffle_again)) }
    }
}
