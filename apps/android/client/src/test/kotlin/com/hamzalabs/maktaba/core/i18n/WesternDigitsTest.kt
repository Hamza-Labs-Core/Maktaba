package com.hamzalabs.maktaba.core.i18n

import com.hamzalabs.maktaba.core.api.PanelText
import com.hamzalabs.maktaba.core.parental.TimeView
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle
import java.util.Locale

class WesternDigitsTest {
    private val easternArabic = Regex("[٠-٩]")
    private val arabicLocales = listOf(
        Locale.forLanguageTag("ar"),
        Locale.forLanguageTag("ar-EG"),
        Locale.forLanguageTag("ar-SA"),
        Locale.forLanguageTag("ar-SA-u-nu-arab"),
    )

    @Test
    fun arabicLocalesUseWesternDigitsAcrossNumbersDatesAndClocks() {
        val original = Locale.getDefault()
        val originalFmt = Locale.getDefault(Locale.Category.FORMAT)
        try {
            val instant = Instant.parse("2026-11-12T07:00:00Z")
            val now = Instant.parse("2026-11-11T18:00:00Z")
            val zone = ZoneId.of("UTC")

            for (loc in arabicLocales) {
                val fixed = loc.withWesternDigits()
                assertEquals("ar", fixed.language)
                assertEquals("latn", fixed.getUnicodeLocaleType("nu"))

                val year = String.format(fixed, "%d", 2024)
                assertEquals("2024", year)
                assertFalse("unexpected Eastern Arabic digits: $year", easternArabic.containsMatchIn(year))

                val date = DateTimeFormatter.ofLocalizedDate(FormatStyle.MEDIUM)
                    .withWesternArabicLocale(loc)
                    .format(instant.atZone(zone))
                assertTrue("expected Western digits in $date", date.any { it in '0'..'9' })
                assertFalse("unexpected Eastern Arabic digits: $date", easternArabic.containsMatchIn(date))

                val next = TimeView.nextWords(instant, loc, zone, now)
                assertTrue("expected Western digits in $next", next.any { it in '0'..'9' })
                assertFalse("unexpected Eastern Arabic digits: $next", easternArabic.containsMatchIn(next))

                Locale.setDefault(loc)
                assertEquals("6:20", PanelText.clock(380_000.0))
                assertEquals("1:02:03", PanelText.clock(3_723_000.0))
                assertEquals("6:20", TimeView.clock(380_000L))

                applyWesternDigitsDefaultLocale()
                assertEquals("06:20", String.format("%02d:%02d", 6, 20))
                assertEquals("14:48", String.format("%02d:%02d", 14, 48))
            }
        } finally {
            Locale.setDefault(original)
            Locale.setDefault(Locale.Category.FORMAT, originalFmt)
        }
    }

    @Test
    fun englishLocaleIsUnchanged() {
        assertEquals(Locale.US, Locale.US.withWesternDigits())
    }
}
