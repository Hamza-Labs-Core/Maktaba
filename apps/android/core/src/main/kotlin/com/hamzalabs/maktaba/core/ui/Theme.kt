package com.hamzalabs.maktaba.core.ui

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.ColorScheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Typography
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.Immutable
import androidx.compose.runtime.ReadOnlyComposable
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.lerp
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.Font
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import com.hamzalabs.maktaba.core.R
import com.hamzalabs.maktaba.core.session.KeyValueStore
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

/** The theme a person picks, as on the web (`web/src/lib/theme.ts`): light, dark, or the system's. */
enum class ThemeMode(@androidx.annotation.StringRes val label: Int) {
    System(R.string.theme_system), Light(R.string.theme_light), Dark(R.string.theme_dark);

    companion object {
        const val STORAGE_KEY = "theme"
        /** The kept choice; with none (or one this version does not know), dark (FR-2). */
        fun parse(s: String?): ThemeMode = entries.firstOrNull { it.name.equals(s, ignoreCase = true) } ?: Dark
    }
}

/** The picked theme, kept per device. */
class ThemeStore(private val store: KeyValueStore) {
    private val _mode = MutableStateFlow(ThemeMode.parse(store.get(ThemeMode.STORAGE_KEY)))
    val mode: StateFlow<ThemeMode> = _mode.asStateFlow()

    fun set(mode: ThemeMode) {
        _mode.value = mode
        store.put(ThemeMode.STORAGE_KEY, mode.name.lowercase())
    }
}

/**
 * "Ink & Brass" as Material's colour schemes, every colour from the generated design tokens
 * ([MaktabaTokens], `web/design-system/tokens/`): a change to a token reaches the apps through
 * `make build-tokens`, never by editing a colour here. A role Material has and the tokens do not
 * name is the nearest token, or a mix of two ([outline]).
 */
object InkAndBrass {
    /** --color-accent: the brand's gold, a fill colour. */
    val Brass = MaktabaTokens.Dark.accent
    /** --color-accent-text on light: the gold dark enough for text (AA). */
    val BrassText = MaktabaTokens.Light.accentText

    /** The Light tokens as Material's light scheme. */
    val Light: ColorScheme = with(MaktabaTokens.Light) {
        lightColorScheme(
            primary = accentText,
            onPrimary = bg,
            primaryContainer = chosen,
            onPrimaryContainer = accentFg,
            secondary = MaktabaTokens.Brand.brand600,
            onSecondary = accentFg,
            secondaryContainer = MaktabaTokens.Brand.brand100,
            onSecondaryContainer = accentFg,
            tertiary = accent,
            onTertiary = accentFg,
            background = bg,
            onBackground = fg,
            surface = bg,
            onSurface = fg,
            surfaceVariant = subtle,
            onSurfaceVariant = muted,
            surfaceContainerLowest = bg,
            surfaceContainerLow = nav,
            surfaceContainer = subtle,
            surfaceContainerHigh = raised,
            surfaceContainerHighest = border,
            outline = outline(muted, border),
            outlineVariant = border,
            error = error,
            onError = bg,
        )
    }

    /** The Dark tokens as Material's dark scheme: the apps' default (FR-2). */
    val Dark: ColorScheme = with(MaktabaTokens.Dark) {
        darkColorScheme(
            primary = accent,
            onPrimary = accentFg,
            primaryContainer = chosen,
            onPrimaryContainer = MaktabaTokens.Brand.brand200,
            secondary = accent,
            onSecondary = accentFg,
            secondaryContainer = chosen,
            onSecondaryContainer = MaktabaTokens.Brand.brand200,
            tertiary = accent,
            onTertiary = accentFg,
            background = bg,
            onBackground = fg,
            surface = bg,
            onSurface = fg,
            surfaceVariant = subtle,
            onSurfaceVariant = muted,
            surfaceContainerLowest = bg,
            surfaceContainerLow = nav,
            surfaceContainer = subtle,
            surfaceContainerHigh = raised,
            surfaceContainerHighest = border,
            outline = outline(muted, border),
            outlineVariant = border,
            error = error,
            onError = bg,
        )
    }

    /** The app-only roles Material's scheme has no slot for, in the light theme. */
    val LightBrand = BrandColors(
        raised = MaktabaTokens.Light.raised, nav = MaktabaTokens.Light.nav, scrim = MaktabaTokens.Light.scrim,
        chosen = MaktabaTokens.Light.chosen, accentSoft = MaktabaTokens.Light.accentSoft, accentText = MaktabaTokens.Light.accentText,
        success = MaktabaTokens.Light.success, warn = MaktabaTokens.Light.warn, running = MaktabaTokens.Light.running, info = MaktabaTokens.Light.info,
    )

    /** The app-only roles Material's scheme has no slot for, in the dark theme. */
    val DarkBrand = BrandColors(
        raised = MaktabaTokens.Dark.raised, nav = MaktabaTokens.Dark.nav, scrim = MaktabaTokens.Dark.scrim,
        chosen = MaktabaTokens.Dark.chosen, accentSoft = MaktabaTokens.Dark.accentSoft, accentText = MaktabaTokens.Dark.accentText,
        success = MaktabaTokens.Dark.success, warn = MaktabaTokens.Dark.warn, running = MaktabaTokens.Dark.running, info = MaktabaTokens.Dark.info,
    )

    /**
     * A control's outline: secondary text 40 % of the way to the hairline border, so it keeps
     * 3:1 on the page (NFR-8) in both themes while staying quieter than text.
     */
    private fun outline(muted: Color, border: Color): Color = lerp(muted, border, 0.4f)
}

/**
 * The tokens' app-only roles (a sheet, the navigation bar, the scrim over the library art, a
 * chosen row, status colours), beside Material's scheme: read them as [MaktabaTheme.brand].
 */
@Immutable
data class BrandColors(
    val raised: Color,
    val nav: Color,
    val scrim: Color,
    val chosen: Color,
    val accentSoft: Color,
    val accentText: Color,
    val success: Color,
    val warn: Color,
    val running: Color,
    val info: Color,
)

private val LocalBrandColors = staticCompositionLocalOf { InkAndBrass.DarkBrand }

/** What the theme provides beside [MaterialTheme]. */
object MaktabaTheme {
    /** The app-only colours of the theme in use. */
    val brand: BrandColors
        @Composable @ReadOnlyComposable get() = LocalBrandColors.current
}

/** The web's type: IBM Plex Sans Arabic for text, Amiri for headings (both SIL OFL). */
object MaktabaFonts {
    val Plex = FontFamily(
        Font(R.font.ibm_plex_sans_arabic_regular, FontWeight.Normal),
        Font(R.font.ibm_plex_sans_arabic_medium, FontWeight.Medium),
        Font(R.font.ibm_plex_sans_arabic_semibold, FontWeight.SemiBold),
        Font(R.font.ibm_plex_sans_arabic_semibold, FontWeight.Bold),
    )
    val Amiri = FontFamily(Font(R.font.amiri_bold, FontWeight.Bold))

    val typography: Typography = Typography().let { t ->
        fun TextStyle.plex() = copy(fontFamily = Plex)
        fun TextStyle.amiri() = copy(fontFamily = Amiri, fontWeight = FontWeight.Bold)
        t.copy(
            displayLarge = t.displayLarge.amiri(), displayMedium = t.displayMedium.amiri(), displaySmall = t.displaySmall.amiri(),
            headlineLarge = t.headlineLarge.amiri(), headlineMedium = t.headlineMedium.amiri(), headlineSmall = t.headlineSmall.amiri(),
            titleLarge = t.titleLarge.plex(), titleMedium = t.titleMedium.plex(), titleSmall = t.titleSmall.plex(),
            bodyLarge = t.bodyLarge.plex(), bodyMedium = t.bodyMedium.plex(), bodySmall = t.bodySmall.plex(),
            labelLarge = t.labelLarge.plex(), labelMedium = t.labelMedium.plex(), labelSmall = t.labelSmall.plex(),
        )
    }
}

/** The app's theme: the picked mode, dark while [forceDark] (as while a video plays). */
@Composable
fun MaktabaTheme(mode: ThemeMode, forceDark: Boolean = false, content: @Composable () -> Unit) {
    val dark = forceDark || when (mode) {
        ThemeMode.System -> isSystemInDarkTheme()
        ThemeMode.Light -> false
        ThemeMode.Dark -> true
    }
    MaterialTheme(colorScheme = if (dark) InkAndBrass.Dark else InkAndBrass.Light, typography = MaktabaFonts.typography) {
        CompositionLocalProvider(LocalBrandColors provides if (dark) InkAndBrass.DarkBrand else InkAndBrass.LightBrand) {
            SystemBars(dark)
            content()
        }
    }
}
