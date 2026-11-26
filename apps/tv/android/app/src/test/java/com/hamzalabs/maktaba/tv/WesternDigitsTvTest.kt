package com.hamzalabs.maktaba.tv

import com.hamzalabs.maktaba.core.i18n.applyWesternDigitsDefaultLocale
import com.hamzalabs.maktaba.core.i18n.withWesternDigits
import com.hamzalabs.maktaba.tv.ui.screens.clock
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Test
import java.util.Locale

class WesternDigitsTvTest {
    private val easternArabic = Regex("[٠-٩]")

    @Test
    fun clockAndDefaultLocaleFormatWithWesternDigitsUnderArabic() {
        val original = Locale.getDefault()
        val originalFmt = Locale.getDefault(Locale.Category.FORMAT)
        try {
            for (tag in listOf("ar", "ar-EG", "ar-SA", "ar-EG-u-nu-arab")) {
                val loc = Locale.forLanguageTag(tag)
                Locale.setDefault(loc)

                assertEquals("6:20", clock(380.0))
                assertEquals("14:48", clock(888.0))
                assertEquals("1:02:03", clock(3723.0))
                assertEquals("2024", String.format(loc.withWesternDigits(), "%d", 2024))

                applyWesternDigitsDefaultLocale()
                val formatted = String.format("%02d:%02d · %02d:%02d", 6, 20, 14, 48)
                assertEquals("06:20 · 14:48", formatted)
                assertFalse("unexpected Eastern Arabic digits: $formatted", easternArabic.containsMatchIn(formatted))
            }
        } finally {
            Locale.setDefault(original)
            Locale.setDefault(Locale.Category.FORMAT, originalFmt)
        }
    }
}
