package com.hamzalabs.maktaba.desktop.watch

// What the desktop Player reads of an HLS master playlist (story 55.08, README §6): JavaFX plays
// a stream's default audio only and draws no subtitles, so the Player lists the session's
// renditions itself, rewrites the master so the chosen audio is the only one, and draws the
// chosen subtitles over the picture.

/** An `EXT-X-MEDIA` rendition: an audio track or a subtitle track of the stream. */
data class Rendition(
    /** "AUDIO" or "SUBTITLES". */
    val type: String,
    val groupId: String,
    val name: String,
    val language: String?,
    val default: Boolean,
    /** Its playlist, as written (relative to the master); null for audio muxed into the video. */
    val uri: String?,
)

/** The renditions a master playlist offers, in its order. */
data class Master(val audio: List<Rendition>, val subtitles: List<Rendition>) {
    companion object {
        val None = Master(emptyList(), emptyList())
    }
}

/** Whether [text] is a master playlist (it names variant streams). */
fun isMaster(text: String): Boolean = text.lineSequence().any { it.startsWith("#EXT-X-STREAM-INF") }

/** The attributes of a tag line (`#EXT-X-MEDIA:TYPE=AUDIO,NAME="English",…`), quoted values unquoted. */
fun attributes(line: String): Map<String, String> {
    val body = line.substringAfter(':', "")
    val out = LinkedHashMap<String, String>()
    var i = 0
    while (i < body.length) {
        val eq = body.indexOf('=', i)
        if (eq < 0) break
        val key = body.substring(i, eq).trim()
        var j = eq + 1
        val value: String
        if (j < body.length && body[j] == '"') {
            val end = body.indexOf('"', j + 1).let { if (it < 0) body.length else it }
            value = body.substring(j + 1, end)
            j = end + 1
        } else {
            val end = body.indexOf(',', j).let { if (it < 0) body.length else it }
            value = body.substring(j, end)
            j = end
        }
        out[key] = value
        i = if (j < body.length && body[j] == ',') j + 1 else j
    }
    return out
}

/** The audio and subtitle renditions of a master playlist. */
fun parseMaster(text: String): Master {
    val media = text.lineSequence().filter { it.startsWith("#EXT-X-MEDIA:") }.map { line ->
        val a = attributes(line)
        Rendition(
            type = a["TYPE"].orEmpty(),
            groupId = a["GROUP-ID"].orEmpty(),
            name = a["NAME"].orEmpty(),
            language = a["LANGUAGE"]?.takeIf { it.isNotBlank() },
            default = a["DEFAULT"].equals("YES", ignoreCase = true),
            uri = a["URI"],
        )
    }.toList()
    return Master(media.filter { it.type == "AUDIO" }, media.filter { it.type == "SUBTITLES" })
}

/**
 * The master playlist JavaFX is given: with [audio] (a rendition's NAME) the only audio
 * rendition of its group, `DEFAULT=YES` and `AUTOSELECT=YES`; with no choice, as the home wrote
 * it. Subtitle renditions are always left out (JavaFX draws none; the Player draws them).
 */
fun rewriteMaster(text: String, audio: String?): String {
    val m = parseMaster(text)
    val chosen = audio?.let { a -> m.audio.firstOrNull { it.name == a } }
    return text.lineSequence().mapNotNull { line ->
        when {
            line.startsWith("#EXT-X-MEDIA:") -> {
                val a = attributes(line)
                when {
                    a["TYPE"] == "SUBTITLES" -> null
                    a["TYPE"] == "AUDIO" && chosen != null && a["GROUP-ID"] == chosen.groupId ->
                        if (a["NAME"] == chosen.name) setDefault(line) else null
                    else -> line
                }
            }
            line.startsWith("#EXT-X-STREAM-INF") -> line.replace(Regex(""",SUBTITLES="[^"]*""""), "")
            else -> line
        }
    }.joinToString("\n")
}

private fun setDefault(line: String): String {
    var s = line
    s = if (Regex("DEFAULT=(YES|NO)").containsMatchIn(s)) s.replace(Regex("DEFAULT=(YES|NO)"), "DEFAULT=YES") else "$s,DEFAULT=YES"
    s = if (Regex("AUTOSELECT=(YES|NO)").containsMatchIn(s)) s.replace(Regex("AUTOSELECT=(YES|NO)"), "AUTOSELECT=YES") else "$s,AUTOSELECT=YES"
    return s
}

/** The media lines (segment URIs) of a media playlist, in order. */
fun segmentUris(playlist: String): List<String> =
    playlist.lineSequence().map { it.trim() }.filter { it.isNotEmpty() && !it.startsWith("#") }.toList()
