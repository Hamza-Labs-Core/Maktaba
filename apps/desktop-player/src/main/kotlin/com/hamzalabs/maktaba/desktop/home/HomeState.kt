package com.hamzalabs.maktaba.desktop.home

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.HomeRails
import com.hamzalabs.maktaba.core.api.NotAvailable
import com.hamzalabs.maktaba.core.api.NowAnswer
import com.hamzalabs.maktaba.core.api.Recommendations
import com.hamzalabs.maktaba.core.api.VideoCard
import com.hamzalabs.maktaba.core.api.dismissRecommendation
import com.hamzalabs.maktaba.core.api.homeRails
import com.hamzalabs.maktaba.core.api.recommendations
import com.hamzalabs.maktaba.core.api.videoDetail
import com.hamzalabs.maktaba.core.browse.HomeRow
import com.hamzalabs.maktaba.core.browse.KEY_LIVE
import com.hamzalabs.maktaba.core.browse.homeRows
import com.hamzalabs.maktaba.core.browse.isEmptyHome
import com.hamzalabs.maktaba.core.session.ReadOrder
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.async
import kotlinx.coroutines.coroutineScope

/** How long Home's rows stay fresh: shown again after this, it reads them again. */
const val HOME_FRESH_MS = 5 * 60_000L

/**
 * Home's data on the desktop (story 55.03), as the Android `HomeState`: `GET /api/home`, the
 * recommendations (`desktop-home`, only when `recommendations` is on) and Live TV on now (only
 * when `live_tv` is on), read together and built into rows by `:client`'s [homeRows]. The last
 * good rows stay when a refresh fails ([stale]). Kept by the signed-in home, so another section
 * and back finds it; shown again after [HOME_FRESH_MS] it reads again.
 */
class HomeState(
    private val client: ApiClient,
    /** Whether recommendations are on now (story 55.02). */
    private val recommendationsOn: () -> Boolean,
    /** `GET /api/channels/now`, or null when Live TV is off for this person. */
    private val liveNow: suspend () -> NowAnswer?,
    private val signedOut: () -> Unit,
    private val now: () -> Long = System::currentTimeMillis,
) {
    var rails by mutableStateOf<HomeRails?>(null)
        private set
    var recs by mutableStateOf<Recommendations?>(null)
        private set
    var live by mutableStateOf<NowAnswer?>(null)
        private set

    /** Videos read for recommendation cards, by id. */
    val known = mutableStateMapOf<String, VideoCard>()

    /** Why Home could not load, when there is nothing to show instead. */
    var problem by mutableStateOf<Throwable?>(null)
        private set

    /** A refresh failed: the rows shown are the last ones. */
    var stale by mutableStateOf(false)
        private set

    var loading by mutableStateOf(false)
        private set

    /** Not interested could not reach the home: the card is back, and the page says so. */
    var dismissFailed by mutableStateOf(false)

    /** The order of the recommendations' reads and Not interested. */
    private val recsOrder = ReadOrder()
    private var answeredAt = 0L
    private val asking = mutableSetOf<String>()

    /** The rows under the hero, in the Android order, empty rows left out. */
    val rows: List<HomeRow>
        get() = rails?.let { homeRows(it, if (recommendationsOn()) recs else null, live, known) }.orEmpty()

    /** Home answered and has nothing at all. */
    val empty: Boolean get() = rails?.let { isEmptyHome(it, rows) } == true

    /** Home is shown: read when there is nothing yet, or when the rows are older than [HOME_FRESH_MS]. */
    suspend fun open() {
        if (rails == null || now() - answeredAt >= HOME_FRESH_MS) load()
    }

    /** Reads Home, its recommendations and Live TV on now together. */
    suspend fun load() {
        loading = true
        val ticket = recsOrder.begin()
        try {
            coroutineScope {
                val r = async { if (recommendationsOn()) runCatching { client.recommendations("desktop-home") } else null }
                val n = async { runCatching { liveNow() } }
                val h = try {
                    client.homeRails()
                } catch (e: CancellationException) {
                    throw e
                } catch (e: ApiException.SignedOut) {
                    signedOut()
                    return@coroutineScope
                } catch (e: Exception) {
                    if (rails == null) problem = e else stale = true
                    return@coroutineScope
                }
                rails = h
                problem = null
                stale = false
                answeredAt = now()
                // a read that began before a Not interested still has the card: dropped
                r.await()?.onSuccess { if (recsOrder.fresh(ticket)) recs = it }?.onFailure { extraFailed(it) }
                n.await().onSuccess { live = it }.onFailure { extraFailed(it); live = null }
            }
        } finally {
            loading = false
        }
    }

    private fun extraFailed(e: Throwable) {
        if (e is CancellationException) throw e
        if (e is ApiException.SignedOut) signedOut()
    }

    /** Reads [videoId] for a recommendation's card; a video the person may no longer see leaves the rows. */
    suspend fun resolve(videoId: String) {
        if (videoId in known || !asking.add(videoId)) return
        try {
            val v = client.videoDetail(videoId)
            known[videoId] = VideoCard(id = v.id, title = v.displayTitle, filename = v.filename, libraryId = v.libraryId, durationSec = v.durationSec)
        } catch (e: CancellationException) {
            throw e
        } catch (e: NotAvailable) {
            drop(videoId)
        } catch (e: ApiException.SignedOut) {
            signedOut()
        } catch (_: Exception) {
            // the card keeps its placeholder title
        } finally {
            asking -= videoId
        }
    }

    /**
     * Not interested: the card leaves at once and the home is told; a read that began before it
     * does not bring it back. When the home cannot be told, the card comes back and
     * [dismissFailed] says so. True when the home took it.
     */
    suspend fun dismiss(videoId: String): Boolean {
        val before = recs
        drop(videoId)
        dismissFailed = false
        return try {
            recsOrder.saving { client.dismissRecommendation(videoId) }
            true
        } catch (e: CancellationException) {
            putBack(videoId, before)
            throw e
        } catch (e: ApiException.SignedOut) {
            signedOut()
            false
        } catch (_: Exception) {
            putBack(videoId, before)
            dismissFailed = true
            false
        }
    }

    private fun putBack(videoId: String, before: Recommendations?) {
        val now = recs ?: return
        val was = before?.rails.orEmpty().firstOrNull { rail -> rail.items.any { it.videoId == videoId } } ?: return
        val at = was.items.indexOfFirst { it.videoId == videoId }
        recs = now.copy(
            rails = now.rails.map { rail ->
                if (rail.id != was.id || rail.items.any { it.videoId == videoId }) rail
                else rail.copy(items = rail.items.toMutableList().apply { add(at.coerceIn(0, size), was.items[at]) })
            },
        )
    }

    private fun drop(videoId: String) {
        val r = recs ?: return
        recs = r.copy(rails = r.rails.map { rail -> rail.copy(items = rail.items.filterNot { it.videoId == videoId }) })
    }

    /** The rows without What's On Now (Live TV went off for this person). */
    fun rowsWithoutLive(): List<HomeRow> = rows.filter { it.key != KEY_LIVE }
}
