package com.hamzalabs.maktaba.desktop.watch

import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.desktop.Lang
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle
import java.util.Locale

/**
 * The home said this account's watching time is over (Epic 57 story 57.15): why
 * (`outside_slot`, `limit_reached`, `no_heartbeat`), when watching opens again (null: the home
 * named no time), and where the video stopped.
 */
data class TimeUp(val reason: String, val nextAllowedAt: Instant?, val positionMs: Long = 0)

/**
 * The home's 403 `parental-time` refusal (stories 57.14 and 57.15). The home keeps the clock
 * and the count; the Player shows what it answers and keeps no count of its own.
 */
object ParentalTime {
    const val SLUG = "parental-time"
    const val CODE = "parental_time"
    const val OUTSIDE_SLOT = "outside_slot"

    /** The time refusal in what a call threw, or null when it is anything else. */
    fun refusal(e: Throwable): TimeUp? {
        if (e !is ApiException.Status || e.code != 403) return null
        val p = e.problem ?: return null
        if (p.slug != SLUG && p.code != CODE) return null
        val next = field(e.body, "next_allowed_at")?.let { runCatching { Instant.parse(it) }.getOrNull() }
        return TimeUp(field(e.body, "reason").orEmpty(), next)
    }

    /** A string field of the problem's JSON (the two this screen needs hold no escapes). */
    private fun field(body: String, name: String): String? =
        Regex("\"" + Regex.escape(name) + "\"\\s*:\\s*\"([^\"\\\\]*)\"").find(body)?.groupValues?.get(1)

    /** The screen's title: not watching time yet outside a slot, else time's up. */
    fun titleKey(reason: String): String =
        if (reason == OUTSIDE_SLOT) "parental.time.outside.title" else "parental.time.up.title"

    /**
     * When watching opens again, in the viewer's words: the time alone when it is today, with
     * the weekday otherwise. The instant is the home's; only its display is this computer's.
     */
    fun next(at: Instant, lang: Lang, zone: ZoneId = ZoneId.systemDefault(), now: Instant = Instant.now()): String {
        val locale = Locale.forLanguageTag(lang.code)
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
