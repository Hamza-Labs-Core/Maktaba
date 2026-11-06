package com.hamzalabs.maktaba.desktop.watch

import androidx.compose.ui.input.key.Key
import com.hamzalabs.maktaba.desktop.ResumePosition
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/** Story 55.08 T07 (the key map, mirrored in Arabic) and T01 (where a video resumes). */
class WatchKeysTest {
    @Test
    fun theArrowsSkipAndMirrorInArabic() {
        assertEquals(WatchAction.Skip(-10_000), watchAction(Key.DirectionLeft, shift = false, rtl = false))
        assertEquals(WatchAction.Skip(30_000), watchAction(Key.DirectionRight, shift = true, rtl = false))
        assertEquals(WatchAction.Skip(-10_000), watchAction(Key.DirectionRight, shift = false, rtl = true))
        assertEquals(WatchAction.Skip(-30_000), watchAction(Key.DirectionRight, shift = true, rtl = true))
    }

    @Test
    fun theOtherKeys() {
        assertEquals(WatchAction.Toggle, watchAction(Key.Spacebar, false, false))
        assertEquals(WatchAction.Toggle, watchAction(Key.MediaPlayPause, false, false))
        assertEquals(WatchAction.NextEpisode, watchAction(Key.N, false, false))
        assertEquals(WatchAction.Subtitles, watchAction(Key.S, false, false))
        assertEquals(WatchAction.Mute, watchAction(Key.M, false, false))
        assertEquals(WatchAction.Volume(0.1f), watchAction(Key.DirectionUp, false, false))
        assertEquals(WatchAction.FullScreen, watchAction(Key.F, false, false))
        assertEquals(WatchAction.Escape, watchAction(Key.Escape, false, false))
        assertNull("F11 stays big screen", watchAction(Key.F11, false, false))
    }

    @Test
    fun aVideoResumesWhereItWasLeftUnlessFinishedOrNearlyDone() {
        assertEquals(724, ResumePosition.of(724.0, completed = false, durationSec = 3_000.0))
        assertEquals(0, ResumePosition.of(724.0, completed = true, durationSec = 3_000.0))
        assertEquals(0, ResumePosition.of(2_900.0, completed = false, durationSec = 3_000.0))
        assertEquals(724, ResumePosition.of(724.0, completed = false, durationSec = null))
        assertEquals(0, ResumePosition.of(0.0, completed = false, durationSec = 3_000.0))
    }
}
