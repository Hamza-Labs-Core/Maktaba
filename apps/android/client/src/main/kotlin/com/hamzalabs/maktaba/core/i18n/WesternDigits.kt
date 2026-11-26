package com.hamzalabs.maktaba.core.i18n

import java.time.format.DateTimeFormatter
import java.time.format.DecimalStyle
import java.util.IllformedLocaleException
import java.util.Locale

/**
 * Returns [this] with Western digits (`-u-nu-latn`) when the language is Arabic,
 * so numbers, durations, and dates use 0–9 consistently across every Maktaba client (#1199).
 */
fun Locale.withWesternDigits(): Locale {
    if (language != "ar") return this
    if (getUnicodeLocaleType("nu") == "latn") return this
    return try {
        Locale.Builder()
            .setLocale(this)
            .setUnicodeLocaleKeyword("nu", "latn")
            .build()
    } catch (_: IllformedLocaleException) {
        Locale.Builder()
            .setLanguage("ar")
            .apply {
                runCatching { if (script.isNotEmpty()) setScript(script) }
                runCatching { if (country.isNotEmpty()) setRegion(country) }
            }
            .setUnicodeLocaleKeyword("nu", "latn")
            .build()
    }
}

/**
 * Applies [locale] with Western digits (`DecimalStyle.STANDARD` in Arabic) to this formatter.
 */
fun DateTimeFormatter.withWesternArabicLocale(locale: Locale = Locale.getDefault()): DateTimeFormatter {
    val resolved = locale.withWesternDigits()
    val styled = withLocale(resolved)
    return if (resolved.language == "ar") styled.withDecimalStyle(DecimalStyle.STANDARD) else styled
}

/**
 * Ensures the JVM / process default locales use Western digits when their language is Arabic,
 * covering framework widgets such as Media3's `PlayerControlView` (`Locale.getDefault()`)
 * while preserving distinct `DISPLAY` and `FORMAT` categories.
 */
fun applyWesternDigitsDefaultLocale() {
    val default = Locale.getDefault()
    val display = Locale.getDefault(Locale.Category.DISPLAY)
    val format = Locale.getDefault(Locale.Category.FORMAT)
    val fixedDefault = default.withWesternDigits()
    val fixedDisplay = display.withWesternDigits()
    val fixedFormat = format.withWesternDigits()
    if (fixedDefault != default) {
        // Locale.setDefault(Locale) updates default, DISPLAY, and FORMAT together;
        // restore distinct DISPLAY/FORMAT categories immediately after.
        Locale.setDefault(fixedDefault)
        if (fixedDisplay != fixedDefault) Locale.setDefault(Locale.Category.DISPLAY, fixedDisplay)
        if (fixedFormat != fixedDefault) Locale.setDefault(Locale.Category.FORMAT, fixedFormat)
    } else {
        if (fixedDisplay != display) Locale.setDefault(Locale.Category.DISPLAY, fixedDisplay)
        if (fixedFormat != format) Locale.setDefault(Locale.Category.FORMAT, fixedFormat)
    }
}
