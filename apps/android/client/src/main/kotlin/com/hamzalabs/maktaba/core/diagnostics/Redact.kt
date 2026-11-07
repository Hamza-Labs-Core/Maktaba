package com.hamzalabs.maktaba.core.diagnostics

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.boolean
import kotlinx.serialization.json.int
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive

/**
 * The shared redaction list (`shared/redact/rules.json`, Epic 53 story 53.01): the rules every
 * Maktaba logger uses, and the cases they all pass (`shared/redact/cases.json`). [REDACT_RULES_JSON]
 * is the copy `tools/log-schema/sync.py` writes. A replacement names its groups as `$1`, which
 * [Regex.replace] reads as is.
 */
object Redact {
    private class Rule(val regex: Regex, val replace: String, val needs: List<String>, val ignoreCase: Boolean)

    private val raw: JsonObject = Json.parseToJsonElement(REDACT_RULES_JSON).jsonObject

    private fun strings(o: JsonObject, key: String): List<String> =
        (o[key] as? JsonArray)?.map { it.jsonPrimitive.content } ?: emptyList()

    val keyMask: String = raw["key_mask"]!!.jsonPrimitive.content
    private val keys = strings(raw, "keys").map { it.lowercase() }.toSet()
    private val suffixes = strings(raw, "key_suffixes")
    private val parts = strings(raw, "key_parts_string_only")
    private val values = raw["values"]!!.jsonArray.map {
        val v = it.jsonObject
        val ic = v["ignore_case"]?.jsonPrimitive?.boolean ?: false
        Rule(
            Regex(v["pattern"]!!.jsonPrimitive.content, if (ic) setOf(RegexOption.IGNORE_CASE) else emptySet()),
            v["replace"]!!.jsonPrimitive.content,
            strings(v, "needs").map { n -> if (ic) n.lowercase() else n },
            ic,
        )
    }
    private val cardsRule = raw["cards"]!!.jsonObject
    private val cards = Regex(cardsRule["pattern"]!!.jsonPrimitive.content)
    private val cardFirst = cardsRule["first_digits"]!!.jsonPrimitive.content
    private val cardMin = cardsRule["min_digits"]!!.jsonPrimitive.int
    private val cardMax = cardsRule["max_digits"]!!.jsonPrimitive.int
    private val cardReplace = cardsRule["replace"]!!.jsonPrimitive.content
    private val ipv4 = Regex(raw["ipv4"]!!.jsonObject["pattern"]!!.jsonPrimitive.content)
    private val ipv4Keep = strings(raw["ipv4"]!!.jsonObject, "keep_first_octet").toSet()
    private val ipv6 = Regex(raw["ipv6"]!!.jsonObject["pattern"]!!.jsonPrimitive.content)

    /** Whether a field named [key] is masked whole; [isString] says whether its value is a string. */
    fun keySensitive(key: String, isString: Boolean): Boolean {
        val low = key.lowercase()
        if (low in keys || suffixes.any { low.endsWith(it) }) return true
        return isString && parts.any { low.contains(it) }
    }

    /** [text] with the shared value rules applied: credentials, emails, home folders, cards, IPs. */
    fun scrub(text: String): String {
        if (text.isEmpty()) return text
        var out = text
        var low = out.lowercase()
        for (r in values) {
            val hay = if (r.ignoreCase) low else out
            if (r.needs.isNotEmpty() && r.needs.none { hay.contains(it) }) continue
            val next = r.regex.replace(out, r.replace)
            if (next != out) {
                out = next
                low = out.lowercase()
            }
        }
        val digits = out.count { it in '0'..'9' }
        if (digits >= cardMin) out = replaceChecked(out, cards, ::card)
        if (out.count { it == '.' } >= 3 && digits >= 4) out = replaceChecked(out, ipv4, ::v4)
        if (out.count { it == ':' } >= 3) out = replaceChecked(out, ipv6, ::v6)
        return out
    }

    /** A flat attribute map with masked keys and scrubbed strings. */
    fun attrs(a: Map<String, Any?>): Map<String, Any?> = a.mapValues { (k, v) ->
        when {
            keySensitive(k, v is String) -> keyMask
            v is String -> scrub(v)
            else -> v
        }
    }

    private fun replaceChecked(text: String, re: Regex, fn: (String, Char?, Char?) -> String): String =
        re.replace(text) { m ->
            val s = m.range.first
            val e = m.range.last + 1
            fn(m.value, if (s > 0) text[s - 1] else null, if (e < text.length) text[e] else null)
        }

    private fun Char?.digit() = this != null && this in '0'..'9'
    private fun Char?.hex() = this != null && (this in '0'..'9' || this in 'a'..'f' || this in 'A'..'F')

    private fun card(m: String, before: Char?, after: Char?): String {
        if (before.digit() || after.digit()) return m
        val d = m.filter { it in '0'..'9' }.map { it - '0' }
        if (d.size < cardMin || d.size > cardMax || !cardFirst.contains(('0' + d[0]))) return m
        var sum = 0
        d.reversed().forEachIndexed { i, n ->
            var x = n
            if (i % 2 == 1) {
                x *= 2
                if (x > 9) x -= 9
            }
            sum += x
        }
        return if (sum % 10 == 0) cardReplace else m
    }

    private fun v4(m: String, before: Char?, after: Char?): String {
        // part of a longer number (a version like 1.2.3.4.5 is cut from its start only)
        if (before.digit() || before == '.' || after.digit()) return m
        val p = m.split('.')
        if (p.any { it.toInt() > 255 } || p[0] in ipv4Keep) return m
        return "${p[0]}.${p[1]}.${p[2]}.0"
    }

    internal fun ipv6Groups(m: String): List<String>? {
        if (m.contains(":::") || m.split("::").size > 2) return null
        val at = m.indexOf("::")
        val groups = if (at >= 0) {
            val l = m.substring(0, at)
            val r = m.substring(at + 2)
            val left = if (l.isEmpty()) emptyList() else l.split(':')
            val right = if (r.isEmpty()) emptyList() else r.split(':')
            if (left.size + right.size > 7) return null
            left + List(8 - left.size - right.size) { "0" } + right
        } else {
            m.split(':').takeIf { it.size == 8 } ?: return null
        }
        return if (groups.any { it.isEmpty() }) null else groups
    }

    private fun v6(m: String, before: Char?, after: Char?): String {
        if (before.hex() || before == ':' || after.hex() || after == ':') return m
        val g = ipv6Groups(m) ?: return m
        if (g.take(7).all { it.toInt(16) == 0 } && g[7].toInt(16) == 1) return m
        return "${g[0]}:${g[1]}:${g[2]}::"
    }
}
