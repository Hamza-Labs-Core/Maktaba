package com.hamzalabs.maktaba.remote

import com.hamzalabs.maktaba.core.StringsXml
import java.io.File
import java.util.Locale

/**
 * The Remote's words on the JVM: its own res/values (English) or res/values-ar (Arabic), read as
 * Android reads them, so the tests say exactly what the app says in either language.
 */
class XmlLookup private constructor(override val locale: Locale, private val dir: String) : Lookup {
    private val xml = StringsXml(File("src/main/res/$dir/strings.xml"))

    override fun text(id: Int, vararg args: Any): String {
        val name = stringNames[id] ?: error("no string with id $id")
        val t = xml.strings[name] ?: error("$dir has no string $name")
        return if (args.isEmpty()) t else String.format(locale, t, *args)
    }

    override fun plural(id: Int, count: Int, vararg args: Any): String {
        val name = pluralNames[id] ?: error("no plurals with id $id")
        val items = xml.plurals[name] ?: error("$dir has no plurals $name")
        val t = items[quantity(count)] ?: items.getValue("other")
        return String.format(locale, t, *args)
    }

    /** CLDR's plural rules for English and Arabic, as Android picks an item. */
    private fun quantity(n: Int): String = if (locale.language == "ar") {
        when {
            n == 0 -> "zero"
            n == 1 -> "one"
            n == 2 -> "two"
            n % 100 in 3..10 -> "few"
            n % 100 in 11..99 -> "many"
            else -> "other"
        }
    } else {
        if (n == 1) "one" else "other"
    }

    companion object {
        val english by lazy { XmlLookup(Locale.ENGLISH, "values") }
        val arabic by lazy { XmlLookup(Locale.forLanguageTag("ar"), "values-ar") }

        private val stringNames: Map<Int, String> by lazy { names(R.string::class.java) }
        private val pluralNames: Map<Int, String> by lazy { names(R.plurals::class.java) }

        private fun names(c: Class<*>): Map<Int, String> = c.fields.associate { it.getInt(null) to it.name }
    }
}
