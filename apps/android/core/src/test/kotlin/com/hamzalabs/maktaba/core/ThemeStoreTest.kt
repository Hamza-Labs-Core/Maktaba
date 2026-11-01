package com.hamzalabs.maktaba.core

import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.luminance
import com.hamzalabs.maktaba.core.ui.InkAndBrass
import com.hamzalabs.maktaba.core.ui.MaktabaTokens
import com.hamzalabs.maktaba.core.ui.ThemeMode
import com.hamzalabs.maktaba.core.ui.ThemeStore
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class ThemeStoreTest {
    @Test
    fun keepsTheThemeAsTheWebNamesIt() {
        val store = MapStore()
        ThemeStore(store).set(ThemeMode.Light)
        assertEquals("light", store.map[ThemeMode.STORAGE_KEY])
        assertEquals(ThemeMode.Light, ThemeStore(store).mode.value)
        ThemeStore(store).set(ThemeMode.System)
        assertEquals(ThemeMode.System, ThemeStore(store).mode.value)
        assertEquals(ThemeMode.Light, ThemeMode.parse("light"))
        assertEquals(ThemeMode.System, ThemeMode.parse("SYSTEM"))
    }

    /** FR-2: with no stored choice the app opens dark, whatever the system says. */
    @Test
    fun opensDarkWithNoChoice() {
        assertEquals(ThemeMode.Dark, ThemeMode.parse(null))
        assertEquals(ThemeMode.Dark, ThemeStore(MapStore()).mode.value)
        // a choice this version does not know (a later one's, or a typo) is no choice
        assertEquals(ThemeMode.Dark, ThemeMode.parse("sepia"))
        assertEquals(ThemeMode.Dark, ThemeMode.parse(""))
    }

    /** InkAndBrass is the generated tokens (Tokens.kt, Story 48.2), not colours of its own. */
    @Test
    fun inkAndBrassReadsTheTokens() {
        with(InkAndBrass.Dark) {
            val t = MaktabaTokens.Dark
            assertEquals(t.bg, background)
            assertEquals(t.bg, surface)
            assertEquals(t.fg, onBackground)
            assertEquals(t.fg, onSurface)
            assertEquals(t.muted, onSurfaceVariant)
            assertEquals(t.subtle, surfaceVariant)
            assertEquals(t.nav, surfaceContainerLow)
            assertEquals(t.raised, surfaceContainerHigh)
            assertEquals(t.accent, primary)
            assertEquals(t.accentFg, onPrimary)
            assertEquals(t.chosen, primaryContainer)
            assertEquals(t.border, outlineVariant)
            assertEquals(t.error, error)
        }
        with(InkAndBrass.Light) {
            val t = MaktabaTokens.Light
            assertEquals(t.bg, background)
            assertEquals(t.fg, onSurface)
            assertEquals(t.muted, onSurfaceVariant)
            assertEquals(t.accentText, primary)
            assertEquals(t.chosen, primaryContainer)
            assertEquals(t.border, outlineVariant)
            assertEquals(t.error, error)
        }
        assertEquals(MaktabaTokens.Dark.accent, InkAndBrass.Brass)
        assertEquals(MaktabaTokens.Light.accentText, InkAndBrass.BrassText)
        assertEquals(MaktabaTokens.Dark.scrim, InkAndBrass.DarkBrand.scrim)
        assertEquals(MaktabaTokens.Light.scrim, InkAndBrass.LightBrand.scrim)
        assertEquals(MaktabaTokens.Dark.raised, InkAndBrass.DarkBrand.raised)
    }

    /** NFR-8: a control's outline keeps 3:1 on the page, text 4.5:1, in both themes. */
    @Test
    fun outlinesAndTextKeepTheirContrast() {
        listOf(InkAndBrass.Dark, InkAndBrass.Light).forEach { s ->
            assertTrue("outline ${s.outline}", contrast(s.outline, s.background) >= 3f)
            assertTrue("text", contrast(s.onBackground, s.background) >= 4.5f)
            assertTrue("muted", contrast(s.onSurfaceVariant, s.background) >= 4.5f)
            assertTrue("primary", contrast(s.primary, s.background) >= 4.5f)
            assertTrue("on primary", contrast(s.onPrimary, s.primary) >= 4.5f)
        }
    }

    private fun contrast(a: Color, b: Color): Float {
        val (hi, lo) = listOf(a.luminance(), b.luminance()).sortedDescending()
        return (hi + 0.05f) / (lo + 0.05f)
    }
}
