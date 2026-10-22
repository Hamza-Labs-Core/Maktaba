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
        content = content,
    )
}
