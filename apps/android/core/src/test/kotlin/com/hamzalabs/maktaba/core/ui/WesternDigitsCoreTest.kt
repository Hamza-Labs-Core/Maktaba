package com.hamzalabs.maktaba.core.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Test
import java.util.Locale

class WesternDigitsCoreTest {
    private val easternArabic = Regex("[٠-٩]")

    @Test
    fun formatDurationAndLocalWhenUseWesternDigitsUnderArabic() {
        val original = Locale.getDefault()
        val originalFmt = Locale.getDefault(Locale.Category.FORMAT)
        try {
            for (tag in listOf("ar", "ar-EG", "ar-SA", "ar-EG-u-nu-arab")) {
                Locale.setDefault(Locale.forLanguageTag(tag))
                assertEquals("6:20", formatDuration(380.0))
                assertEquals("14:48", formatDuration(888.0))
                assertEquals("1:02:03", formatDuration(3723.0))

                val (date, time) = localWhen(1_793_999_100_000L)
                assertFalse("unexpected Eastern Arabic digits in date: $date", easternArabic.containsMatchIn(date))
                assertFalse("unexpected Eastern Arabic digits in time: $time", easternArabic.containsMatchIn(time))
            }
        } finally {
            Locale.setDefault(original)
            Locale.setDefault(Locale.Category.FORMAT, originalFmt)
        }
    }
}
