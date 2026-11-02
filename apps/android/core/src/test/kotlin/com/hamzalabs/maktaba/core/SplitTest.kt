package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.layout.splitListWidth
import com.hamzalabs.maktaba.core.layout.splitRange
import org.junit.Assert.assertEquals
import org.junit.Test

/** Where a list-detail divider may stand (core/layout/ListDetailSplit.kt), in dp. */
class SplitTest {
    @Test
    fun theDividerKeepsBothMinimums() {
        // a foldable open flat held sideways, beside the collapsed rail: 841 - 96
        assertEquals(240f..425f, splitRange(745f))
        assertEquals(240f..864f, splitRange(1184f))
    }

    @Test
    fun tooNarrowForBothTheyShareInProportion() {
        val r = splitRange(500f)
        assertEquals(r.start, r.endInclusive, 0.01f)
        assertEquals(500f * 240f / 560f, r.start, 0.01f)
        assertEquals(0f..0f, splitRange(0f))
    }

    @Test
    fun withNothingChosenTheListTakesItsPreferredWidthButNeverMoreThanFortyPercent() {
        assertEquals("a tablet: the preferred 380", 380f, splitListWidth(1184f, null), 0.01f)
        assertEquals("a foldable: 40 %", 745f * 0.4f, splitListWidth(745f, null), 0.01f)
        assertEquals("never under the minimum", 240f, splitListWidth(593f, null), 0.01f)
        assertEquals("NaN is nothing chosen", 380f, splitListWidth(1184f, Float.NaN), 0.01f)
    }

    @Test
    fun aChosenShareIsKeptInsideTheRange() {
        assertEquals(372.5f, splitListWidth(745f, 0.5f), 0.01f)
        assertEquals("the page keeps its minimum", 425f, splitListWidth(745f, 0.9f), 0.01f)
        assertEquals("the list keeps its minimum", 240f, splitListWidth(745f, 0.05f), 0.01f)
    }
}
