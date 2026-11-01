package com.hamzalabs.maktaba.player.profile

import android.app.LocaleManager
import android.content.Context
import android.content.ContextWrapper
import android.content.res.Configuration
import android.content.res.Resources
import android.os.Build
import android.os.LocaleList
import android.view.View
import androidx.annotation.RequiresApi
import androidx.annotation.StringRes
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.unit.LayoutDirection
import androidx.core.text.layoutDirection
import androidx.compose.ui.platform.LocalLayoutDirection
import com.hamzalabs.maktaba.core.session.KeyValueStore
import com.hamzalabs.maktaba.core.session.PreferencesStore
import com.hamzalabs.maktaba.player.R
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import java.util.Locale

/** The app's language (FR-30): the system's, English or Arabic. */
enum class AppLanguage(val tag: String?, @param:StringRes val label: Int) {
    System(null, R.string.profile_language_system),
    English("en", R.string.profile_language_english),
    Arabic("ar", R.string.profile_language_arabic),
    ;

    companion object {
        /** The language a list of BCP 47 tags names first ("ar-EG,en" is Arabic); none is the system's. */
        fun of(tags: String?): AppLanguage {
            val first = tags?.split(',')?.firstOrNull()?.trim()?.takeIf { it.isNotEmpty() } ?: return System
            val language = Locale.forLanguageTag(first).language
            return entries.firstOrNull { it.tag == language } ?: System
        }
    }
}

/** Android 13's per-app languages (`LocaleManager`): the tags the app runs in, empty for the system's. */
interface PerAppLocales {
    fun get(): String
    fun set(tags: String)
}

/** [PerAppLocales] by the framework's [LocaleManager] (Android 13+). */
@RequiresApi(Build.VERSION_CODES.TIRAMISU)
class FrameworkLocales(context: Context) : PerAppLocales {
    private val manager = context.getSystemService(LocaleManager::class.java)
    override fun get(): String = manager.applicationLocales.toLanguageTags()
    override fun set(tags: String) {
        manager.applicationLocales = LocaleList.forLanguageTags(tags)
    }
}

/**
 * The language choice. On Android 13+ it is the system's per-app language
 * (`LocaleManager.setApplicationLocales`; the system lists English and Arabic from the locale
 * config AGP generates, `generateLocaleConfig`), so the app's settings page and this one agree.
 * Older Android has no per-app language: the choice is kept here and [AppLanguageScope] draws
 * the app in it ([applied]).
 */
class AppLanguages(
    private val sdk: Int,
    private val perApp: PerAppLocales?,
    private val store: KeyValueStore,
) {
    /** True when the system keeps the choice (Android 13+): the app's language page opens too. */
    val perAppLanguages: Boolean get() = sdk >= Build.VERSION_CODES.TIRAMISU && perApp != null

    private val _current = MutableStateFlow(read())
    /** What the person chose. */
    val current: StateFlow<AppLanguage> = _current.asStateFlow()

    private val _applied = MutableStateFlow(if (perAppLanguages) AppLanguage.System else _current.value)
    /** What [AppLanguageScope] draws the app in: the in-app choice before Android 13, else the system's. */
    val applied: StateFlow<AppLanguage> = _applied.asStateFlow()

    private fun read(): AppLanguage =
        if (perAppLanguages) AppLanguage.of(perApp!!.get()) else AppLanguage.of(store.get(STORAGE_KEY))

    fun set(language: AppLanguage) {
        _current.value = language
        if (perAppLanguages) {
            // the system recreates the activity in the new language
            perApp!!.set(language.tag.orEmpty())
        } else {
            store.put(STORAGE_KEY, language.tag)
            _applied.value = language
        }
    }

    /** Reads the choice again (the person may have changed it in the system's settings). */
    fun refresh() {
        _current.value = read()
    }

    companion object {
        const val STORAGE_KEY = "language"

        @Volatile private var shared: AppLanguages? = null

        /** The app's one instance, on the device's settings (the same file as the theme). */
        fun of(context: Context): AppLanguages = shared ?: synchronized(this) {
            shared ?: run {
                val app = context.applicationContext
                val perApp = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) FrameworkLocales(app) else null
                AppLanguages(Build.VERSION.SDK_INT, perApp, PreferencesStore(app, "settings")).also { shared = it }
            }
        }
    }
}

/** The languages a screen reads; a test provides its own. */
val LocalAppLanguages = staticCompositionLocalOf<AppLanguages?> { null }

@Composable
fun appLanguages(): AppLanguages {
    LocalAppLanguages.current?.let { return it }
    val context = LocalContext.current
    return remember(context) { AppLanguages.of(context) }
}

/**
 * Draws [content] in the in-app language before Android 13 (FR-30's fallback): its resources,
 * configuration and layout direction (Arabic mirrors). With the system's language, or on
 * Android 13+ where the system applies the choice itself, it draws [content] as it is.
 */
@Composable
fun AppLanguageScope(languages: AppLanguages = appLanguages(), content: @Composable () -> Unit) {
    val language by languages.applied.collectAsState()
    val base = LocalContext.current
    val baseConfig = LocalConfiguration.current
    val baseResources = LocalResources.current
    val baseDirection = LocalLayoutDirection.current
    val locale = remember(language) { language.tag?.let(Locale::forLanguageTag) }
    val config = remember(baseConfig, locale) {
        locale?.let { Configuration(baseConfig).apply { setLocale(it); setLayoutDirection(it) } } ?: baseConfig
    }
    val context = remember(base, config, locale) { if (locale == null) base else LocalizedContext(base, config) }
    val direction = when {
        locale == null -> baseDirection
        locale.layoutDirection == View.LAYOUT_DIRECTION_RTL -> LayoutDirection.Rtl
        else -> LayoutDirection.Ltr
    }
    // always the same provider around [content], so a change of language keeps the app's state
    // (the section shown, its page) rather than composing it afresh
    CompositionLocalProvider(
        LocalContext provides context,
        LocalConfiguration provides config,
        LocalResources provides if (locale == null) baseResources else context.resources,
        LocalLayoutDirection provides direction,
    ) {
        content()
    }
}

/** [base] (the activity, for starting others) with its resources in [config]'s language. */
private class LocalizedContext(base: Context, config: Configuration) : ContextWrapper(base) {
    private val localized: Resources = base.createConfigurationContext(config).resources
    override fun getResources(): Resources = localized
}
