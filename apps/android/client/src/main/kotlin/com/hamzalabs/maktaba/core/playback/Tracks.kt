package com.hamzalabs.maktaba.core.playback

import com.hamzalabs.maktaba.core.api.Rendition

// The track and quality rules both Players follow (FR-34, FR-35; Epic 55, story 55.08). The
// Android Player applies them to Media3's track selection, the desktop to its HLS renditions.

/**
 * The audio and subtitle choice a person made, kept for the next video (FR-34: "a choice is
 * kept for the next video in the series"): by language, since the next video's tracks are
 * other tracks. Null fields: no choice, the player's default.
 */
data class TrackChoice(
    val audioLanguage: String? = null,
    /** True: subtitles off; false: on in [textLanguage]; null: no choice made. */
    val textOff: Boolean? = null,
    val textLanguage: String? = null,
)

/** What a new video plays with: the audio language to prefer, and subtitles on (in a language) or off. */
data class TrackPlan(val audioLanguage: String?, val textOn: Boolean, val textLanguage: String?)

/**
 * The plan for a new video: the [choice] kept from the last one, else the settings' defaults
 * (FR-31: subtitles off unless turned on, in the chosen language; the audio language set, else
 * the video's own).
 */
fun TrackChoice.plan(prefs: PlaybackValues): TrackPlan {
    val textOn = textOff?.not() ?: prefs.subtitlesOn
    return TrackPlan(
        audioLanguage = audioLanguage ?: prefs.audioLanguage.tag,
        textOn = textOn,
        textLanguage = if (textOn) textLanguage ?: prefs.subtitleLanguage.tag else null,
    )
}

/**
 * Picks the track to play from [languages] (each track's language, null when unknown, in the
 * stream's order) for [wanted]: the first in that language, else the first when nothing
 * matches (or nothing is wanted). Null when there is no track.
 */
fun pickTrack(languages: List<String?>, wanted: String?): Int? {
    if (languages.isEmpty()) return null
    if (wanted == null) return 0
    val at = languages.indexOfFirst { it != null && sameLanguage(it, wanted) }
    return if (at >= 0) at else 0
}

/** "en" and "en-GB", "ar" and "ara" are one language here. */
fun sameLanguage(a: String, b: String): Boolean {
    fun base(s: String) = s.lowercase().substringBefore('-').substringBefore('_').let { THREE[it] ?: it }
    return base(a) == base(b)
}

private val THREE = mapOf("eng" to "en", "ara" to "ar", "fra" to "fr", "fre" to "fr", "urd" to "ur", "tur" to "tr", "ind" to "id", "msa" to "ms", "may" to "ms")

/** A quality the picker offers (FR-35): Auto (no cap), or a height and the cap it sends. */
data class QualityOption(val height: Int?, val maxBitrateKbps: Int?) {
    companion object {
        val Auto = QualityOption(null, null)

        /**
         * The streaming service's own ladder (`streaming/internal/ffmpeg`): offered when a session
         * names none (direct play), so a person can still ask for less.
         */
        val Standard = listOf(Rendition(1080, 1920, 6000), Rendition(720, 1280, 3000), Rendition(480, 854, 1200))

        /** Auto, then each rung of [ladder] (else [Standard]) from the highest, one per height. */
        fun of(ladder: List<Rendition>): List<QualityOption> {
            val rungs = ladder.filter { it.height > 0 && it.bitrateKbps > 0 }.ifEmpty { Standard }
            return listOf(Auto) + rungs.sortedByDescending { it.height }.distinctBy { it.height }
                .map { QualityOption(it.height, it.bitrateKbps) }
        }
    }
}
