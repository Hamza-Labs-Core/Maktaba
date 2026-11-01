package com.hamzalabs.maktaba.player.live

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.hamzalabs.maktaba.core.api.GuideAnswer
import com.hamzalabs.maktaba.core.api.LiveApi
import com.hamzalabs.maktaba.core.api.LiveChannel
import com.hamzalabs.maktaba.core.api.NowAnswer
import com.hamzalabs.maktaba.core.api.NowEntry
import com.hamzalabs.maktaba.player.shell.PlayerHome
import kotlinx.coroutines.CancellationException
import java.time.Instant
import java.time.ZoneId
import java.time.ZonedDateTime
import java.time.temporal.ChronoUnit

/**
 * Live TV's state for one signed-in home (kept by [PlayerHome.keep], so Now, the guide and a
 * channel's player share one answer): what is on now (`GET /api/channels/now`), the guide's
 * window (`GET /api/channels/guide`), and whether this user can see any channel at all, which
 * decides whether the Live TV section is shown (FR-5).
 *
 * Every time shown is the home's ([NowAnswer.serverNow]): a phone whose clock is wrong still
 * shows the right programme and progress.
 */
class LiveTv(val api: LiveApi) {
    /** Whether this user can see a channel; null until the home has said. */
    var hasChannels by mutableStateOf<Boolean?>(null)
        private set

    var now by mutableStateOf<NowAnswer?>(null)
        private set
    private var nowAnsweredAt = 0L

    /** Why Now could not load (it keeps the last answer it had). */
    var nowProblem by mutableStateOf<Throwable?>(null)
        private set

    var guide by mutableStateOf<GuideAnswer?>(null)
        private set
    private var guideAnsweredAt = 0L

    /** The window [guide] answers for. */
    var guideWindow by mutableStateOf<GuideWindow?>(null)
        private set

    var guideProblem by mutableStateOf<Throwable?>(null)
        private set

    /** The guide's day picked (0 today, 1 tomorrow ...), kept while signed in. */
    var guideDay by mutableIntStateOf(0)

    /** How many windows Earlier (negative) or Later the guide is moved from the picked day's. */
    var guideShift by mutableIntStateOf(0)

    /** The channels in number order: the live player's ▲ / ▼ walk them. */
    val lineup: List<LiveChannel> get() = now?.channels.orEmpty().map { it.channel }.sortedBy { it.number }

    /** What is on [channelId] now and next. */
    fun entry(channelId: String): NowEntry? = now?.channels?.firstOrNull { it.channel.id == channelId }

    /** Asks `GET /api/channels/now`; [localMs] is this device's time when asked. */
    suspend fun loadNow(localMs: Long) {
        try {
            val a = api.now()
            now = a
            nowAnsweredAt = localMs
            hasChannels = a.channels.isNotEmpty()
            nowProblem = null
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            nowProblem = e
        }
    }

    /** Asks `GET /api/channels/guide` for [window]. */
    suspend fun loadGuide(window: GuideWindow, localMs: Long) {
        try {
            val a = api.guide(Instant.ofEpochMilli(window.startMs), Instant.ofEpochMilli(window.endMs))
            guide = a
            guideWindow = window
            guideAnsweredAt = localMs
            guideProblem = null
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            guideProblem = e
        }
    }

    /** The home's time at this device's [localMs], from the latest answer's clock. */
    fun serverNow(localMs: Long): Long = when {
        now != null -> now!!.serverNow(localMs, nowAnsweredAt)
        guide != null -> guide!!.serverNow(localMs, guideAnsweredAt)
        else -> localMs
    }
}

/** This home's Live TV state. */
fun PlayerHome.liveTv(): LiveTv = keep("live") { LiveTv(LiveApi(client)) }

/** A span of the guide: [startMs] up to [endMs], epoch milliseconds. */
data class GuideWindow(val startMs: Long, val endMs: Long) {
    val minutes: Long get() = (endMs - startMs) / 60_000

    /** Minutes from the window's start to [ms] (negative before it). */
    fun minutesTo(ms: Long): Float = (ms - startMs) / 60_000f

    /** The window moved by [hours] (earlier when negative). */
    fun shifted(hours: Long): GuideWindow = GuideWindow(startMs + hours * HOUR_MS, endMs + hours * HOUR_MS)

    companion object {
        const val HOUR_MS = 3_600_000L

        /** How many hours the guide asks for at once: three on a phone, six on a tablet (FR-22). */
        fun hours(wide: Boolean): Int = if (wide) 6 else 3

        /**
         * The guide's window [dayOffset] days from today at the same time of day: from the half
         * hour before [nowMs]'s half hour (so the now-line is never at the very edge), [hours] long.
         */
        fun around(nowMs: Long, zone: ZoneId, hours: Int, dayOffset: Int = 0): GuideWindow {
            val t = ZonedDateTime.ofInstant(Instant.ofEpochMilli(nowMs), zone)
            val half = t.truncatedTo(ChronoUnit.HOURS).plusMinutes(if (t.minute >= 30) 30L else 0L)
            val start = half.minusMinutes(30).plusDays(dayOffset.toLong()).toInstant().toEpochMilli()
            return GuideWindow(start, start + hours * HOUR_MS)
        }
    }
}
