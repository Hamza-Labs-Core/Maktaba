package com.hamzalabs.maktaba.core

import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.hamzalabs.maktaba.core.ui.MaktabaTokens
import org.junit.Assert.assertEquals
import org.junit.Test

/** The generated Tokens.kt (Story 48.2) carries Ink & Brass from web/design-system/tokens/. */
class TokensTest {
    @Test
    fun darkIsInkAndBrass() {
        assertEquals(Color(0xFF0B111C), MaktabaTokens.Dark.bg)
        assertEquals(Color(0xFFE3B252), MaktabaTokens.Dark.accent)
        assertEquals(Color(0xFFFFFFFF), MaktabaTokens.Light.bg)
        assertEquals(Color(0xFF8A6212), MaktabaTokens.Light.accentText)
    }

    @Test
    fun keepsTranslucencyAndScales() {
        assertEquals(0x8F / 255f, MaktabaTokens.Dark.scrim.alpha, 0.002f)
        assertEquals(16.dp, MaktabaTokens.Spacing.s4)
        assertEquals(12.dp, MaktabaTokens.Radius.lg)
        assertEquals(16.sp, MaktabaTokens.TypeSize.base)
        assertEquals("Amiri", MaktabaTokens.Fonts.HEADING)
    }
}
