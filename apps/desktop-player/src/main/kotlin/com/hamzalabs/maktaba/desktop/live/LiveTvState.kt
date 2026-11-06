package com.hamzalabs.maktaba.desktop.live

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.GuideAnswer
import com.hamzalabs.maktaba.core.api.LiveApi
import com.hamzalabs.maktaba.core.api.LiveChannel
import com.hamzalabs.maktaba.core.api.NowAnswer
import com.hamzalabs.maktaba.core.api.NowEntry
import com.hamzalabs.maktaba.core.live.GuideWindow
import kotlinx.coroutines.CancellationException
import java.time.Instant

/** How many hours Earlier and Later move the guide (story 55.07). */
const val GUIDE_STEP_HOURS = 2L

/** Live TV's tabs. */
enum class LiveTab { Now, Guide }

/**
 * Live TV on the desktop (story 55.07), kept by the signed-in home so Now, the guide and a
 * channel share one answer: what is on now (`GET /api/channels/now`) and the guide's window
 * (`GET /api/channels/guide`). A guide answer for a window the person has moved away from is
 * dropped. Every time shown is the home's ([NowAnswer.serverNow]).
 */
class LiveTvState(val api: LiveApi, private val signedOut: () -> Unit = {}) {
    var tab by mutableStateOf(LiveTab.Now)
    var now by mutableStateOf<NowAnswer?>(null)
        private set
    private var nowAnsweredAt = 0L
    var nowProblem by mutableStateOf<Throwable?>(null)
        private set

    var guide by mutableStateOf<GuideAnswer?>(null)
        private set
    /** The window [guide] answers for. */
    var guideWindow by mutableStateOf<GuideWindow?>(null)
        private set
    var guideProblem by mutableStateOf<Throwable?>(null)
        private set
    /** Windows of [GUIDE_STEP_HOURS] the guide is moved, Earlier (negative) or Later. */
    var guideShift by mutableIntStateOf(0)

    private var guideAsked = 0

    /** The channels in number order: the channel player's up and down walk them. */
    val lineup: List<LiveChannel> get() = now?.channels.orEmpty().map { it.channel }.sortedBy { it.number }

    fun entry(channelId: String): NowEntry? = now?.channels?.firstOrNull { it.channel.id == channelId }

    suspend fun loadNow(localMs: Long = System.currentTimeMillis()) {
        try {
            val a = api.now()
            now = a
            nowAnsweredAt = localMs
            nowProblem = null
        } catch (e: CancellationException) {
            throw e
        } catch (e: ApiException.SignedOut) {
            signedOut()
        } catch (e: Exception) {
            nowProblem = e
        }
    }

    /** Asks for [window]; an answer for a window asked for before a newer one is dropped. */
    suspend fun loadGuide(window: GuideWindow) {
        val mine = ++guideAsked
        try {
            val a = api.guide(Instant.ofEpochMilli(window.startMs), Instant.ofEpochMilli(window.endMs))
            if (mine != guideAsked) return
            guide = a
            guideWindow = window
            guideProblem = null
        } catch (e: CancellationException) {
            throw e
        } catch (e: ApiException.SignedOut) {
            signedOut()
        } catch (e: Exception) {
            if (mine == guideAsked) guideProblem = e
        }
    }

    /** The home's time at this computer's [localMs]. */
    fun serverNow(localMs: Long = System.currentTimeMillis()): Long = now?.serverNow(localMs, nowAnsweredAt) ?: localMs
}

/** The channel [by] steps from [currentId] in [lineup] (by number), wrapping; null in an empty lineup (as Android). */
fun neighbour(lineup: List<LiveChannel>, currentId: String?, by: Int): LiveChannel? {
    if (lineup.isEmpty()) return null
    val sorted = lineup.sortedBy { it.number }
    val at = sorted.indexOfFirst { it.id == currentId }
    if (at < 0) return sorted.first()
    return sorted[Math.floorMod(at + by, sorted.size)]
}
