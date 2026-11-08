package com.hamzalabs.maktaba.tv.ui.theme

import androidx.compose.runtime.Composable
import androidx.tv.material3.ExperimentalTvMaterial3Api
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.darkColorScheme

/**
 * Dark, TV-optimized "Ink & Brass" theme. Every colour comes from
 * [MaktabaPalette]; this maps it onto TV Material's slots so stock
 * components (Surface, Tab, Button) pick it up without per-call colours.
 */
@OptIn(ExperimentalTvMaterial3Api::class)
private val MaktabaColors = darkColorScheme(
    primary = MaktabaPalette.Gold,
    onPrimary = MaktabaPalette.OnGold,
    primaryContainer = MaktabaPalette.Raised,
    onPrimaryContainer = MaktabaPalette.Gold,
    secondary = MaktabaPalette.GoldLight,
    onSecondary = MaktabaPalette.OnGold,
    secondaryContainer = MaktabaPalette.Raised,
    onSecondaryContainer = MaktabaPalette.Text,
    background = MaktabaPalette.Background,
    onBackground = MaktabaPalette.Text,
    surface = MaktabaPalette.Surface,
    onSurface = MaktabaPalette.Text,
    surfaceVariant = MaktabaPalette.Raised,
    onSurfaceVariant = MaktabaPalette.Muted,
    surfaceTint = MaktabaPalette.Gold,
    inverseSurface = MaktabaPalette.Gold,
    inverseOnSurface = MaktabaPalette.OnGold,
    // `border` is the stock focused-card ring colour in tv-material.
    border = MaktabaPalette.Gold,
    borderVariant = MaktabaPalette.Border,
)

@OptIn(ExperimentalTvMaterial3Api::class)
@Composable
fun MaktabaTvTheme(content: @Composable () -> Unit) {
    MaterialTheme(
        colorScheme = MaktabaColors,
        typography = MaktabaTypography,
    ) {
        // The phones' core composables (Material 3, e.g. the plugin panels) drawn on the TV:
        // the same palette, never Material's light defaults.
        androidx.compose.material3.MaterialTheme(colorScheme = CoreColors, content = content)
    }
}

/** [MaktabaColors] in the phones' Material 3 slots, for the core composables the TV shows. */
private val CoreColors = androidx.compose.material3.darkColorScheme(
    primary = MaktabaPalette.Gold,
    onPrimary = MaktabaPalette.OnGold,
    primaryContainer = MaktabaPalette.Raised,
    onPrimaryContainer = MaktabaPalette.Gold,
    secondary = MaktabaPalette.GoldLight,
    onSecondary = MaktabaPalette.OnGold,
    secondaryContainer = MaktabaPalette.Raised,
    onSecondaryContainer = MaktabaPalette.Text,
    background = MaktabaPalette.Background,
    onBackground = MaktabaPalette.Text,
    surface = MaktabaPalette.Surface,
    onSurface = MaktabaPalette.Text,
    surfaceVariant = MaktabaPalette.Raised,
    onSurfaceVariant = MaktabaPalette.Muted,
    surfaceTint = MaktabaPalette.Gold,
    inverseSurface = MaktabaPalette.Gold,
    inverseOnSurface = MaktabaPalette.OnGold,
    outline = MaktabaPalette.Border,
    outlineVariant = MaktabaPalette.Border,
)
