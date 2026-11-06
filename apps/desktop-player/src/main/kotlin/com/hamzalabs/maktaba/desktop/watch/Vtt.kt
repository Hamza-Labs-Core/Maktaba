package com.hamzalabs.maktaba.desktop.watch

/** One subtitle cue: its text from [startMs] up to [endMs]. */
data class Cue(val startMs: Long, val endMs: Long, val text: String)

/**
 * A WebVTT reader for the subtitles the desktop Player draws itself (story 55.08): cue times,
 * settings after the times ignored, styling tags (`<b>`, `<c.x>`, `<v Name>`) taken out, and the
 * segment's `X-TIMESTAMP-MAP` honoured so segmented subtitles line up across segments.
 */
object Vtt {
    private val time = Regex("""(?:(\d+):)?(\d{1,2}):(\d{2})[.,](\d{3})""")
    private val tag = Regex("<[^>]+>")

    /** "01:02:03.456" or "02:03.456" in milliseconds, or null. */
    fun millis(s: String): Long? {
        val m = time.find(s.trim()) ?: return null
        val (h, mi, se, ms) = m.destructured
        return ((h.ifEmpty { "0" }.toLong() * 60 + mi.toLong()) * 60 + se.toLong()) * 1000 + ms.toLong()
    }

    /**
     * The shift of one file's cues from its `X-TIMESTAMP-MAP=MPEGTS:<90 kHz>,LOCAL:<time>`:
     * MPEGTS − LOCAL, in milliseconds (0 without one).
     */
    fun timestampShift(text: String): Long {
        val line = text.lineSequence().firstOrNull { it.startsWith("X-TIMESTAMP-MAP") } ?: return 0
        val mpegts = Regex("""MPEGTS:(\d+)""").find(line)?.groupValues?.get(1)?.toLongOrNull() ?: return 0
        val local = Regex("""LOCAL:([0-9:.,]+)""").find(line)?.groupValues?.get(1)?.let(::millis) ?: 0
        return mpegts / 90 - local
    }

    /** The cues of one file, each shifted by [shiftMs]. */
    fun parse(text: String, shiftMs: Long = 0): List<Cue> {
        val lines = text.replace("\r\n", "\n").replace('\r', '\n').split('\n')
        val out = mutableListOf<Cue>()
        var i = 0
        while (i < lines.size) {
            val line = lines[i]
            if ("-->" in line) {
                val start = millis(line.substringBefore("-->"))
                val end = millis(line.substringAfter("-->").trim().substringBefore(' '))
                val body = StringBuilder()
                i++
                while (i < lines.size && lines[i].isNotBlank()) {
                    if (body.isNotEmpty()) body.append('\n')
                    body.append(clean(lines[i]))
                    i++
                }
                if (start != null && end != null && end > start && body.isNotBlank()) {
                    out += Cue(start + shiftMs, end + shiftMs, body.toString())
                }
            } else {
                i++
            }
        }
        return out
    }

    /**
     * Joins the files of a segmented subtitle track: each file's map is read against the first
     * one's, so the track starts where its first file says.
     */
    fun join(files: List<String>): List<Cue> {
        if (files.isEmpty()) return emptyList()
        val base = timestampShift(files.first())
        return files.flatMap { parse(it, timestampShift(it) - base) }.distinct().sortedBy { it.startMs }
    }

    private fun clean(s: String): String = tag.replace(s, "")
        .replace("&amp;", "&").replace("&lt;", "<").replace("&gt;", ">").replace("&nbsp;", " ").replace("&lrm;", "").replace("&rlm;", "")
}

/** The text on screen at [ms]: every cue showing then, top to bottom (overlapping cues both show). */
fun List<Cue>.at(ms: Long): String? = filter { ms >= it.startMs && ms < it.endMs }.takeIf { it.isNotEmpty() }?.joinToString("\n") { it.text }
