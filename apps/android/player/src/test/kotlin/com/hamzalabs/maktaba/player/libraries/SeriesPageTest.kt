package com.hamzalabs.maktaba.player.libraries

import java.util.Locale
import org.junit.Assert.assertEquals
import org.junit.Test

/** A series' year is written in the page's digits, as its episode count is, never grouped. */
class SeriesPageTest {
    @Test fun theYearUsesTheLocalesDigits() {
        assertEquals("2024", seriesYear(2024, Locale.US))
        assertEquals("٢٠٢٤", seriesYear(2024, Locale.forLanguageTag("ar-EG")))
    }
}
