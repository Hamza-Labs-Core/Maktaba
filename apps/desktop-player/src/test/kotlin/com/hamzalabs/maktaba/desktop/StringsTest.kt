package com.hamzalabs.maktaba.desktop

import androidx.compose.ui.unit.LayoutDirection
import com.hamzalabs.maktaba.desktop.shell.featureOffSentence
import com.hamzalabs.maktaba.desktop.shell.featureUnavailableSentence
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertThrows
import org.junit.Assert.assertTrue
import org.junit.Test

class StringsTest {
    private fun placeholders(s: String) = Regex("""\{\d}""").findAll(s).map { it.value }.toSet()

    @Test
    fun everyStringIsInBothLanguages() {
        assertEquals(Strings.EN.keys, Strings.AR.keys)
        for ((k, en) in Strings.EN) assertEquals(k, placeholders(en), placeholders(Strings.AR.getValue(k)))
    }

    @Test
    fun everyTableHasBothLanguagesWithTheSamePlaceholders() {
        for (part in Strings.parts) {
            assertEquals(part.en.keys, part.ar.keys)
            for ((k, en) in part.en) assertEquals(k, placeholders(en), placeholders(part.ar.getValue(k)))
        }
    }

    @Test
    fun aKeyDefinedTwiceIsAnErrorNamingIt() {
        val a = Strings.Part(mapOf("x.one" to "One"), mapOf("x.one" to "واحد"))
        val b = Strings.Part(mapOf("x.one" to "Again"), mapOf("x.one" to "مجددًا"))
        val e = assertThrows(IllegalStateException::class.java) { Strings.merge(listOf(a, b)) }
        assertTrue(e.message!!, e.message!!.contains("x.one"))
    }

    @Test
    fun everyKnownFeatureHasAnOffAndAnUnavailableSentence() {
        for (lang in Lang.entries) for (f in com.hamzalabs.maktaba.core.api.Feature.known) {
            val t = Texts(lang)
            for (s in listOf(t.featureOffSentence(f), t.featureUnavailableSentence(f))) {
                assertTrue("$lang $f: $s", s.isNotBlank() && !s.startsWith("feature.") && !s.contains("{0}") && !s.contains(f))
            }
        }
        // a feature this app does not know: the general sentence, with its id
        assertEquals("The tea feature is off on this home.", Texts(Lang.En).featureOffSentence("tea"))
    }

    @Test
    fun theHomeIsAlJihazAlRaeesiInEveryNewArabicString() {
        for (part in Strings.parts.drop(1)) for (v in part.ar.values) assertFalse(v, v.contains("الخادم الرئيسي"))
    }

    @Test
    fun arabicIsRightToLeft() {
        assertEquals(LayoutDirection.Rtl, Lang.Ar.direction)
        assertEquals("تسجيل الدخول إلى البيت", Texts(Lang.Ar)["signin.title", "البيت"])
        assertEquals(Lang.Ar, Lang.of("ar"))
        assertEquals(Lang.En, Lang.of("en"))
    }

    @Test
    fun everyErrorKindHasASentence() {
        for (lang in Lang.entries) for (kind in com.hamzalabs.maktaba.core.api.ErrorKind.entries) {
            val s = Texts(lang).sentence(kind)
            assertTrue("$lang $kind: $s", s.isNotBlank() && !s.startsWith("error."))
        }
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
