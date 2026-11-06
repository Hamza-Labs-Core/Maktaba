package com.hamzalabs.maktaba.desktop

import androidx.compose.foundation.background
import androidx.compose.foundation.focusable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.filled.VolumeOff
import androidx.compose.material.icons.automirrored.filled.VolumeUp
import androidx.compose.material.icons.filled.Forward10
import androidx.compose.material.icons.filled.Fullscreen
import androidx.compose.material.icons.filled.FullscreenExit
import androidx.compose.material.icons.filled.Pause
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.filled.Replay10
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Slider
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.isShiftPressed
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.LayoutDirection
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.api.Capabilities
import com.hamzalabs.maktaba.core.api.Chapter
import com.hamzalabs.maktaba.core.api.Feature
import com.hamzalabs.maktaba.desktop.watch.AudioMenu
import com.hamzalabs.maktaba.desktop.watch.QualityMenu
import com.hamzalabs.maktaba.desktop.watch.SubtitlesMenu
import com.hamzalabs.maktaba.desktop.watch.WatchAction
import com.hamzalabs.maktaba.desktop.watch.watchAction

/**
 * The video, its title, and its controls below it (Swing draws the video above anything Compose
 * would put on it; the captions are drawn inside the JavaFX scene). The keys are story 55.08's
 * ([watchAction]); Escape leaves full screen, else closes.
 */
@Composable
fun Watch(
    playback: Playback,
    features: Capabilities,
    homeUrl: String,
    fullScreen: Boolean,
    onFullScreen: (Boolean) -> Unit,
    onClose: () -> Unit,
    posters: Posters? = null,
) {
    val t = LocalTexts.current
    val rtl = LocalLayoutDirection.current == LayoutDirection.Rtl
    val focus = remember { FocusRequester() }
    LaunchedEffect(Unit) { focus.requestFocus() }
    Column(
        Modifier.fillMaxSize().background(Color.Black)
            .focusRequester(focus).focusable()
            .onPreviewKeyEvent { e ->
                if (e.type != KeyEventType.KeyDown) return@onPreviewKeyEvent false
                when (val a = watchAction(e.key, e.isShiftPressed, rtl) ?: return@onPreviewKeyEvent false) {
                    WatchAction.Toggle -> playback.toggle()
                    is WatchAction.Skip -> playback.skip(a.ms)
                    WatchAction.NextEpisode -> playback.playNext()
                    WatchAction.Subtitles -> if (!features.hides(Feature.SUBTITLES)) playback.toggleSubtitles()
                    WatchAction.Mute -> playback.toggleMute()
                    is WatchAction.Volume -> playback.changeVolume(playback.volume + a.delta)
                    WatchAction.FullScreen -> onFullScreen(!fullScreen)
                    WatchAction.Escape -> when {
                        playback.upNext.secondsLeft != null -> playback.upNext.cancel()
                        fullScreen -> onFullScreen(false)
                        else -> onClose()
                    }
                }
                true
            },
    ) {
        Row(Modifier.fillMaxWidth().padding(8.dp), verticalAlignment = Alignment.CenterVertically) {
            IconButton(onClick = onClose) { Icon(Icons.AutoMirrored.Filled.ArrowBack, t["watch.close"], tint = Color.White) }
            Text(
                playback.request?.title ?: "", color = Color.White, maxLines = 1, overflow = TextOverflow.Ellipsis,
                style = MaterialTheme.typography.titleLarge, modifier = Modifier.weight(1f).semantics { heading() },
            )
            if (playback.reopening) Text(t["watch.reopening"], color = Color.White, style = MaterialTheme.typography.labelLarge)
        }
        Box(Modifier.weight(1f).fillMaxWidth(), contentAlignment = Alignment.Center) {
            val problem = playback.problem
            val player = playback.player
            when {
                problem != null -> Problem(t["watch.failed", problem])
                player == null -> Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(12.dp)) {
                    CircularProgressIndicator()
                    Text(t["watch.opening"], color = Color.White)
                }
                else -> VideoSurface(player, Modifier.fillMaxSize(), caption = playback.caption)
            }
        }
        playback.note?.let { Text(t["watch.failed", it], color = MaterialTheme.colorScheme.error, modifier = Modifier.padding(horizontal = 16.dp)) }
        val next = playback.upNext.next
        val left = playback.upNext.secondsLeft
        if (next != null && left != null) {
            UpNextCard(next, left, posters, onPlayNow = { playback.playNext() }, onCancel = { playback.upNext.cancel() })
        }
        ControlsBar(
            playing = playback.playing,
            positionMs = playback.positionMs,
            durationMs = playback.durationMs,
            chapters = if (features.isOn(Feature.CHAPTERS)) playback.chapters else emptyList(),
            volume = playback.volume,
            muted = playback.muted,
            features = features,
            homeUrl = homeUrl,
            fullScreen = fullScreen,
            subtitles = playback.subtitleOptions,
            subtitle = playback.subtitle,
            audioOptions = playback.audioOptions,
            audio = playback.audio,
            qualities = playback.ladder,
            quality = playback.quality,
            on = ControlsActions(
                toggle = playback::toggle,
                skip = playback::skip,
                seek = playback::seekTo,
                volume = playback::changeVolume,
                mute = playback::toggleMute,
                subtitle = playback::chooseSubtitle,
                audio = playback::chooseAudio,
                quality = playback::chooseQuality,
                fullScreen = { onFullScreen(!fullScreen) },
            ),
        )
    }
}

/** What the controls do. */
class ControlsActions(
    val toggle: () -> Unit = {},
    val skip: (Long) -> Unit = {},
    val seek: (Long) -> Unit = {},
    val volume: (Float) -> Unit = {},
    val mute: () -> Unit = {},
    val subtitle: (com.hamzalabs.maktaba.desktop.watch.Rendition?) -> Unit = {},
    val audio: (AudioOption) -> Unit = {},
    val quality: (com.hamzalabs.maktaba.core.playback.QualityOption) -> Unit = {},
    val fullScreen: () -> Unit = {},
)

/**
 * The bar below the picture: play/pause, back and forward 10 s, the seek bar with chapter marks,
 * the times, volume and mute, Subtitles, Audio, Quality and full screen. Subtitles is hidden
 * from a viewer when they are off on the home; an admin sees it with the off note (story 55.02).
 */
@Composable
fun ControlsBar(
    playing: Boolean,
    positionMs: Long,
    durationMs: Long,
    chapters: List<Chapter>,
    volume: Float,
    muted: Boolean,
    features: Capabilities,
    homeUrl: String,
    fullScreen: Boolean,
    subtitles: List<com.hamzalabs.maktaba.desktop.watch.Rendition>,
    subtitle: com.hamzalabs.maktaba.desktop.watch.Rendition?,
    audioOptions: List<AudioOption>,
    audio: AudioOption?,
    qualities: List<com.hamzalabs.maktaba.core.playback.QualityOption>,
    quality: com.hamzalabs.maktaba.core.playback.QualityOption,
    on: ControlsActions,
) {
    val t = LocalTexts.current
    var dragging by remember { mutableStateOf<Float?>(null) }
    Row(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp).testTag("controls"), verticalAlignment = Alignment.CenterVertically) {
        IconButton(onClick = { on.skip(-10_000) }) { Icon(Icons.Filled.Replay10, t["watch.back"], tint = Color.White) }
        IconButton(onClick = on.toggle, modifier = Modifier.testTag("play-pause")) {
            if (playing) Icon(Icons.Filled.Pause, t["watch.pause"], tint = Color.White)
            else Icon(Icons.Filled.PlayArrow, t["watch.play"], tint = Color.White)
        }
        IconButton(onClick = { on.skip(10_000) }) { Icon(Icons.Filled.Forward10, t["watch.forward"], tint = Color.White) }
        Text(formatDuration(positionMs / 1000.0), color = Color.White)
        val total = durationMs.coerceAtLeast(1)
        BoxWithConstraints(Modifier.weight(1f).padding(horizontal = 12.dp)) {
            // a mark at each chapter's start, under the bar
            if (durationMs > 0) for (c in chapters) {
                val f = (c.startSec * 1000 / total).toFloat().coerceIn(0f, 1f)
                Box(Modifier.offset(x = maxWidth * f).padding(top = 30.dp).size(width = 2.dp, height = 8.dp).background(MaterialTheme.colorScheme.primary).testTag("chapter-mark"))
            }
            Slider(
                value = dragging ?: (positionMs.toFloat() / total).coerceIn(0f, 1f),
                onValueChange = { dragging = it },
                onValueChangeFinished = { dragging?.let { on.seek((it * total).toLong()) }; dragging = null },
                enabled = durationMs > 0,
                modifier = Modifier.fillMaxWidth(),
            )
        }
        Text(formatDuration(durationMs / 1000.0), color = Color.White)
        IconButton(onClick = on.mute, modifier = Modifier.testTag("mute")) {
            if (muted || volume == 0f) Icon(Icons.AutoMirrored.Filled.VolumeOff, t["watch.unmute"], tint = Color.White)
            else Icon(Icons.AutoMirrored.Filled.VolumeUp, t["watch.mute"], tint = Color.White)
        }
        Slider(value = if (muted) 0f else volume, onValueChange = on.volume, modifier = Modifier.width(96.dp).testTag("volume"))
        val subtitlesOff = !features.isOn(Feature.SUBTITLES)
        if (!features.hides(Feature.SUBTITLES) && (subtitles.isNotEmpty() || subtitlesOff)) {
            SubtitlesMenu(subtitles, subtitle, on.subtitle, offNote = if (features.explains(Feature.SUBTITLES)) Feature.SUBTITLES else null, homeUrl = homeUrl)
        }
        if (audioOptions.size > 1) AudioMenu(audioOptions, audio, on.audio)
        if (qualities.size > 1) QualityMenu(qualities, quality, on.quality)
        IconButton(onClick = on.fullScreen, modifier = Modifier.testTag("full-screen")) {
            if (fullScreen) Icon(Icons.Filled.FullscreenExit, t["watch.leaveFullScreen"], tint = Color.White)
            else Icon(Icons.Filled.Fullscreen, t["watch.fullScreen"], tint = Color.White)
        }
    }
}

/** Up next below the picture: the next video, the countdown, Play now and Cancel. */
@Composable
fun UpNextCard(next: PlayRequest, secondsLeft: Int, posters: Posters?, onPlayNow: () -> Unit, onCancel: () -> Unit) {
    val t = LocalTexts.current
    Row(
        Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 4.dp)
            .background(MaterialTheme.colorScheme.surfaceContainerHigh, RoundedCornerShape(12.dp))
            .padding(12.dp).testTag("up-next"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        if (posters != null) PosterImage(posters, next.videoId, Modifier.width(128.dp).height(72.dp))
        Column(Modifier.weight(1f)) {
            Text(t["upnext.in", secondsLeft], color = MaterialTheme.colorScheme.primary, style = MaterialTheme.typography.labelLarge)
            Text(next.title, maxLines = 2, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.titleMedium)
        }
        Button(onClick = onPlayNow, modifier = Modifier.testTag("up-next-play")) { Text(t["upnext.playNow"]) }
        TextButton(onClick = onCancel, modifier = Modifier.testTag("up-next-cancel")) { Text(t["upnext.cancel"]) }
    }
}

