package com.hamzalabs.maktaba.tv.ui.screens.library

import androidx.compose.runtime.staticCompositionLocalOf
import com.hamzalabs.maktaba.core.queue.QueueSource
import com.hamzalabs.maktaba.tv.data.TvCollection

/**
 * What the library view's screens open and play (Epic 56): SignedInApp gives them, so a
 * screen never knows the stack or the queue.
 */
data class TvNav(
    val openVideo: (String) -> Unit = {},
    val openSeries: (String) -> Unit = {},
    val openSeason: (seriesId: String, season: Int) -> Unit = { _, _ -> },
    val openCollection: (library: String, collection: TvCollection) -> Unit = { _, _ -> },
    val openPlaylist: (String) -> Unit = {},
    /** Makes a server queue and plays its first item; the caller shows a failure (story 56.02). */
    val play: (source: QueueSource, shuffle: Boolean, startVideoId: String?) -> Unit = { _, _, _ -> },
    /** A card's long press: Play next, Add to queue, Add to playlist, Go to series. */
    val cardMenu: (videoId: String, seriesId: String?) -> Unit = { _, _ -> },
    val signedOut: () -> Unit = {},
)

val LocalTvNav = staticCompositionLocalOf { TvNav() }
