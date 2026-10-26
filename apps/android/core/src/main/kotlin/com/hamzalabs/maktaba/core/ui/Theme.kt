package com.hamzalabs.maktaba.core.ui

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.ColorScheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Typography
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color
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
enum class ThemeMode(val label: String) {
    System("System"), Light("Light"), Dark("Dark");

    companion object {
        const val STORAGE_KEY = "theme"
        fun parse(s: String?): ThemeMode = entries.firstOrNull { it.name.equals(s, ignoreCase = true) } ?: System
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

/** "Ink & Brass", the web's tokens (`web/src/styles/tokens.css`). */
object InkAndBrass {
    /** --color-accent: the brand's gold, a fill colour. */
    val Brass = Color(0xFFE3B252)
    /** --color-accent-text on light: the gold dark enough for text (AA). */
    val BrassText = Color(0xFF8A6212)

    val Light: ColorScheme = lightColorScheme(
        primary = BrassText,
        onPrimary = Color.White,
        primaryContainer = Color(0xFFF3DCA2),
        onPrimaryContainer = Color(0xFF1A1408),
        secondary = Color(0xFFC8952C),
        onSecondary = Color(0xFF1A1408),
        secondaryContainer = Color(0xFFFBEFD3),
        onSecondaryContainer = Color(0xFF1A1408),
        tertiary = Brass,
        onTertiary = Color(0xFF1A1408),
        background = Color(0xFFFFFFFF),
        onBackground = Color(0xFF1C1810),
        surface = Color(0xFFFFFFFF),
        onSurface = Color(0xFF1C1810),
        surfaceVariant = Color(0xFFFBF5E8),
        onSurfaceVariant = Color(0xFF5A5040),
        surfaceContainerLowest = Color(0xFFFFFFFF),
        surfaceContainerLow = Color(0xFFFFF9EA),
        surfaceContainer = Color(0xFFFBF5E8),
        surfaceContainerHigh = Color(0xFFF8EFDB),
        surfaceContainerHighest = Color(0xFFF4E8CD),
        outline = Color(0xFF8C7C5C),
        outlineVariant = Color(0xFFEFE3C8),
        error = Color(0xFFB6253A),
        onError = Color.White,
    )

    val Dark: ColorScheme = darkColorScheme(
        primary = Brass,
        onPrimary = Color(0xFF1A1408),
        primaryContainer = Color(0xFF3A3020),
        onPrimaryContainer = Color(0xFFF3DCA2),
        secondary = Brass,
        onSecondary = Color(0xFF1A1408),
        secondaryContainer = Color(0xFF2A2618),
        onSecondaryContainer = Color(0xFFF3DCA2),
        tertiary = Brass,
        onTertiary = Color(0xFF1A1408),
        background = Color(0xFF0B111C),
        onBackground = Color(0xFFE8ECF3),
        surface = Color(0xFF0B111C),
        onSurface = Color(0xFFE8ECF3),
        surfaceVariant = Color(0xFF131D2E),
        onSurfaceVariant = Color(0xFFA9B3C4),
        surfaceContainerLowest = Color(0xFF080D16),
        surfaceContainerLow = Color(0xFF0F1725),
        surfaceContainer = Color(0xFF131D2E),
        surfaceContainerHigh = Color(0xFF18243A),
        surfaceContainerHighest = Color(0xFF1D2B44),
        outline = Color(0xFF5A6A86),
        outlineVariant = Color(0xFF22304A),
        error = Color(0xFFF0707F),
        onError = Color(0xFF1A0508),
    )
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
        SystemBars(dark)
        content()
    }
}
