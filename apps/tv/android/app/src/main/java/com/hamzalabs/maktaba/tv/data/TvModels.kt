package com.hamzalabs.maktaba.tv.data

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/**
 * A video as the TV shows it: `GET /api/videos` and `GET /api/videos/{id}`, with the name a
 * person reads (`display_title`, else the title, else the file's name) and, on the detail, where
 * this person stopped (`playback_state`).
 */
@Serializable
data class TvVideo(
    val id: String,
    @SerialName("library_id") val libraryId: String = "",
    val filename: String = "",
    val title: String? = null,
    @SerialName("display_title") val displayTitleRaw: String? = null,
    val description: String? = null,
    @SerialName("duration_sec") val durationSec: Double? = null,
    @SerialName("created_at") val createdAt: String? = null,
    @SerialName("playback_state") val playback: PlaybackState? = null,
) {
    val displayTitle: String
        get() = displayTitleRaw?.takeIf { it.isNotBlank() } ?: title?.takeIf { it.isNotBlank() } ?: filename

    /** Where to pick up, in seconds: null when not started, finished, or too near either end. */
    val resumeSec: Double?
        get() = resumeAt(playback?.positionSec, durationSec, playback?.completed == true)
}

@Serializable
data class PlaybackState(
    @SerialName("position_sec") val positionSec: Double = 0.0,
    val completed: Boolean = false,
)

/** `GET /api/recommendations`: the rails (Continue Watching first). Items carry no title. */
@Serializable
data class Recommendations(val rails: List<Rail> = emptyList())

@Serializable
data class Rail(
    val id: String,
    val title: String = "",
    @SerialName("reason_kind") val reasonKind: String = "",
    val items: List<RailItem> = emptyList(),
)

@Serializable
data class RailItem(
    @SerialName("video_id") val videoId: String,
    @SerialName("position_sec") val positionSec: Double? = null,
    @SerialName("duration_sec") val durationSec: Double? = null,
) {
    /** Fraction watched in 0..1, for the bar under a card. */
    val progress: Float
        get() {
            val pos = positionSec ?: return 0f
            val dur = durationSec ?: return 0f
            if (dur <= 0) return 0f
            return (pos / dur).coerceIn(0.0, 1.0).toFloat()
        }
}

/**
 * Where to resume, in seconds: past the first 10 s, more than 30 s before the end, and not
 * finished; else null (start at the beginning).
 */
fun resumeAt(positionSec: Double?, durationSec: Double?, completed: Boolean): Double? {
    val pos = positionSec ?: return null
    if (completed || pos < 10) return null
    val dur = durationSec
    if (dur != null && dur > 0 && dur - pos < 30) return null
    return pos
}
