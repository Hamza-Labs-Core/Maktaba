package com.hamzalabs.maktaba.player.watch

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.foundation.background
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.ClosedCaption
import androidx.compose.material.icons.filled.Forward10
import androidx.compose.material.icons.filled.HighQuality
import androidx.compose.material.icons.filled.Pause
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.filled.Replay10
import androidx.compose.material.icons.filled.SkipNext
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Slider
import androidx.compose.material3.SliderDefaults
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.LayoutDirection
import androidx.compose.ui.unit.dp
import androidx.media3.common.C
import com.hamzalabs.maktaba.core.R as CoreR
import com.hamzalabs.maktaba.core.ui.InkAndBrass
import com.hamzalabs.maktaba.core.ui.formatDuration
import com.hamzalabs.maktaba.player.Playback
import com.hamzalabs.maktaba.player.R
import kotlinx.coroutines.delay

/** How long the controls stay after the last touch while playing (FR-36). */
const val CONTROLS_HIDE_MS = 4_000L

/** How far ±10 s and a double-tap seek. */
const val SEEK_MS = 10_000L

/**
 * What a double-tap at [x] of a [width]-wide video seeks (FR-36): the side the video starts on
 * goes back [SEEK_MS], the other side forward. The start side is the left, or the right in
 * Arabic (right to left), as the scrubber mirrors.
 */
fun doubleTapSeekMs(x: Float, width: Float, rtl: Boolean): Long {
    val onLeft = x < width / 2
    return if (onLeft != rtl) -SEEK_MS else SEEK_MS
}

/** What the controls show of the stream: where it is and how long, polled while shown. */
class PlayerClock(positionMs: Long = 0, durationMs: Long = 0) {
    var positionMs by mutableStateOf(positionMs)
    var durationMs by mutableStateOf(durationMs)
}

private enum class Sheet { Tracks, Quality }

/**
 * The player's own controls over the video (FR-36, 48.32's part): a tap shows or hides them,
 * and they hide [CONTROLS_HIDE_MS] after the last touch while playing; a double-tap on either
 * side seeks ±10 s (mirrored in Arabic). Close and the title; Subtitles and audio (FR-34) and
 * Quality (FR-35); Play / Pause and ±10 s; the scrubber with the time and length; Next, when
 * there is a next episode (FR-33).
 */
@Composable
fun PlayerControls(
    playback: Playback,
    clock: PlayerClock,
    title: String,
    onClose: () -> Unit,
    hasNext: Boolean,
    onNext: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val player = playback.player ?: return
    val rtl = LocalLayoutDirection.current == LayoutDirection.Rtl
    var visible by remember { mutableStateOf(true) }
    // each touch starts the 4 seconds again
    var touched by remember { mutableIntStateOf(0) }
    var sheet by remember { mutableStateOf<Sheet?>(null) }
    // the side a double-tap just sought, shown for a moment
    var flash by remember { mutableStateOf<Long?>(null) }

    fun seekBy(ms: Long) {
        val d = clock.durationMs
        val to = (player.currentPosition + ms).coerceAtLeast(0).let { if (d > 0) it.coerceAtMost(d) else it }
        player.seekTo(to)
        clock.positionMs = to
    }

    LaunchedEffect(visible, touched, playback.isPlaying, sheet) {
        if (visible && playback.isPlaying && sheet == null) {
            delay(CONTROLS_HIDE_MS)
            visible = false
        }
    }
    LaunchedEffect(flash) {
        if (flash != null) {
            delay(600)
            flash = null
        }
    }

    Box(
        modifier
            .fillMaxSize()
            .testTag("watch-surface")
            .semantics { contentDescription = title }
            .pointerInput(rtl) {
                detectTapGestures(
                    onTap = {
                        if (sheet != null) sheet = null else visible = !visible
                        touched++
                    },
                    onDoubleTap = { at ->
                        val ms = doubleTapSeekMs(at.x, size.width.toFloat(), rtl)
                        seekBy(ms)
                        flash = ms
                        touched++
                    },
                )
            },
    ) {
        flash?.let { ms ->
            Text(
                stringResource(if (ms < 0) R.string.watch_back_10 else R.string.watch_forward_10),
                color = Color.White,
                modifier = Modifier
                    .align(if ((ms < 0) != rtl) Alignment.CenterStart else Alignment.CenterEnd)
                    .padding(32.dp)
                    .background(Color.Black.copy(alpha = 0.5f), CircleShape)
                    .padding(horizontal = 16.dp, vertical = 8.dp)
                    .testTag("seek-flash"),
            )
        }
        AnimatedVisibility(visible, enter = fadeIn(), exit = fadeOut()) {
            Box(Modifier.fillMaxSize().background(Color.Black.copy(alpha = 0.45f)).testTag("controls")) {
                TopRow(
                    title = title,
                    onClose = onClose,
                    onTracks = { sheet = Sheet.Tracks; touched++ },
                    onQuality = { sheet = Sheet.Quality; touched++ },
                    modifier = Modifier.align(Alignment.TopCenter),
                )
                CenterRow(
                    playing = playback.isPlaying,
                    onBack = { seekBy(-SEEK_MS); touched++ },
                    onPlayPause = {
                        if (player.isPlaying) player.pause() else {
                            if (playback.ended) player.seekTo(0)
                            player.play()
                        }
                        touched++
                    },
                    onForward = { seekBy(SEEK_MS); touched++ },
                    modifier = Modifier.align(Alignment.Center),
                )
                BottomRow(
                    clock = clock,
                    onSeek = { player.seekTo(it); clock.positionMs = it; touched++ },
                    hasNext = hasNext,
                    onNext = onNext,
                    modifier = Modifier.align(Alignment.BottomCenter),
                )
            }
        }
        when (sheet) {
            Sheet.Tracks -> Picker(onDismiss = { sheet = null }, modifier = Modifier.align(Alignment.CenterEnd)) {
                TracksList(playback)
            }
            Sheet.Quality -> Picker(onDismiss = { sheet = null }, modifier = Modifier.align(Alignment.CenterEnd)) {
                QualityList(playback, onChosen = { sheet = null })
            }
            null -> Unit
        }
    }
}

@Composable
private fun TopRow(title: String, onClose: () -> Unit, onTracks: () -> Unit, onQuality: () -> Unit, modifier: Modifier) {
    Row(
        modifier.fillMaxWidth().safeDrawingPadding().padding(12.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        IconButton(onClick = onClose, modifier = Modifier.clip(CircleShape).background(Color.Black.copy(alpha = 0.45f))) {
            Icon(Icons.Filled.Close, stringResource(CoreR.string.close), tint = Color.White)
        }
        Text(
            title,
            color = Color.White,
            style = MaterialTheme.typography.titleMedium,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.weight(1f),
        )
        IconButton(onClick = onTracks, modifier = Modifier.testTag("tracks-button")) {
            Icon(Icons.Filled.ClosedCaption, stringResource(R.string.watch_tracks), tint = Color.White)
        }
        IconButton(onClick = onQuality, modifier = Modifier.testTag("quality-button")) {
            Icon(Icons.Filled.HighQuality, stringResource(R.string.watch_quality), tint = Color.White)
        }
    }
}

@Composable
private fun CenterRow(playing: Boolean, onBack: () -> Unit, onPlayPause: () -> Unit, onForward: () -> Unit, modifier: Modifier) {
    Row(modifier, horizontalArrangement = Arrangement.spacedBy(32.dp), verticalAlignment = Alignment.CenterVertically) {
        // the arrows mirror in Arabic with the row (back is on the start side)
        IconButton(onClick = onBack, modifier = Modifier.size(56.dp)) {
            Icon(Icons.Filled.Replay10, stringResource(R.string.watch_back_10), tint = Color.White, modifier = Modifier.size(36.dp))
        }
        IconButton(
            onClick = onPlayPause,
            modifier = Modifier.size(72.dp).clip(CircleShape).background(Color.Black.copy(alpha = 0.45f)).testTag("play-pause"),
        ) {
            Icon(
                if (playing) Icons.Filled.Pause else Icons.Filled.PlayArrow,
                stringResource(if (playing) R.string.watch_pause else R.string.watch_play),
                tint = Color.White,
                modifier = Modifier.size(48.dp),
            )
        }
        IconButton(onClick = onForward, modifier = Modifier.size(56.dp)) {
            Icon(Icons.Filled.Forward10, stringResource(R.string.watch_forward_10), tint = Color.White, modifier = Modifier.size(36.dp))
        }
    }
}

@Composable
private fun BottomRow(clock: PlayerClock, onSeek: (Long) -> Unit, hasNext: Boolean, onNext: () -> Unit, modifier: Modifier) {
    // while dragging, the thumb follows the finger; the player seeks on release
    var dragging by remember { mutableStateOf(false) }
    var dragAt by remember { mutableFloatStateOf(0f) }
    val d = clock.durationMs.coerceAtLeast(0)
    val at = if (dragging) dragAt else clock.positionMs.coerceIn(0, d.coerceAtLeast(0)).toFloat()
    val positionLabel = stringResource(R.string.watch_position)
    Column(modifier.fillMaxWidth().safeDrawingPadding().padding(horizontal = 16.dp, vertical = 8.dp)) {
        Slider(
            value = at,
            onValueChange = { dragging = true; dragAt = it },
            onValueChangeFinished = { dragging = false; onSeek(dragAt.toLong()) },
            valueRange = 0f..d.coerceAtLeast(1).toFloat(),
            enabled = d > 0,
            colors = SliderDefaults.colors(thumbColor = InkAndBrass.Brass, activeTrackColor = InkAndBrass.Brass),
            modifier = Modifier.fillMaxWidth().testTag("scrubber").semantics { contentDescription = positionLabel },
        )
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                formatDuration(at / 1000.0) + " / " + formatDuration(d / 1000.0),
                color = Color.White,
                style = MaterialTheme.typography.labelLarge,
            )
            Spacer(Modifier.weight(1f))
            if (hasNext) {
                IconButton(onClick = onNext, modifier = Modifier.testTag("next")) {
                    Icon(Icons.Filled.SkipNext, stringResource(R.string.watch_next), tint = Color.White)
                }
            }
        }
    }
}

@Composable
private fun Picker(onDismiss: () -> Unit, modifier: Modifier, content: @Composable () -> Unit) {
    Surface(
        modifier = modifier
            .fillMaxHeight()
            .widthIn(max = 320.dp)
            .width(320.dp)
            .safeDrawingPadding()
            .padding(8.dp)
            .testTag("picker"),
        color = Color(0xEE111111),
        contentColor = Color.White,
        shape = MaterialTheme.shapes.large,
    ) {
        Column(Modifier.verticalScroll(rememberScrollState()).padding(vertical = 8.dp)) {
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
                IconButton(onClick = onDismiss) { Icon(Icons.Filled.Close, stringResource(CoreR.string.close)) }
            }
            content()
        }
    }
}

@Composable
private fun Heading(text: String) {
    Text(text, style = MaterialTheme.typography.titleSmall, color = InkAndBrass.Brass, modifier = Modifier.padding(horizontal = 16.dp, vertical = 8.dp))
}

@Composable
private fun Choice(text: String, selected: Boolean, tag: String, onClick: () -> Unit) {
    Row(
        Modifier
            .fillMaxWidth()
            .selectable(selected = selected, role = Role.RadioButton, onClick = onClick)
            .padding(horizontal = 16.dp, vertical = 12.dp)
            .testTag(tag),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(Modifier.size(24.dp)) { if (selected) Icon(Icons.Filled.Check, null, tint = InkAndBrass.Brass) }
        Text(text, Modifier.padding(start = 12.dp))
    }
}

/** Subtitles (Off and each rendition) and the audio tracks (FR-34). */
@Composable
private fun TracksList(playback: Playback) {
    val locale = LocalConfiguration.current.locales[0]
    val texts = trackOptions(playback.tracks, C.TRACK_TYPE_TEXT)
    val audios = trackOptions(playback.tracks, C.TRACK_TYPE_AUDIO)
    Heading(stringResource(R.string.watch_subtitles))
    Choice(stringResource(R.string.watch_subtitles_off), selected = texts.none { it.selected }, tag = "subtitle-off") { playback.subtitlesOff() }
    texts.forEachIndexed { i, o ->
        val name = o.name(locale) ?: stringResource(R.string.watch_track_n, i + 1)
        Choice(name, o.selected, tag = "subtitle-$i") { playback.select(o) }
    }
    if (audios.isNotEmpty()) {
        Heading(stringResource(R.string.watch_audio))
        audios.forEachIndexed { i, o ->
            val name = o.name(locale) ?: stringResource(R.string.watch_track_n, i + 1)
            Choice(name, o.selected, tag = "audio-$i") { playback.select(o) }
        }
    }
}

/** Auto and the session's ladder (FR-35): a choice reopens the stream at the same position. */
@Composable
private fun QualityList(playback: Playback, onChosen: () -> Unit) {
    Heading(stringResource(R.string.watch_quality))
    QualityOption.of(playback.ladder).forEach { q ->
        val text = q.height?.let { stringResource(R.string.watch_quality_height, it) } ?: stringResource(R.string.watch_quality_auto)
        Choice(text, selected = playback.maxBitrateKbps == q.maxBitrateKbps, tag = "quality-${q.height ?: "auto"}") {
            playback.setQuality(q.maxBitrateKbps)
            onChosen()
        }
    }
}
