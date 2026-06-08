package com.hamzalabs.maktaba.tv

import com.hamzalabs.maktaba.tv.data.models.RailItem
import org.junit.Assert.assertEquals
import org.junit.Test

class RailItemTest {

    @Test
    fun `progress is zero without position or duration`() {
        assertEquals(0f, RailItem(videoId = "a").progress)
        assertEquals(0f, RailItem(videoId = "a", positionSec = 30.0).progress)
        assertEquals(0f, RailItem(videoId = "a", durationSec = 120.0).progress)
    }

    @Test
    fun `progress is the watched fraction`() {
        val item = RailItem(videoId = "a", positionSec = 30.0, durationSec = 120.0)
        assertEquals(0.25f, item.progress, 0.0001f)
    }

    @Test
    fun `progress is clamped to one`() {
        val item = RailItem(videoId = "a", positionSec = 200.0, durationSec = 120.0)
        assertEquals(1f, item.progress)
    }
}
