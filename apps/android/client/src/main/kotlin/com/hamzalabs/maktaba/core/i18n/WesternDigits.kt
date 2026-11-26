package com.hamzalabs.maktaba.core.i18n

import java.time.format.DateTimeFormatter
import java.time.format.DecimalStyle
import java.util.Locale

/**
 * Returns [this] with Western digits (`-u-nu-latn`) when the language is Arabic,
 * so numbers, durations, and dates use 0–9 consistently across every Maktaba client (#1199).
 */
fun Locale.withWesternDigits(): Locale {
    if (language != "ar") return this
    if (getUnicodeLocaleType("nu") == "latn") return this
    return Locale.Builder()
        .setLocale(this)
        .setUnicodeLocaleKeyword("nu", "latn")
        .build()
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
 * Ensures the JVM / process default locale uses Western digits when its language is Arabic,
 * covering framework widgets such as Media3's `PlayerControlView` (`Locale.getDefault()`).
 */
fun applyWesternDigitsDefaultLocale() {
    val current = Locale.getDefault()
    val fixed = current.withWesternDigits()
    if (fixed != current) Locale.setDefault(fixed)
    val fmt = Locale.getDefault(Locale.Category.FORMAT)
    val fixedFmt = fmt.withWesternDigits()
    if (fixedFmt != fmt) Locale.setDefault(Locale.Category.FORMAT, fixedFmt)
}
