package com.hamzalabs.maktaba.core.ui

import android.content.Context
import android.content.ContextWrapper
import android.content.res.Configuration
import android.content.res.Resources
import android.os.LocaleList
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.SideEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalResources
import com.hamzalabs.maktaba.core.i18n.applyWesternDigitsDefaultLocale
import com.hamzalabs.maktaba.core.i18n.withWesternDigits

/**
 * Ensures Arabic locales in the active Compose [Configuration], [Resources], and process default
 * [java.util.Locale] carry `-u-nu-latn` so `stringResource(..., int)`, `pluralStringResource`,
 * date/number formatters, and Media3 controls render Western digits (0–9) in Arabic (#1199).
 */
@Composable
fun WesternDigitsScope(content: @Composable () -> Unit) {
    val base = LocalContext.current
    val baseConfig = LocalConfiguration.current
    SideEffect { applyWesternDigitsDefaultLocale() }
    val needsWrap = remember(baseConfig) {
        val locales = baseConfig.locales
        (0 until locales.size()).any { i ->
            val l = locales[i]
            l.language == "ar" && l.getUnicodeLocaleType("nu") != "latn"
        }
    }
    if (!needsWrap) {
        content()
        return
    }
    val config = remember(baseConfig) {
        val locales = baseConfig.locales
        val updated = Array(locales.size()) { i -> locales[i].withWesternDigits() }
        Configuration(baseConfig).apply {
            setLocales(LocaleList(*updated))
            if (updated.isNotEmpty()) setLayoutDirection(updated[0])
        }
    }
    val context = remember(base, config) { WesternDigitsContext(base, config) }
    CompositionLocalProvider(
        LocalContext provides context,
        LocalConfiguration provides config,
        LocalResources provides context.resources,
    ) {
        content()
    }
}

private class WesternDigitsContext(base: Context, config: Configuration) : ContextWrapper(base) {
    private val localized: Resources = base.createConfigurationContext(config).resources
    override fun getResources(): Resources = localized
}
