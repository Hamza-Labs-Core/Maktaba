package com.hamzalabs.maktaba.player.watch

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.Episode
import com.hamzalabs.maktaba.core.api.SeriesEpisodes
import com.hamzalabs.maktaba.core.api.Video
import com.hamzalabs.maktaba.core.api.myVideo
import com.hamzalabs.maktaba.core.api.seriesEpisodes
import com.hamzalabs.maktaba.core.ui.InkAndBrass
import com.hamzalabs.maktaba.player.R
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay

/** Seconds Up next counts down before the next episode plays (FR-33, as the web's). */
const val UP_NEXT_SEC = 10

/** The episode after [videoId] in the series' order (its seasons, then their episodes), or null. */
fun nextEpisode(episodes: SeriesEpisodes, videoId: String): Episode? {
    val all = episodes.seasons.flatMap { it.episodes }
    val at = all.indexOfFirst { it.videoId == videoId }
    return if (at >= 0) all.getOrNull(at + 1) else null
}

/**
 * The video after [videoId] (FR-33): the next episode of its series (`/api/me/videos/{id}`
 * names the series, `/api/series/{id}/episodes` its order), or null when it is in none, is the
 * last, or the home does not answer (Up next is only a nicety). The Android Player keeps no
 * play queue, so the series is the only "next".
 */
suspend fun ApiClient.nextVideo(videoId: String): Video? = try {
    val series = myVideo(videoId).series
    val next = series?.let { nextEpisode(seriesEpisodes(it.id), videoId) }
    next?.let { e ->
        runCatching { video(e.videoId) }.getOrNull()
            ?: Video(e.videoId, title = e.title.takeIf { it.isNotBlank() }, durationSec = e.durationSec)
    }
} catch (e: CancellationException) {
    throw e
} catch (_: Exception) {
    null
}

/** Whether Up next is due: the last [UP_NEXT_SEC] seconds of a video of known length, or its end. */
fun upNextDue(positionMs: Long, durationMs: Long, ended: Boolean): Boolean =
    ended || (durationMs > 0 && positionMs > 0 && durationMs - positionMs <= UP_NEXT_SEC * 1000L)

/**
 * Up next (FR-33): while [due] and Settings' "play the next episode automatically" is on
 * ([autoplay]), a card counts down [UP_NEXT_SEC] seconds and then plays [next]; Play now plays
 * it at once, Cancel hides it for this video. Off in Settings, nothing shows and the video stops
 * at its end (the Next button stays).
 */
@Composable
fun UpNext(next: Video?, due: Boolean, autoplay: Boolean, onPlay: (Video) -> Unit, modifier: Modifier = Modifier) {
    var cancelled by remember(next?.id) { mutableStateOf(false) }
    if (next == null || !autoplay || !due || cancelled) return
    var left by remember(next.id) { mutableIntStateOf(UP_NEXT_SEC) }
    val play by rememberUpdatedState(onPlay)
    LaunchedEffect(next.id) {
        while (left > 0) {
            delay(1_000)
            left--
        }
        play(next)
    }
    UpNextCard(next, left, onPlayNow = { play(next) }, onCancel = { cancelled = true }, modifier)
}

@Composable
fun UpNextCard(next: Video, secondsLeft: Int, onPlayNow: () -> Unit, onCancel: () -> Unit, modifier: Modifier = Modifier) {
    Column(
        modifier
            .testTag("up-next")
            .widthIn(max = 360.dp)
            .background(Color.Black.copy(alpha = 0.8f), RoundedCornerShape(12.dp))
            .padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        Text(stringResource(R.string.watch_up_next_in, secondsLeft), color = InkAndBrass.Brass, style = MaterialTheme.typography.labelLarge)
        Text(next.displayTitle, color = Color.White, style = MaterialTheme.typography.titleMedium, maxLines = 2, overflow = TextOverflow.Ellipsis)
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Button(onClick = onPlayNow) { Text(stringResource(R.string.watch_play_now)) }
            TextButton(onClick = onCancel) { Text(stringResource(R.string.watch_cancel), color = Color.White) }
        }
    }
}
