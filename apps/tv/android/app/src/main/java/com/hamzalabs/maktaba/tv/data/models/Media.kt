package com.hamzalabs.maktaba.tv.data.models

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/** A playable item: enough to render a poster and start playback. */
@Serializable
data class Media(
    val id: String,
    val title: String,
    @SerialName("poster_url") val posterUrl: String? = null,
    @SerialName("duration_sec") val durationSec: Double? = null,
    @SerialName("hls_url") val hlsUrl: String? = null,
)

/** A Home-screen rail (Continue Watching / Recently Added / recs). */
@Serializable
data class MediaRail(
    val id: String,
    val title: String,
    @SerialName("reason_kind") val reasonKind: String? = null,
    val items: List<RailItem> = emptyList(),
)

/** One entry inside a rail; carries resume position when present. */
@Serializable
data class RailItem(
    @SerialName("video_id") val videoId: String,
    val title: String = "",
    @SerialName("poster_url") val posterUrl: String? = null,
    @SerialName("position_sec") val positionSec: Double? = null,
    @SerialName("duration_sec") val durationSec: Double? = null,
    @SerialName("remaining_sec") val remainingSec: Double? = null,
) {
    /** Fraction watched in `0f..1f`, for the progress bar under a card. */
    val progress: Float
        get() {
            val pos = positionSec ?: return 0f
            val dur = durationSec ?: return 0f
            if (dur <= 0) return 0f
            return (pos / dur).coerceIn(0.0, 1.0).toFloat()
        }

    fun asMedia() = Media(id = videoId, title = title, posterUrl = posterUrl, durationSec = durationSec)
}

/** Top-level payload of `GET /api/recommendations`. */
@Serializable
data class Recommendations(
    val rails: List<MediaRail> = emptyList(),
)
