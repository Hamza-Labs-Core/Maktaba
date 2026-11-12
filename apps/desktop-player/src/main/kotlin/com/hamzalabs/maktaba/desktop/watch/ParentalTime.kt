package com.hamzalabs.maktaba.desktop.watch

import com.hamzalabs.maktaba.core.api.timeRefusal
import com.hamzalabs.maktaba.core.parental.TimeView
import com.hamzalabs.maktaba.desktop.Lang
import com.hamzalabs.maktaba.desktop.Texts
import java.time.Instant
import java.time.ZoneId
import java.util.Locale

/**
 * The home said this account's watching time is over (Epic 57 story 57.15): why
 * (`outside_slot`, `limit_reached`, `no_heartbeat`), when watching opens again (null: the home
 * named no time), and where the video stopped.
 */
data class TimeUp(val reason: String, val nextAllowedAt: Instant?, val positionMs: Long = 0)

/**
 * The home's 403 `parental-time` refusal (stories 57.14 and 57.15) and the words the desktop
 * Player says about time. The home keeps the clock and the count; the Player shows what it
 * answers and keeps no count of its own. The reading and the rules are the apps' shared
 * client's ([timeRefusal], [TimeView]); only the words are this Player's.
 */
object ParentalTime {
    /** The time refusal in what a call threw, or null when it is anything else. */
    fun refusal(e: Throwable): TimeUp? = e.timeRefusal()?.let { TimeUp(it.reason, it.nextAllowedAt) }

    /** The screen's title: not watching time yet outside a slot, else time's up. */
    fun titleKey(reason: String): String =
        if (TimeView.outsideSlot(reason)) "parental.time.outside.title" else "parental.time.up.title"

    /**
     * When watching opens again, in the viewer's words: the time alone when it is today, with
     * the weekday otherwise. The instant is the home's; only its display is this computer's.
     */
    fun next(at: Instant, lang: Lang, zone: ZoneId = ZoneId.systemDefault(), now: Instant = Instant.now()): String =
        TimeView.nextWords(at, Locale.forLanguageTag(lang.code), zone, now)

    /** A position as m:ss, or h:mm:ss from an hour up. */
    fun clock(ms: Long): String = TimeView.clock(ms)

    /** A length of time in words: "45 minutes", "1 hour and 5 minutes", "less than a minute". */
    fun words(seconds: Long, t: Texts): String {
        val p = TimeView.parts(seconds)
        if (p.underMinute) return t["parental.time.underMinute"]
        val h = t["parental.time.hours." + plural(t.lang, p.hours), p.hours]
        val m = t["parental.time.minutes." + plural(t.lang, p.minutes), p.minutes]
        return when {
            p.hours == 0 -> m
            p.minutes == 0 -> h
            else -> t["parental.time.hoursMinutes", h, m]
        }
    }

    /** The plural form of [n] in [lang] (CLDR: English one/other; Arabic zero, one, two, few, many, other). */
    fun plural(lang: Lang, n: Int): String = when {
        lang != Lang.Ar -> if (n == 1) "one" else "other"
        n == 0 -> "zero"
        n == 1 -> "one"
        n == 2 -> "two"
        n % 100 in 3..10 -> "few"
        n % 100 in 11..99 -> "many"
        else -> "other"
    }
}
