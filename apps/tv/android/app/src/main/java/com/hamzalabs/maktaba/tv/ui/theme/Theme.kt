package com.hamzalabs.maktaba.tv.ui.theme

import androidx.compose.runtime.Composable
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.sp
import androidx.tv.material3.ExperimentalTvMaterial3Api
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Typography
import androidx.tv.material3.darkColorScheme

/**
 * Dark, TV-optimized theme. TVs are viewed in dim living rooms, so a
 * dark scheme reduces eye strain and screen burn; type is scaled up for
 * the 10-foot distance (body text starts at 18sp, not 14).
 */
@OptIn(ExperimentalTvMaterial3Api::class)
private val MaktabaColors = darkColorScheme(
    primary = Brand,
    onPrimary = Ink,
    surface = Surface,
    onSurface = OnSurface,
    background = Background,
    onBackground = OnSurface,
)

@OptIn(ExperimentalTvMaterial3Api::class)
private val MaktabaTypography = Typography(
    displayLarge = TextStyle(fontSize = 57.sp, fontWeight = FontWeight.Bold),
    headlineMedium = TextStyle(fontSize = 32.sp, fontWeight = FontWeight.SemiBold),
    titleLarge = TextStyle(fontSize = 26.sp, fontWeight = FontWeight.SemiBold),
    bodyLarge = TextStyle(fontSize = 18.sp, fontWeight = FontWeight.Normal),
    labelLarge = TextStyle(fontSize = 16.sp, fontWeight = FontWeight.Medium),
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
