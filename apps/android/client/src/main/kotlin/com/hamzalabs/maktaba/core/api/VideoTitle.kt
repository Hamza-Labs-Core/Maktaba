package com.hamzalabs.maktaba.core.api

/**
 * The title a video shows when it has no metadata title: its file name without
 * the final extension. The API applies the same rule (Go package videotitle)
 * wherever it picks the title itself; this is for the places the app falls
 * back to `filename` on its own.
 */
object VideoTitle {
    /** A final "." and one or more ASCII letters or digits, after a non-empty stem. */
    private val EXT = Regex("^(.+)\\.[A-Za-z0-9]+$", RegexOption.DOT_MATCHES_ALL)

    /** [filename] without its final extension: "a.b.mkv" -> "a.b"; "notes", ".mp4" and "x." stay. */
    fun stripExt(filename: String): String = EXT.matchEntire(filename)?.groupValues?.get(1) ?: filename

    /** [title] when it is not blank, else [filename] without its extension. */
    fun of(title: String?, filename: String): String =
        title?.takeIf { it.isNotBlank() } ?: stripExt(filename)
}
