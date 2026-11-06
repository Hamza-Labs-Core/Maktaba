package com.hamzalabs.maktaba.desktop.watch

import androidx.compose.ui.input.key.Key

/** What a key does while a video plays (story 55.08's table). */
sealed interface WatchAction {
    data object Toggle : WatchAction
    data class Skip(val ms: Long) : WatchAction
    data object NextEpisode : WatchAction
    data object Subtitles : WatchAction
    data object Mute : WatchAction
    data class Volume(val delta: Float) : WatchAction
    data object FullScreen : WatchAction
    data object Escape : WatchAction
}

/**
 * The key map: Space and the media key play and pause; ←/→ 10 s and Shift+←/→ 30 s, mirrored in
 * Arabic (→ goes back); N next episode; S subtitles; M mute; ↑/↓ volume; F full screen (F11
 * stays big screen); Escape leaves full screen, else closes.
 */
fun watchAction(key: Key, shift: Boolean, rtl: Boolean): WatchAction? {
    val step = if (shift) 30_000L else 10_000L
    return when (key) {
        Key.Spacebar, Key.MediaPlayPause -> WatchAction.Toggle
        Key.DirectionLeft -> WatchAction.Skip(if (rtl) step else -step)
        Key.DirectionRight -> WatchAction.Skip(if (rtl) -step else step)
        Key.N -> WatchAction.NextEpisode
        Key.S -> WatchAction.Subtitles
        Key.M -> WatchAction.Mute
        Key.DirectionUp -> WatchAction.Volume(0.1f)
        Key.DirectionDown -> WatchAction.Volume(-0.1f)
        Key.F -> WatchAction.FullScreen
        Key.Escape, Key.Back -> WatchAction.Escape
        else -> null
    }
}
