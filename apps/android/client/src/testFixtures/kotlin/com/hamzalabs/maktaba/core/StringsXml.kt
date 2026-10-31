package com.hamzalabs.maktaba.core

import org.w3c.dom.Element
import java.io.File
import javax.xml.parsers.DocumentBuilderFactory

/**
 * An Android strings.xml read on the JVM: its strings and plurals by name, unescaped as Android
 * reads them (`\'`, `\"`, `\n`, `\@`). For the tests of every module: the translations guard and
 * the words the apps say, in English and Arabic.
 */
class StringsXml(val file: File) {
    /** The translatable strings: name to text. */
    val strings: Map<String, String>

    /** The translatable plurals: name to quantity (zero, one, two, few, many, other) to text. */
    val plurals: Map<String, Map<String, String>>

    init {
        val doc = DocumentBuilderFactory.newInstance().newDocumentBuilder().parse(file)
        val root = doc.documentElement
        strings = root.children("string").filter(::translatable).associate { it.getAttribute("name") to unescape(it.textContent) }
        plurals = root.children("plurals").filter(::translatable).associate { p ->
            p.getAttribute("name") to p.children("item").associate { it.getAttribute("quantity") to unescape(it.textContent) }
        }
    }

    companion object {
        /** `%1$s`, `%2$d`, `%1$.1f`: the positional format arguments of [text], as "1$s", ... */
        private val ARG = Regex("""%(\d+\$)?[-#+ 0,(]*\d*(?:\.\d+)?([a-zA-Z])""")

        fun args(text: String): Set<String> =
            ARG.findAll(text.replace("%%", "")).map { (it.groupValues[1].ifEmpty { "?$" }) + it.groupValues[2] }.toSet()

        fun unescape(s: String): String {
            val t = s.trim().let { if (it.length >= 2 && it.startsWith('"') && it.endsWith('"')) it.substring(1, it.length - 1) else it }
            val out = StringBuilder()
            var i = 0
            while (i < t.length) {
                val c = t[i]
                if (c == '\\' && i + 1 < t.length) {
                    out.append(
                        when (val n = t[i + 1]) {
                            'n' -> '\n'
                            't' -> '\t'
                            else -> n
                        },
                    )
                    i += 2
                } else {
                    out.append(c)
                    i++
                }
            }
            return out.toString()
        }

        private fun translatable(e: Element) = e.getAttribute("translatable") != "false"

        private fun Element.children(tag: String): List<Element> =
            (0 until childNodes.length).map { childNodes.item(it) }.filterIsInstance<Element>().filter { it.tagName == tag }
    }
}
