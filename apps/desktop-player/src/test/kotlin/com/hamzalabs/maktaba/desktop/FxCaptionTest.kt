package com.hamzalabs.maktaba.desktop

import com.hamzalabs.maktaba.core.playback.SubtitleSize
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Story 13.20 with JavaFX's captions: the subtitle size scales the font, which wraps within its
 * share of the picture; a laid-out text drawn [SubtitleSize.Large] times as wide would be cut.
 */
class FxCaptionTest {
    @Test
    fun theFontTakesTheSizeAndTheLinesStayWithinThePicture() {
        assertEquals(720.0 / 22, fxCaptionFont(720.0, 1f), 1e-6)
        for (size in SubtitleSize.entries) {
            assertEquals(size.name, 720.0 / 22 * size.scale, fxCaptionFont(720.0, size.scale), 1e-6)
        }
        // the smallest and the tallest picture's font are scaled too
        assertEquals(14.0 * SubtitleSize.Large.scale, fxCaptionFont(100.0, SubtitleSize.Large.scale), 1e-6)
        assertEquals(48.0 * SubtitleSize.Large.scale, fxCaptionFont(4000.0, SubtitleSize.Large.scale), 1e-6)
        // what a line is wrapped at is what it is drawn at: within the picture at every size
        assertTrue(FX_CAPTION_WIDTH < 1.0)
        assertTrue("drawn scaled, a Large line is wider than the picture", FX_CAPTION_WIDTH * SubtitleSize.Large.scale > 1.0)
    }
}
