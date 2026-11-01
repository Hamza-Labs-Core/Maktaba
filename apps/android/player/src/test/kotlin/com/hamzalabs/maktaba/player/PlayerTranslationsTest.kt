package com.hamzalabs.maktaba.player

import com.hamzalabs.maktaba.core.StringsXml
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File

/**
 * The Player ships in English and Arabic together, in every strings file: `strings.xml` (which
 * the core's TranslationsTest checks for every module) and each screen's own table,
 * `strings_<screen>.xml` (Epic 48's one table per screen story), which only this test reads.
 */
class PlayerTranslationsTest {
    // the tests run in the module's folder: apps/android/player
    private val values = File("src/main/res/values")

    private fun tables(): List<File> =
        values.listFiles { f -> f.name.startsWith("strings") && f.name.endsWith(".xml") }.orEmpty().sortedBy { it.name }

    @Test
    fun theShellsTableIsChecked() {
        val names = tables().map { it.name }
        assertTrue("found: $names", names.containsAll(listOf("strings.xml", "strings_shell.xml")))
    }

    @Test
    fun everyTableHasItsArabic() {
        val problems = tables().flatMap { en ->
            val arFile = File(values.parentFile, "values-ar/${en.name}")
            val enXml = StringsXml(en)
            if (!arFile.isFile) return@flatMap listOf("${en.name}: no values-ar/${en.name}")
            val ar = StringsXml(arFile)
            buildList {
                for ((name, text) in enXml.strings) {
                    val a = ar.strings[name]
                    when {
                        a.isNullOrBlank() -> add("${en.name}: $name has no Arabic")
                        StringsXml.args(a) != StringsXml.args(text) -> add("${en.name}: $name's arguments differ in Arabic")
                    }
                }
                for (name in enXml.plurals.keys - ar.plurals.keys) add("${en.name}: plurals $name has no Arabic")
                (ar.strings.keys - enXml.strings.keys).forEach { add("${en.name}: Arabic $it is not in the English") }
            }
        }
        assertTrue("Missing or mismatched Arabic:\n" + problems.joinToString("\n"), problems.isEmpty())
    }
}
