package com.maktaba.tv

import org.junit.Assert.assertEquals
import org.junit.Test

class VideoSummaryTest {
    @Test fun progressHalfwayThrough() {
        val v = VideoSummary("v1", "t", durationSec = 3600.0, positionSec = 1800.0, posterUrl = null)
        assertEquals(0.5, v.progressFraction, 1e-9)
    }

    @Test fun progressClampsToOne() {
        val v = VideoSummary("v1", "t", durationSec = 100.0, positionSec = 200.0, posterUrl = null)
        assertEquals(1.0, v.progressFraction, 1e-9)
    }

    @Test fun progressZeroWhenNullPosition() {
        val v = VideoSummary("v1", "t", durationSec = 100.0, positionSec = null, posterUrl = null)
        assertEquals(0.0, v.progressFraction, 1e-9)
    }

    @Test fun stubLibraryReturnsEmpty() = kotlinx.coroutines.runBlocking {
        val svc = StubLibraryService()
        assertEquals(0, svc.continueWatching().size)
        assertEquals(0, svc.recommendations().size)
        assertEquals(0, svc.search("test").size)
    }
}
