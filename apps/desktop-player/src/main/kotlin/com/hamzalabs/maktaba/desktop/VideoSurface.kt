package com.hamzalabs.maktaba.desktop

import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.awt.SwingPanel
import androidx.compose.ui.graphics.Color
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
            root.heightProperty().addListener { _, _, h -> t.font = Font.font((h.toDouble() / 22).coerceIn(14.0, 48.0)) }
            t.wrappingWidthProperty().bind(root.widthProperty().multiply(0.85))
            t.translateYProperty().bind(root.heightProperty().multiply(-0.06))
            scene = Scene(root, javafx.scene.paint.Color.BLACK)
            view = v
            text = t
        }
    }

    fun show(player: MediaPlayer?) = Platform.runLater { view?.mediaPlayer = player }

    /** The caption shown now (null: none), laid out right to left when [rtl]. */
    fun caption(s: String?, rtl: Boolean, scale: Float) = Platform.runLater {
        val t = text ?: return@runLater
        t.text = s.orEmpty()
        t.isVisible = !s.isNullOrBlank()
        t.nodeOrientation = if (rtl) NodeOrientation.RIGHT_TO_LEFT else NodeOrientation.LEFT_TO_RIGHT
        t.scaleX = scale.toDouble()
        t.scaleY = scale.toDouble()
    }
}

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
fun VideoSurface(player: MediaPlayer?, modifier: Modifier = Modifier, caption: String? = null, captionScale: Float = 1f) {
    SwingPanel(
        background = Color.Black,
        factory = { VideoPanel() },
        update = {
            it.show(player)
            it.caption(caption, caption?.let(::isRtlText) == true, captionScale)
        },
        modifier = modifier,
    )
}
