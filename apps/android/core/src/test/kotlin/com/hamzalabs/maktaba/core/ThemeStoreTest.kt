package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.ui.ThemeMode
import com.hamzalabs.maktaba.core.ui.ThemeStore
import org.junit.Assert.assertEquals
import org.junit.Test

class ThemeStoreTest {
    @Test
    fun keepsTheThemeAsTheWebNamesIt() {
        val store = MapStore()
        assertEquals(ThemeMode.System, ThemeStore(store).mode.value)
        ThemeStore(store).set(ThemeMode.Dark)
        assertEquals("dark", store.map[ThemeMode.STORAGE_KEY])
        assertEquals(ThemeMode.Dark, ThemeStore(store).mode.value)
        assertEquals(ThemeMode.Light, ThemeMode.parse("light"))
        assertEquals(ThemeMode.System, ThemeMode.parse("sepia"))
    }
}
