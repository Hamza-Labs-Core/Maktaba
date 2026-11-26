package com.hamzalabs.maktaba.player.libraries

import java.util.Locale
import org.junit.Assert.assertEquals
import org.junit.Test

/** A series' year uses Western digits in Arabic and is never grouped. */
class SeriesPageTest {
    @Test fun theYearUsesWesternDigitsInArabic() {
        assertEquals("2024", seriesYear(2024, Locale.US))
        assertEquals("2024", seriesYear(2024, Locale.forLanguageTag("ar-EG")))
        assertEquals("2024", seriesYear(2024, Locale.forLanguageTag("ar-SA")))
    }
}
