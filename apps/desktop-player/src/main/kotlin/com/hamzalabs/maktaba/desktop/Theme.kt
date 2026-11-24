package com.hamzalabs.maktaba.desktop

import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Typography
import androidx.compose.material3.darkColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.platform.Font
import androidx.compose.ui.unit.Density

/** The apps' Ink & Brass, dark: the token build's colours (Tokens.kt, story 13.15). */
private val InkAndBrass = darkColorScheme(
    primary = Tokens.Ink.accent,
    onPrimary = Tokens.Ink.accentFg,
    primaryContainer = Tokens.Ink.chosen,
    onPrimaryContainer = Tokens.Brand.brand200,
    secondary = Tokens.Ink.accent,
    onSecondary = Tokens.Ink.accentFg,
    secondaryContainer = Tokens.Ink.chosen,
    onSecondaryContainer = Tokens.Brand.brand200,
    background = Tokens.Desktop.window,
    onBackground = Tokens.Ink.fg,
    surface = Tokens.Desktop.window,
    onSurface = Tokens.Ink.fg,
    surfaceVariant = Tokens.Desktop.subtle,
    onSurfaceVariant = Tokens.Ink.muted,
    surfaceContainerLow = Tokens.Desktop.pane,
    surfaceContainer = Tokens.Desktop.subtle,
    surfaceContainerHigh = Tokens.Desktop.raised,
    surfaceContainerHighest = Tokens.Desktop.hover,
    outline = Tokens.Desktop.control,
    outlineVariant = Tokens.Desktop.line,
    error = Tokens.Ink.error,
    onError = Tokens.Ink.bg,
)

/** The web's type (IBM Plex Sans Arabic, Amiri for headings): the Android core's font files. */
private val Plex = FontFamily(
    Font("ibm_plex_sans_arabic_regular.ttf", FontWeight.Normal),
    Font("ibm_plex_sans_arabic_medium.ttf", FontWeight.Medium),
    Font("ibm_plex_sans_arabic_semibold.ttf", FontWeight.SemiBold),
    Font("ibm_plex_sans_arabic_semibold.ttf", FontWeight.Bold),
)
private val Amiri = FontFamily(Font("amiri_bold.ttf", FontWeight.Bold))

private fun typography(): Typography {
    val base = Typography()
    fun TextStyle.plex() = copy(fontFamily = Plex)
    fun TextStyle.amiri() = copy(fontFamily = Amiri, fontWeight = FontWeight.Bold)
    return base.copy(
        displayLarge = base.displayLarge.amiri(), displayMedium = base.displayMedium.amiri(),
        displaySmall = base.displaySmall.amiri(), headlineLarge = base.headlineLarge.amiri(),
        headlineMedium = base.headlineMedium.amiri(), headlineSmall = base.headlineSmall.amiri(),
        titleLarge = base.titleLarge.amiri(), titleMedium = base.titleMedium.plex(),
        titleSmall = base.titleSmall.plex(), bodyLarge = base.bodyLarge.plex(),
        bodyMedium = base.bodyMedium.plex(), bodySmall = base.bodySmall.plex(),
        labelLarge = base.labelLarge.plex(), labelMedium = base.labelMedium.plex(),
        labelSmall = base.labelSmall.plex(),
    )
}

/** How much bigger everything is in big screen (Epic 46's 10-foot sizes: 28 px body at 1080p). */
const val BIG_SCALE = 1.75f

/**
 * The player's look, in [texts]' language and direction. In big screen everything is scaled
 * up, as across a room.
 */
@Composable
fun PlayerTheme(texts: Texts, big: Boolean, content: @Composable () -> Unit) {
    val density = LocalDensity.current
    val scaled = if (big) Density(density.density * BIG_SCALE, density.fontScale) else density
    MaterialTheme(colorScheme = InkAndBrass, typography = typography()) {
        CompositionLocalProvider(
            LocalTexts provides texts,
            LocalLayoutDirection provides texts.lang.direction,
            LocalDensity provides scaled,
            content = content,
        )
    }
}
