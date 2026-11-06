package com.hamzalabs.maktaba.core.live

import com.hamzalabs.maktaba.core.api.Programme
import java.time.Instant
import java.time.ZoneId
import java.time.ZonedDateTime
import java.time.temporal.ChronoUnit

/**
 * A span of the Live TV guide: [startMs] up to [endMs], epoch milliseconds. The guide's time
 * maths, shared by the Android and desktop Players (Epic 55, story 55.07): the window asked for,
 * a programme cut to it, the now line, Earlier and Later.
 */
data class GuideWindow(val startMs: Long, val endMs: Long) {
    val minutes: Long get() = (endMs - startMs) / 60_000

    /** Minutes from the window's start to [ms] (negative before it). */
    fun minutesTo(ms: Long): Float = (ms - startMs) / 60_000f

    /** The window moved by [hours] (earlier when negative). */
    fun shifted(hours: Long): GuideWindow = GuideWindow(startMs + hours * HOUR_MS, endMs + hours * HOUR_MS)

    /** Where the now line is: minutes from the start, or null when [nowMs] is outside the window. */
    fun nowLine(nowMs: Long): Float? = minutesTo(nowMs).takeIf { it >= 0 && it < minutes }

    /**
     * [p] cut to the window, as minutes from its start (from, to); null when it has no times or
     * lies outside the window.
     */
    fun cut(p: Programme): Pair<Float, Float>? {
        val s = p.startMs ?: return null
        val e = p.stopMs ?: return null
        val from = minutesTo(s).coerceAtLeast(0f)
        val to = minutesTo(e).coerceAtMost(minutes.toFloat())
        return if (to <= from) null else from to to
    }

    /** Which day the window starts on, from [nowMs]'s day in [zone]: 0 today, 1 tomorrow, -1 yesterday. */
    fun dayFrom(nowMs: Long, zone: ZoneId): Int {
        val today = Instant.ofEpochMilli(nowMs).atZone(zone).toLocalDate()
        val day = Instant.ofEpochMilli(startMs).atZone(zone).toLocalDate()
        return ChronoUnit.DAYS.between(today, day).toInt()
    }

    companion object {
        const val HOUR_MS = 3_600_000L

        /** How many hours the guide asks for at once: three on a phone, six on a tablet or a desktop window (FR-22). */
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
