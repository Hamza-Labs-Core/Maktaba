package com.hamzalabs.maktaba.tv.ui.theme

import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.Font
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.sp
import androidx.tv.material3.ExperimentalTvMaterial3Api
import androidx.tv.material3.Typography
import com.hamzalabs.maktaba.tv.R

/**
 * Bundled fonts (SIL OFL 1.1 — licence texts ship in
 * `assets/licenses/`). Amiri, a classical Naskh, is the heading face;
 * IBM Plex Sans Arabic is the body face. Both cover Latin as well, so
 * English and Arabic UI text share the same look.
 */
val Amiri = FontFamily(
    Font(R.font.amiri_bold, FontWeight.Bold),
)

val PlexSansArabic = FontFamily(
    Font(R.font.ibm_plex_sans_arabic_regular, FontWeight.Normal),
    Font(R.font.ibm_plex_sans_arabic_medium, FontWeight.Medium),
    Font(R.font.ibm_plex_sans_arabic_semibold, FontWeight.SemiBold),
)

/**
 * TV type scale: sizes are up-scaled for the 10-foot distance (body text
 * starts at 18sp, not 14). Display/headline/title use Amiri Bold; body
 * and labels use Plex. Amiri's tall Naskh ascenders need generous line
 * heights or Arabic diacritics clip.
 */
@OptIn(ExperimentalTvMaterial3Api::class)
val MaktabaTypography = Typography(
    displayLarge = TextStyle(fontFamily = Amiri, fontWeight = FontWeight.Bold, fontSize = 57.sp, lineHeight = 80.sp),
    displayMedium = TextStyle(fontFamily = Amiri, fontWeight = FontWeight.Bold, fontSize = 45.sp, lineHeight = 64.sp),
    headlineLarge = TextStyle(fontFamily = Amiri, fontWeight = FontWeight.Bold, fontSize = 36.sp, lineHeight = 52.sp),
    headlineMedium = TextStyle(fontFamily = Amiri, fontWeight = FontWeight.Bold, fontSize = 32.sp, lineHeight = 46.sp),
    titleLarge = TextStyle(fontFamily = Amiri, fontWeight = FontWeight.Bold, fontSize = 26.sp, lineHeight = 38.sp),
    titleMedium = TextStyle(fontFamily = PlexSansArabic, fontWeight = FontWeight.SemiBold, fontSize = 20.sp, lineHeight = 28.sp),
    bodyLarge = TextStyle(fontFamily = PlexSansArabic, fontWeight = FontWeight.Normal, fontSize = 18.sp, lineHeight = 28.sp),
    bodyMedium = TextStyle(fontFamily = PlexSansArabic, fontWeight = FontWeight.Normal, fontSize = 16.sp, lineHeight = 24.sp),
    labelLarge = TextStyle(fontFamily = PlexSansArabic, fontWeight = FontWeight.Medium, fontSize = 16.sp, lineHeight = 24.sp),
    labelMedium = TextStyle(fontFamily = PlexSansArabic, fontWeight = FontWeight.Medium, fontSize = 14.sp, lineHeight = 20.sp),
)
