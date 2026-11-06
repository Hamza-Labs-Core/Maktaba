package com.hamzalabs.maktaba.core.playback

import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.Episode
import com.hamzalabs.maktaba.core.api.SeriesEpisodes
import com.hamzalabs.maktaba.core.api.Video
import com.hamzalabs.maktaba.core.api.myVideo
import com.hamzalabs.maktaba.core.api.seriesEpisodes
import kotlinx.coroutines.CancellationException

// Up next (FR-33), the rule both Players follow (Epic 55, story 55.08): what plays after a video,
// and when the card shows.

/** Seconds Up next counts down before the next episode plays (FR-33, as the web's). */
const val UP_NEXT_SEC = 10

/** The episode after [videoId] in the series' order (its seasons, then their episodes), or null. */
fun nextEpisode(episodes: SeriesEpisodes, videoId: String): Episode? {
    val all = episodes.seasons.flatMap { it.episodes }
    val at = all.indexOfFirst { it.videoId == videoId }
    return if (at >= 0) all.getOrNull(at + 1) else null
}

/**
 * The video after [videoId] (FR-33): the next episode of its series (`/api/me/videos/{id}`
 * names the series, `/api/series/{id}/episodes` its order), or null when it is in none, is the
 * last, or the home does not answer (Up next is only a nicety).
 */
suspend fun ApiClient.nextVideo(videoId: String): Video? = try {
    val series = myVideo(videoId).series
    val next = series?.let { nextEpisode(seriesEpisodes(it.id), videoId) }
    next?.let { e ->
        runCatching { video(e.videoId) }.getOrNull()
            ?: Video(e.videoId, title = e.title.takeIf { it.isNotBlank() }, durationSec = e.durationSec)
    }
} catch (e: CancellationException) {
    throw e
} catch (_: Exception) {
    null
}

/** Whether Up next is due: the last [leadSec] seconds of a video of known length, or its end. */
fun upNextDue(positionMs: Long, durationMs: Long, ended: Boolean, leadSec: Int = UP_NEXT_SEC): Boolean =
    ended || (durationMs > 0 && positionMs > 0 && durationMs - positionMs <= leadSec * 1000L)
