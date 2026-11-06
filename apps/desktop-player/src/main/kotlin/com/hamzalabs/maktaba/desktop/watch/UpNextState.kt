package com.hamzalabs.maktaba.desktop.watch

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.hamzalabs.maktaba.core.playback.UP_NEXT_SEC
import com.hamzalabs.maktaba.desktop.PlayRequest

/** Seconds before an episode's end that Up next shows when the series has a next episode (story 55.08). */
const val UP_NEXT_LEAD_SEC = 20

/**
 * Up next below the picture (story 55.08): when an episode ends, or [leadSec] before its end
 * when there is a [next], a card counts down [countSec] seconds and then plays it from its
 * start. Cancel (or Escape) keeps the video; N plays the next at once. Driven by [tick] once a
 * second, so a test is the clock.
 */
class UpNextState(private val leadSec: Int = UP_NEXT_LEAD_SEC, private val countSec: Int = UP_NEXT_SEC) {
    /** What plays next (the next episode, the playlist's next video), or null. */
    var next by mutableStateOf<PlayRequest?>(null)

    /** Seconds left on the card shown, or null when no card shows. */
    var secondsLeft by mutableStateOf<Int?>(null)
        private set

    private var cancelled = false

    /** A new video: no card, nothing cancelled. */
    fun reset(next: PlayRequest?) {
        this.next = next
        secondsLeft = null
        cancelled = false
    }

    fun cancel() {
        cancelled = true
        secondsLeft = null
    }

    /**
     * One second passed at [positionMs] of [durationMs] ([ended]: the video is over). With
     * [autoplay] off nothing shows. Returns what to play now, or null.
     */
    fun tick(positionMs: Long, durationMs: Long, ended: Boolean, autoplay: Boolean = true): PlayRequest? {
        val n = next ?: return null
        if (cancelled || !autoplay) return null
        val due = ended || (durationMs > 0 && positionMs > 0 && durationMs - positionMs <= leadSec * 1000L)
        val left = secondsLeft
        if (left == null) {
            if (due) secondsLeft = countSec
            return null
        }
        if (left > 1) {
            secondsLeft = left - 1
            return null
        }
        secondsLeft = null
        cancelled = true
        return n
    }

    /** N or Play now: the next video at once. */
    fun playNow(): PlayRequest? = next?.also {
        secondsLeft = null
        cancelled = true
    }
}
