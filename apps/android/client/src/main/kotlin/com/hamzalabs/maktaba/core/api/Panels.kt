package com.hamzalabs.maktaba.core.api

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.doubleOrNull

// Plugin panels (Epic 54, story 54.20): a plugin's read-only view of a video, series or library,
// declared as data (ui@1). The home checked every value when the plugin wrote it; the apps show
// them as plain text again (PanelText) and never make a request from one.

@Serializable
data class PanelBlock(
    val type: String,
    val label: String? = null,
    val columns: List<String> = emptyList(),
    val value: JsonElement? = null,
    val image: String? = null,
)

@Serializable
data class PanelView(
    @SerialName("plugin_id") val pluginId: String,
    val plugin: String = "",
    val title: String = "",
    val blocks: List<PanelBlock> = emptyList(),
)

@Serializable
internal data class PanelsDoc(val panels: List<PanelView> = emptyList())

/**
 * `GET /api/panels?subject=<kind>:<id>&lang=`. Asked only while [Capabilities.panels]; a 404 (an
 * older home, or a subject the person cannot see) is no panels. At most three.
 */
suspend fun ApiClient.panels(kind: String, id: String, lang: String): List<PanelView> = try {
    get<PanelsDoc>("/api/panels", mapOf("subject" to "$kind:$id", "lang" to lang)).panels.take(3)
} catch (e: ApiException.Status) {
    if (e.code == 404) emptyList() else throw e
}

/** How a panel's values become text. */
object PanelText {
    /** The longest text one cell, chip or row shows. */
    const val MAX_SHOWN = 200

    private fun hidden(c: Int): Boolean =
        c < 0x20 || c in 0x7F..0x9F || c == 0x200E || c == 0x200F || c in 0x202A..0x202E || c in 0x2066..0x2069 || c == 0xFEFF

    /** A string or number as plain text: control and direction characters removed, cut with an ellipsis. */
    fun plain(v: JsonElement?, max: Int = MAX_SHOWN): String {
        val p = v as? JsonPrimitive ?: return ""
        val s = when {
            p.isString -> p.content
            p.doubleOrNull?.isFinite() == true -> p.content
            else -> return ""
        }
        return plain(s, max)
    }

    fun plain(s: String?, max: Int = MAX_SHOWN): String {
        if (s == null) return ""
        val kept = buildString { s.codePoints().forEach { if (!hidden(it)) appendCodePoint(it) } }
        val n = kept.codePointCount(0, kept.length)
        return if (n > max) kept.substring(0, kept.offsetByCodePoints(0, max - 1)) + "…" else kept
    }

    /** The home's own panel image route only; never a URL a plugin wrote. */
    fun safeImage(s: String?): String? =
        s?.takeIf { Regex("^/api/panels/image\\?[A-Za-z0-9_.:=&%-]*$").matches(it) }

    fun list(v: JsonElement?): List<JsonElement> = (v as? JsonArray).orEmpty()
    fun field(v: JsonElement?, k: String): JsonElement? = (v as? JsonObject)?.get(k)
    fun number(v: JsonElement?): Double? = (v as? JsonPrimitive)?.takeIf { !it.isString }?.doubleOrNull

    /** "1:01" or "1:02:03". */
    fun clock(ms: Double): String {
        val s = (ms / 1000).toLong()
        val h = s / 3600
        val m = (s % 3600) / 60
        val sec = s % 60
        return if (h > 0) "%d:%02d:%02d".format(h, m, sec) else "%d:%02d".format(m, sec)
    }

    /** The blocks a screen draws: the TV shows kv, badges and markers only. */
    fun shown(blocks: List<PanelBlock>, tv: Boolean): List<PanelBlock> =
        if (tv) blocks.filter { it.type in setOf("kv", "badges", "markers") } else blocks
}
