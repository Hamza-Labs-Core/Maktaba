package com.hamzalabs.maktaba.desktop.watch

import com.hamzalabs.maktaba.core.api.VideoSubtitle
import com.hamzalabs.maktaba.core.playback.SubtitleColour
import com.hamzalabs.maktaba.core.playback.SubtitleSize

/** mpv's codecs of picture subtitles (Blu-ray's PGS, DVD's and DVB's bitmaps): drawn as authored. */
val PICTURE_SUBTITLE_CODECS = setOf("hdmv_pgs_subtitle", "dvd_subtitle", "dvb_subtitle")

/** A subtitle track of the file itself, from mpv's `track-list` (story 13.20). */
data class FileSubtitle(
    /** mpv's id (`sid`). */
    val id: String,
    val title: String?,
    val language: String?,
    val codec: String?,
    /** Flagged forced: mpv shows it itself, with the audio of its language, when subtitles are off. */
    val forced: Boolean = false,
) {
    val picture: Boolean get() = codec in PICTURE_SUBTITLE_CODECS
}

/**
 * A subtitle the Subtitles menu offers. [Kind.Stream]: an HLS rendition of the master playlist,
 * whose cues the Player draws ([rendition]). [Kind.Home]: a subtitle the home keeps (a sidecar
 * file or its transcription), which libmpv adds from [url] (the stream proxy's). [Kind.File]: a
 * track of the file itself, libmpv's [trackId]. Story 13.20.
 */
data class SubtitleOption(
    val name: String?,
    val language: String?,
    val kind: Kind,
    val rendition: Rendition? = null,
    val url: String? = null,
    val trackId: String? = null,
    /** A picture track (PGS, DVD): drawn as authored, neither size nor colour. */
    val picture: Boolean = false,
    /** The home's subtitle source (`external`, `auto`): its [url] is the session's, made again for a new one. */
    val source: String? = null,
) {
    enum class Kind { Stream, Home, File }

    companion object {
        /** An HLS subtitle rendition, as the menu offers it. */
        fun of(r: Rendition) = SubtitleOption(r.name.ifBlank { null }, r.language, Kind.Stream, rendition = r)
    }
}

/** The size and colour a video's subtitles are drawn in, taken from the settings when it opens. */
data class SubtitleStyle(val size: SubtitleSize = SubtitleSize.Medium, val colour: SubtitleColour = SubtitleColour.White) {
    val scale: Float get() = size.scale
}

/** The home's subtitle sources a session serves: a sidecar file and Maktaba's transcription. */
/**
 * The home's subtitle sources a session serves (`subtitle_files.source`): a file beside the
 * video (`external`) and Maktaba's own transcription (`generated`), both at the session's
 * `subs/sidecar/{language}.vtt` (the file beside the video first, else the stored copy).
 */
val HOME_SUBTITLE_SOURCES = setOf("external", "generated")

/**
 * Direct play's Subtitles menu (story 13.20): the home's subtitles first (in the home's order,
 * each at [url]; a row with no language, or whose address cannot be made, is left out, and rows
 * at one address, such as one language's SRT and VTT or its file and transcription, are one
 * entry), then the file's own tracks in the file's order. The home's `embedded` rows are the
 * file's tracks again, read here from the file itself; a forced track is not a choice.
 */
fun directSubtitles(home: List<VideoSubtitle>, file: List<FileSubtitle>, url: (VideoSubtitle) -> String?): List<SubtitleOption> {
    val fromHome = home.filter { it.source in HOME_SUBTITLE_SOURCES && it.language.isNotBlank() }.mapNotNull { s ->
        url(s)?.let { SubtitleOption(null, s.language, SubtitleOption.Kind.Home, url = it, source = s.source) }
    }.distinctBy { it.url }
    val fromFile = file.filter { !it.forced }.map { t ->
        SubtitleOption(t.title?.ifBlank { null }, t.language?.ifBlank { null }, SubtitleOption.Kind.File, trackId = t.id, picture = t.picture)
    }
    return fromHome + fromFile
}
