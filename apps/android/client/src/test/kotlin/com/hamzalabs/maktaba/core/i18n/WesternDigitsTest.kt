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

    @Test
    fun illformedLegacyArabicVariantDoesNotThrow() {
        @Suppress("DEPRECATION")
        val legacy = Locale("ar", "EG", "bad!")
        val fixed = legacy.withWesternDigits()
        assertEquals("ar", fixed.language)
        assertEquals("EG", fixed.country)
        assertEquals("latn", fixed.getUnicodeLocaleType("nu"))
        assertEquals("2026", String.format(fixed, "%d", 2026))
    }

    @Test
    fun applyWesternDigitsPreservesDistinctDisplayAndFormatCategories() {
        val originalDefault = Locale.getDefault()
        val originalDisplay = Locale.getDefault(Locale.Category.DISPLAY)
        val originalFormat = Locale.getDefault(Locale.Category.FORMAT)
        try {
            // Arabic display + English format: format must stay English
            Locale.setDefault(Locale.forLanguageTag("ar-EG"))
            Locale.setDefault(Locale.Category.DISPLAY, Locale.forLanguageTag("ar-EG"))
            Locale.setDefault(Locale.Category.FORMAT, Locale.US)
            applyWesternDigitsDefaultLocale()
            assertEquals("ar-EG-u-nu-latn", Locale.getDefault().toLanguageTag())
            assertEquals("ar-EG-u-nu-latn", Locale.getDefault(Locale.Category.DISPLAY).toLanguageTag())
            assertEquals(Locale.US, Locale.getDefault(Locale.Category.FORMAT))

            // English display + Arabic format: display must stay English, format gains latn
            Locale.setDefault(Locale.US)
            Locale.setDefault(Locale.Category.DISPLAY, Locale.US)
            Locale.setDefault(Locale.Category.FORMAT, Locale.forLanguageTag("ar-SA"))
            applyWesternDigitsDefaultLocale()
            assertEquals(Locale.US, Locale.getDefault())
            assertEquals(Locale.US, Locale.getDefault(Locale.Category.DISPLAY))
            assertEquals("ar-SA-u-nu-latn", Locale.getDefault(Locale.Category.FORMAT).toLanguageTag())

            // Distinct Arabic regions in DISPLAY and FORMAT: each keeps its own region
            Locale.setDefault(Locale.forLanguageTag("ar-SA"))
            Locale.setDefault(Locale.Category.DISPLAY, Locale.forLanguageTag("ar-EG"))
            Locale.setDefault(Locale.Category.FORMAT, Locale.forLanguageTag("ar-AE"))
            applyWesternDigitsDefaultLocale()
            assertEquals("ar-SA-u-nu-latn", Locale.getDefault().toLanguageTag())
            assertEquals("ar-EG-u-nu-latn", Locale.getDefault(Locale.Category.DISPLAY).toLanguageTag())
            assertEquals("ar-AE-u-nu-latn", Locale.getDefault(Locale.Category.FORMAT).toLanguageTag())
        } finally {
            Locale.setDefault(originalDefault)
            Locale.setDefault(Locale.Category.DISPLAY, originalDisplay)
            Locale.setDefault(Locale.Category.FORMAT, originalFormat)
        }
    }
}
