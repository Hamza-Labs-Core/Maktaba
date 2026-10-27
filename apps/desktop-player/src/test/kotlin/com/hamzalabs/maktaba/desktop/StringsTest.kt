package com.hamzalabs.maktaba.desktop

import androidx.compose.ui.unit.LayoutDirection
import org.junit.Assert.assertEquals
import org.junit.Test

class StringsTest {
    private fun placeholders(s: String) = Regex("""\{\d}""").findAll(s).map { it.value }.toSet()

    @Test
    fun everyStringIsInBothLanguages() {
        assertEquals(Strings.EN.keys, Strings.AR.keys)
        for ((k, en) in Strings.EN) assertEquals(k, placeholders(en), placeholders(Strings.AR.getValue(k)))
    }

    @Test
    fun arabicIsRightToLeft() {
        assertEquals(LayoutDirection.Rtl, Lang.Ar.direction)
        assertEquals("تسجيل الدخول إلى البيت", Texts(Lang.Ar)["signin.title", "البيت"])
        assertEquals(Lang.Ar, Lang.of("ar"))
        assertEquals(Lang.En, Lang.of("en"))
    }

    @Test
    fun saysWhichAppItIs() {
        assertEquals("player/0.3.35 (windows; desktop)", clientHeader("0.3.35", "Windows 11"))
        assertEquals("player/0.3.35 (linux; desktop)", clientHeader("0.3.35", "Linux"))
    }

    @Test
    fun readsWhatThePersonTyped() {
        assertEquals(listOf("http://192.168.1.5:8088"), linkFor("192.168.1.5:8088")?.urls)
        assertEquals(
            listOf("http://10.0.0.2:8088"),
            linkFor("maktaba://connect?id=x&n=Kitchen&u=http%3A%2F%2F10.0.0.2%3A8088")?.urls,
        )
        assertEquals(null, linkFor("   "))
    }
}
