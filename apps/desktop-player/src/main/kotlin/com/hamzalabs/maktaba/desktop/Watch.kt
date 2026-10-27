package com.hamzalabs.maktaba.desktop

import androidx.compose.foundation.background
import androidx.compose.foundation.focusable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Forward10
import androidx.compose.material.icons.filled.Pause
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.filled.Replay10
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Slider
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.awt.SwingPanel
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.LayoutDirection
import androidx.compose.ui.unit.dp
import javafx.application.Platform
import javafx.embed.swing.JFXPanel
import javafx.scene.Scene
import javafx.scene.layout.StackPane
import javafx.scene.media.MediaPlayer
import javafx.scene.media.MediaView

/** JavaFX's video surface in Swing: one per watch screen, its player swapped as a stream reopens. */
private class VideoPanel : JFXPanel() {
    private var view: MediaView? = null

    init {
        Platform.runLater {
            val v = MediaView().apply { isPreserveRatio = true }
            val root = StackPane(v).apply { style = "-fx-background-color: black;" }
            v.fitWidthProperty().bind(root.widthProperty())
            v.fitHeightProperty().bind(root.heightProperty())
            scene = Scene(root, javafx.scene.paint.Color.BLACK)
            view = v
        }
    }

    fun show(player: MediaPlayer?) = Platform.runLater { view?.mediaPlayer = player }
}

/**
 * The video, its title and its controls (below it: Swing draws the video above anything
 * Compose would put on it). Space plays and pauses, the arrows skip 10 s (mirrored right to
 * left), Escape closes.
 */
@Composable
fun Watch(playback: Playback, onClose: () -> Unit) {
    val t = LocalTexts.current
    val rtl = LocalLayoutDirection.current == LayoutDirection.Rtl
    val focus = remember { FocusRequester() }
    var dragging by remember { mutableStateOf<Float?>(null) }
    LaunchedEffect(Unit) { focus.requestFocus() }
    Column(
        Modifier.fillMaxSize().background(Color.Black)
            .focusRequester(focus).focusable()
            .onPreviewKeyEvent { e ->
                if (e.type != KeyEventType.KeyDown) return@onPreviewKeyEvent false
                when (e.key) {
                    Key.Spacebar, Key.MediaPlayPause -> playback.toggle()
                    Key.DirectionLeft -> playback.skip(if (rtl) 10_000 else -10_000)
                    Key.DirectionRight -> playback.skip(if (rtl) -10_000 else 10_000)
                    Key.Escape, Key.Back -> onClose()
                    else -> return@onPreviewKeyEvent false
                }
                true
            },
    ) {
        Row(Modifier.fillMaxWidth().padding(8.dp), verticalAlignment = Alignment.CenterVertically) {
            IconButton(onClick = onClose) { Icon(Icons.AutoMirrored.Filled.ArrowBack, t["watch.close"], tint = Color.White) }
            Text(
                playback.video?.displayTitle ?: "", color = Color.White, maxLines = 1, overflow = TextOverflow.Ellipsis,
                style = MaterialTheme.typography.titleLarge, modifier = Modifier.semantics { heading() },
            )
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
                else -> SwingPanel(
                    background = Color.Black,
                    factory = { VideoPanel() },
                    update = { it.show(player) },
                    modifier = Modifier.fillMaxSize(),
                )
            }
        }
        Row(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp), verticalAlignment = Alignment.CenterVertically) {
            IconButton(onClick = { playback.skip(-10_000) }) { Icon(Icons.Filled.Replay10, t["watch.back"], tint = Color.White) }
            IconButton(onClick = { playback.toggle() }) {
                if (playback.playing) Icon(Icons.Filled.Pause, t["watch.pause"], tint = Color.White)
                else Icon(Icons.Filled.PlayArrow, t["watch.play"], tint = Color.White)
            }
            IconButton(onClick = { playback.skip(10_000) }) { Icon(Icons.Filled.Forward10, t["watch.forward"], tint = Color.White) }
            Text(formatDuration(playback.positionMs / 1000.0), color = Color.White)
            val total = playback.durationMs.coerceAtLeast(1)
            Slider(
                value = dragging ?: (playback.positionMs.toFloat() / total).coerceIn(0f, 1f),
                onValueChange = { dragging = it },
                onValueChangeFinished = { dragging?.let { playback.seekTo((it * total).toLong()) }; dragging = null },
                enabled = playback.durationMs > 0,
                modifier = Modifier.weight(1f).padding(horizontal = 12.dp),
            )
            Text(formatDuration(playback.durationMs / 1000.0), color = Color.White)
        }
    }
}
