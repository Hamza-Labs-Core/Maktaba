package com.hamzalabs.maktaba.desktop

import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.foundation.background
import androidx.compose.ui.awt.SwingPanel
import androidx.compose.ui.graphics.Color
import com.hamzalabs.maktaba.core.playback.SubtitleColour
import javafx.application.Platform
import javafx.embed.swing.JFXPanel
import javafx.geometry.NodeOrientation
import javafx.geometry.Pos
import javafx.scene.Scene
import javafx.scene.effect.DropShadow
import javafx.scene.layout.StackPane
import javafx.scene.media.MediaPlayer
import javafx.scene.media.MediaView
import javafx.scene.text.Font
import javafx.scene.text.Text
import javafx.scene.text.TextAlignment

/**
 * JavaFX's video surface in Swing: one per screen, its player swapped as a stream reopens. A
 * caption is drawn in a JavaFX [Text] over the [MediaView], inside the JavaFX scene: Swing draws
 * the video above anything Compose would put on it (story 55.08).
 */
internal class VideoPanel : JFXPanel() {
    private var view: MediaView? = null
    private var text: Text? = null
    // the subtitle size chosen (on JavaFX's thread, as the text is)
    private var scale = 1f

    init {
        Platform.runLater {
            val v = MediaView().apply { isPreserveRatio = true }
            val t = Text().apply {
                fill = javafx.scene.paint.Color.WHITE
                textAlignment = TextAlignment.CENTER
                effect = DropShadow(4.0, javafx.scene.paint.Color.BLACK)
                isVisible = false
            }
            val root = StackPane(v, t).apply { style = "-fx-background-color: black;" }
            StackPane.setAlignment(t, Pos.BOTTOM_CENTER)
            v.fitWidthProperty().bind(root.widthProperty())
            v.fitHeightProperty().bind(root.heightProperty())
            root.heightProperty().addListener { _, _, h -> t.font = Font.font(fxCaptionFont(h.toDouble(), scale)) }
            t.wrappingWidthProperty().bind(root.widthProperty().multiply(FX_CAPTION_WIDTH))
            t.translateYProperty().bind(root.heightProperty().multiply(-0.06))
            scene = Scene(root, javafx.scene.paint.Color.BLACK)
            view = v
            text = t
        }
    }

    fun show(player: MediaPlayer?) = Platform.runLater { view?.mediaPlayer = player }

    /** The caption shown now (null: none), laid out right to left when [rtl], at [scale] in [colour] (story 13.20). */
    fun caption(s: String?, rtl: Boolean, scale: Float, colour: SubtitleColour = SubtitleColour.White) = Platform.runLater {
        val t = text ?: return@runLater
        if (scale != this.scale) {
            this.scale = scale
            t.font = Font.font(fxCaptionFont(scene.height, scale))
        }
        t.text = s.orEmpty()
        t.isVisible = !s.isNullOrBlank()
        t.nodeOrientation = if (rtl) NodeOrientation.RIGHT_TO_LEFT else NodeOrientation.LEFT_TO_RIGHT
        t.fill = javafx.scene.paint.Color.web(colour.rgb)
    }
}

/** How much of the picture's width a JavaFX caption's lines take: the text is wrapped at it. */
internal const val FX_CAPTION_WIDTH = 0.85

/**
 * A JavaFX caption's font size in a picture [height] high, at [scale] (the subtitle size chosen).
 * The font takes the scale, not the laid-out text, so a Large line still wraps within
 * [FX_CAPTION_WIDTH] of the picture.
 */
internal fun fxCaptionFont(height: Double, scale: Float): Double = (height / 22).coerceIn(14.0, 48.0) * scale

/** Whether [s] reads right to left (Arabic, Hebrew, Urdu…): its first strong letter says. */
fun isRtlText(s: String): Boolean {
    for (c in s) {
        when (Character.getDirectionality(c)) {
            Character.DIRECTIONALITY_RIGHT_TO_LEFT, Character.DIRECTIONALITY_RIGHT_TO_LEFT_ARABIC -> return true
            Character.DIRECTIONALITY_LEFT_TO_RIGHT -> return false
        }
    }
    return false
}

/** [player]'s picture, with [caption] over it (story 55.08); Live TV draws its channel with it too (55.07). */
@Composable
fun VideoSurface(
    player: MediaPlayer?,
    modifier: Modifier = Modifier,
    caption: String? = null,
    captionScale: Float = 1f,
    captionColour: SubtitleColour = SubtitleColour.White,
) {
    SwingPanel(
        background = Color.Black,
        factory = { VideoPanel() },
        update = {
            it.show(player)
            it.caption(caption, caption?.let(::isRtlText) == true, captionScale, captionColour)
        },
        modifier = modifier,
    )
}

/**
 * A watch screen's picture (story 13.18): libmpv's frames, drawn by Compose at the size they
 * were drawn for (mpv letterboxes, and draws the caption itself); JavaFX's panel for an
 * [FxVideo].
 */
@Composable
fun VideoSurface(
    video: Video?,
    modifier: Modifier = Modifier,
    caption: String? = null,
    captionScale: Float = 1f,
    captionColour: SubtitleColour = SubtitleColour.White,
) {
    val mpv = video as? com.hamzalabs.maktaba.desktop.mpv.MpvVideo
    if (mpv == null) {
        if (video == null || video is FxVideo) {
            VideoSurface((video as? FxVideo)?.media, modifier, caption, captionScale, captionColour)
        } else {
            // a player that draws nowhere (the tests'): the black picture, nothing else
            androidx.compose.foundation.layout.Box(modifier.background(Color.Black).testTag("video-surface"))
        }
        return
    }
    androidx.compose.runtime.LaunchedEffect(mpv, caption, captionScale, captionColour) {
        mpv.caption(caption, caption?.let(::isRtlText) == true, captionScale, captionColour)
    }
    val frame = mpv.frame
    androidx.compose.foundation.Canvas(
        modifier
            .background(Color.Black)
            .onSizeChanged { mpv.resize(it.width, it.height) }
            .testTag("video-surface"),
    ) {
        frame?.let { drawImage(it) }
    }
}
