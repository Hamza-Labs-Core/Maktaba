package com.hamzalabs.maktaba.tv

import com.hamzalabs.maktaba.core.StringsXml
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File

/**
 * The code screen says where to approve it in the phones' own words: its instruction takes the
 * phone apps' button label (core `approve_tv`) and the older apps' one as arguments, so the TV
 * cannot name a button the phones no longer show. And every TV string ships in Arabic too.
 */
class SignInStringsTest {
    // the tests run in the module's folder: apps/tv/android/app
    private val tvEn = StringsXml(File("src/main/res/values/strings.xml"))
    private val tvAr = StringsXml(File("src/main/res/values-ar/strings.xml"))
    private val coreEn = StringsXml(File("../../../android/core/src/main/res/values/strings.xml"))
    private val coreAr = StringsXml(File("../../../android/core/src/main/res/values-ar/strings.xml"))

    private fun StringsXml.get(name: String) = strings[name] ?: error("$name is not in ${file.path}")

    private fun where(tv: StringsXml, core: StringsXml): String =
        String.format(tv.get("signin_code_where"), core.get("approve_tv"), tv.get("signin_code_old_label"))

    @Test
    fun theInstructionNamesThePhonesButtonInBothLanguages() {
        for ((tv, core) in listOf(tvEn to coreEn, tvAr to coreAr)) {
            val template = tv.get("signin_code_where")
            assertEquals("${tv.file.path}: signin_code_where's arguments", setOf("1\$s", "2\$s"), StringsXml.args(template))
            val shown = where(tv, core)
            assertTrue("$shown\nnames ${core.get("approve_tv")}", shown.contains(core.get("approve_tv")))
            assertTrue("$shown\nnames ${tv.get("signin_code_old_label")}", shown.contains(tv.get("signin_code_old_label")))
            assertTrue("$shown\nnames the apps", shown.contains("Maktaba Remote") && shown.contains("Maktaba Player"))
        }
    }

    @Test
    fun thePhonesButtonSaysTvOrDevice() {
        assertEquals("Approve a TV or device", coreEn.get("approve_tv"))
        assertEquals("الموافقة على تلفاز أو جهاز", coreAr.get("approve_tv"))
        // the older phone apps' entry: the TV names it as they show it
        assertEquals("the label older phone apps show", "Approve a sign-in", tvEn.get("signin_code_old_label"))
        assertEquals("الموافقة على تسجيل دخول", tvAr.get("signin_code_old_label"))
    }

    @Test
    fun theFoundLineNamesThePhonesApproveButton() {
        for ((tv, core) in listOf(tvEn to coreEn, tvAr to coreAr)) {
            val shown = String.format(tv.get("signin_code_looked_up"), core.get("approve_button"))
            assertTrue(shown, shown.contains(core.get("approve_button")))
        }
    }

    @Test
    fun everyTvStringHasItsArabic() {
        val problems = buildList {
            for ((name, text) in tvEn.strings) {
                val a = tvAr.strings[name]
                when {
                    a.isNullOrBlank() -> add("$name has no Arabic")
                    StringsXml.args(a) != StringsXml.args(text) -> add("$name's arguments differ in Arabic")
                }
            }
            for (name in tvEn.plurals.keys - tvAr.plurals.keys) add("plurals $name has no Arabic")
            (tvAr.strings.keys - tvEn.strings.keys).forEach { add("Arabic $it is not in the English") }
        }
        assertTrue("Missing or mismatched Arabic:\n" + problems.joinToString("\n"), problems.isEmpty())
    }
}
