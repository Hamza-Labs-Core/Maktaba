package com.hamzalabs.maktaba.core

import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File

/**
 * Every app ships in English and Arabic together: each English string and plural of every Android
 * module (core, player, remote, and the Android TV app) has its Arabic, with the same format
 * arguments, and the Arabic has nothing the English no longer has. Lint's MissingTranslation says
 * the same at build time; this says it in the unit tests, for every module at once.
 */
class TranslationsTest {
    // the tests run in the module's folder: apps/android/core (also from the TV app's build)
    private val android = File("..").canonicalFile
    private val tv = File(android, "../tv/android/app").canonicalFile

    private fun english(): List<File> =
        (android.listFiles().orEmpty().map { File(it, "src/main/res/values/strings.xml") } + File(tv, "src/main/res/values/strings.xml"))
            .filter { it.isFile }

    @Test
    fun everyModuleIsChecked() {
        val modules = english().map { it.parentFile.parentFile.parentFile.parentFile.parentFile.name }.toSet()
        assertTrue("found: $modules", modules.containsAll(setOf("core", "player", "remote", "app")))
    }

    @Test
    fun everyEnglishStringHasItsArabic() {
        val problems = english().flatMap { en -> problems(en, File(en.parentFile.parentFile, "values-ar/strings.xml")) }
        assertTrue("Missing or mismatched Arabic:\n" + problems.joinToString("\n"), problems.isEmpty())
    }

    /** What is wrong with [arFile] as the Arabic of [enFile], one line each. */
    private fun problems(enFile: File, arFile: File): List<String> {
        val en = StringsXml(enFile)
        if (en.strings.isEmpty() && en.plurals.isEmpty()) return emptyList()
        if (!arFile.isFile) return listOf("$arFile: missing (the English has ${en.strings.size + en.plurals.size} strings)")
        val ar = StringsXml(arFile)
        val where = enFile.path.substringAfter(android.parentFile.path + File.separator)
        return buildList {
            for ((name, text) in en.strings) {
                val a = ar.strings[name]
                when {
                    a == null -> add("$where: string $name has no Arabic")
                    a.isBlank() -> add("$where: string $name is empty in Arabic")
                    StringsXml.args(a) != StringsXml.args(text) ->
                        add("$where: string $name takes ${StringsXml.args(text)} in English, ${StringsXml.args(a)} in Arabic")
                }
            }
            for ((name, items) in en.plurals) {
                val a = ar.plurals[name]
                if (a == null) {
                    add("$where: plurals $name has no Arabic")
                    continue
                }
                if ("other" !in a) add("$where: plurals $name has no Arabic \"other\"")
                val enArgs = items.values.flatMap(StringsXml::args).toSet()
                val arArgs = a.values.flatMap(StringsXml::args).toSet()
                // an Arabic "one" or "two" may leave the number out (مهمة واحدة), never take another
                if (arArgs != enArgs) add("$where: plurals $name takes $enArgs in English, $arArgs in Arabic")
            }
            (ar.strings.keys - en.strings.keys).forEach { add("$where: Arabic string $it is not in the English") }
            (ar.plurals.keys - en.plurals.keys).forEach { add("$where: Arabic plurals $it is not in the English") }
        }
    }

    @Test
    fun formatArgumentsAreReadAsAndroidDoes() {
        assertTrue(StringsXml.args("%1\$s has not installed in %2\$d windows: %3\$s") == setOf("1\$s", "2\$d", "3\$s"))
        assertTrue(StringsXml.args("CPU %1\$d%%") == setOf("1\$d"))
        assertTrue(StringsXml.args("%1\$.1f a minute") == setOf("1\$f"))
        assertTrue(StringsXml.unescape("That is not a home\\'s address.") == "That is not a home's address.")
    }

    @Test
    fun aMissingOrMismatchedArabicFails() {
        val dir = kotlin.io.path.createTempDirectory("translations").toFile()
        try {
            val en = File(dir, "values/strings.xml").apply { parentFile.mkdirs() }
            val ar = File(dir, "values-ar/strings.xml").apply { parentFile.mkdirs() }
            en.writeText(
                """<resources><string name="a">Hello</string><string name="b">Version %1${'$'}s</string>""" +
                    """<string name="c">Only English</string><string name="brand" translatable="false">Maktaba</string>""" +
                    """<plurals name="p"><item quantity="one">%1${'$'}d job</item><item quantity="other">%1${'$'}d jobs</item></plurals></resources>""",
            )
            ar.writeText(
                """<resources><string name="a">مرحبًا</string><string name="b">الإصدار %1${'$'}d</string>""" +
                    """<plurals name="p"><item quantity="one">مهمة واحدة</item><item quantity="other">%1${'$'}d مهمة</item></plurals></resources>""",
            )
            val found = problems(en, ar)
            assertTrue(found.toString(), found.size == 2)
            assertTrue(found.toString(), found.any { "string b takes" in it } && found.any { "string c has no Arabic" in it })
        } finally {
            dir.deleteRecursively()
        }
    }
}
