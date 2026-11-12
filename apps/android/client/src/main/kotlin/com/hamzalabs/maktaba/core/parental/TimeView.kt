package com.hamzalabs.maktaba.core.parental

import com.hamzalabs.maktaba.core.api.MyTime
import com.hamzalabs.maktaba.core.api.TimeReason
import com.hamzalabs.maktaba.core.api.TimeRefusal
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle
import java.util.Locale

/**
 * How the players show the time the home says an account has (Epic 57 story 57.15), the same
 * on the phone, the TV and the desktop: when the time left shows, which warning an answer is
 * under and how often to ask. Nothing here counts: every number is the home's last answer.
 */
object TimeView {
    /** How often the home is asked while a screen shows the time. */
    const val POLL_MS = 30_000L

    /** Closer to the end, sooner: the one-minute warning must not come late. */
    const val NEAR_POLL_MS = 10_000L

    /** "Near": this many seconds or fewer left. */
    const val NEAR_S = 6 * 60L

    /** The time left shows in the player once this many seconds or fewer remain. */
    const val SHOW_S = 15 * 60L

    /** The warnings: five minutes and one minute before the end. */
    val WARNINGS_S = listOf(300L, 60L)

    /** How long a warning stays on screen. */
    const val WARNING_SHOWN_MS = 10_000L

    /** The seconds left when a rule covers now and watching is allowed; else null. */
    fun left(time: MyTime?): Long? =
        if (time != null && time.allowed && time.limited) time.remainingSeconds?.coerceAtLeast(0) else null

    /** Whether the player shows the time left: 15 minutes or fewer remain. */
    fun showsInPlayer(time: MyTime?): Boolean = left(time)?.let { it <= SHOW_S } == true

    /** The warning line an answer is under: 300, then 60; null above both and when no rule covers now. */
    fun warningLine(time: MyTime?): Long? {
        val left = left(time) ?: return null
        return WARNINGS_S.sorted().firstOrNull { left <= it }
    }

    /** When to ask the home next after [time] (null: the call failed). */
    fun pollMs(time: MyTime?): Long = if (left(time)?.let { it <= NEAR_S } == true) NEAR_POLL_MS else POLL_MS

    /** Whether the time is over by the home's own answer. */
    fun over(time: MyTime?): Boolean = time != null && !time.allowed

    /** The refusal a "not allowed" time answer stands for (Home's line, a player not yet opened). */
    fun refusal(time: MyTime?): TimeRefusal? =
        if (over(time)) TimeRefusal(time!!.reason.ifEmpty { TimeReason.LIMIT_REACHED }, time.nextAllowedAt) else null

    /** Whole hours and minutes of [seconds], rounded down: 0 and 0 is "less than a minute". */
    fun parts(seconds: Long): Parts {
        val total = (seconds.coerceAtLeast(0) / 60)
        return Parts((total / 60).toInt(), (total % 60).toInt())
    }

    data class Parts(val hours: Int, val minutes: Int) {
        val underMinute: Boolean get() = hours == 0 && minutes == 0
    }

    /** Whether the screen is titled "Not watching time yet" (outside a slot) and not "Time's up". */
    fun outsideSlot(reason: String): Boolean = reason == TimeReason.OUTSIDE_SLOT

    /**
     * When watching opens again, in the viewer's words: the time alone when it is today, with
     * the weekday otherwise. The instant is the home's; only its display is this device's.
     */
    fun nextWords(at: Instant, locale: Locale, zone: ZoneId = ZoneId.systemDefault(), now: Instant = Instant.now()): String {
        val local = at.atZone(zone)
        val time = DateTimeFormatter.ofLocalizedTime(FormatStyle.SHORT).withLocale(locale).format(local)
        if (local.toLocalDate() == now.atZone(zone).toLocalDate()) return time
        return DateTimeFormatter.ofPattern("EEEE", locale).format(local) + " " + time
    }

    /** A position as m:ss, or h:mm:ss from an hour up. */
    fun clock(ms: Long): String {
        val s = (ms / 1000).coerceAtLeast(0)
        val h = s / 3600
        val m = (s % 3600) / 60
        val sec = s % 60
        return if (h > 0) "%d:%02d:%02d".format(Locale.ROOT, h, m, sec) else "%d:%02d".format(Locale.ROOT, m, sec)
    }
}

/**
 * Which warning to show as answers come: each line (five minutes, one minute) once as the
 * answer crosses it, and again only after the time went back above it (a parent gave more).
 */
class TimeWarnings {
    private var last: Long? = null

    /** The line [time] newly crossed (300 or 60), or null. */
    fun crossed(time: MyTime?): Long? {
        val line = TimeView.warningLine(time)
        if (line == last) return null
        val was = last
        last = line
        // a larger line after a smaller one is time given back, not a warning
        return line?.takeIf { was == null || it < was }
    }
}
