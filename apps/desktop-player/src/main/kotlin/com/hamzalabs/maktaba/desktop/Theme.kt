package com.hamzalabs.maktaba.desktop

import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Typography
import androidx.compose.material3.darkColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.platform.Font
import androidx.compose.ui.unit.Density

/** The apps' Ink & Brass, dark: the colours of the Android core's theme (`core/ui/Theme.kt`). */
private val InkAndBrass = darkColorScheme(
    primary = Color(0xFFE3B252),
    onPrimary = Color(0xFF1A1408),
    primaryContainer = Color(0xFF3A3020),
    onPrimaryContainer = Color(0xFFF3DCA2),
    secondary = Color(0xFFE3B252),
    onSecondary = Color(0xFF1A1408),
    secondaryContainer = Color(0xFF2A2618),
    onSecondaryContainer = Color(0xFFF3DCA2),
    background = Color(0xFF0B111C),
    onBackground = Color(0xFFE8ECF3),
    surface = Color(0xFF0B111C),
    onSurface = Color(0xFFE8ECF3),
    surfaceVariant = Color(0xFF131D2E),
    onSurfaceVariant = Color(0xFFA9B3C4),
    surfaceContainerLow = Color(0xFF0F1725),
    surfaceContainer = Color(0xFF131D2E),
    surfaceContainerHigh = Color(0xFF18243A),
    surfaceContainerHighest = Color(0xFF1D2B44),
    outline = Color(0xFF5A6A86),
    outlineVariant = Color(0xFF22304A),
    error = Color(0xFFF0707F),
    onError = Color(0xFF1A0508),
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
