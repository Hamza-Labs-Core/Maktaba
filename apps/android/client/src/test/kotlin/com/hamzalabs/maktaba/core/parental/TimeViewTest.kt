package com.hamzalabs.maktaba.core.parental

import com.hamzalabs.maktaba.core.api.MyTime
import com.hamzalabs.maktaba.core.api.TimeReason
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.Instant
import java.time.ZoneId
import java.util.Locale

/** Story 57.15: the time left, the warnings and the time's-up words, the same on every player. */
class TimeViewTest {
    private fun left(seconds: Long?) = MyTime(allowed = true, limited = true, remainingSeconds = seconds)

    @Test
    fun theTimeLeftShowsInThePlayerFromFifteenMinutes() {
        assertFalse(TimeView.showsInPlayer(left(901)))
        assertTrue(TimeView.showsInPlayer(left(900)))
        assertTrue(TimeView.showsInPlayer(left(0)))
        assertFalse("no answer yet", TimeView.showsInPlayer(null))
        assertFalse("no rule covers now", TimeView.showsInPlayer(MyTime(allowed = true, limited = false, remainingSeconds = 60)))
        assertFalse("over", TimeView.showsInPlayer(MyTime(allowed = false, limited = true, remainingSeconds = 0)))
        assertFalse(TimeView.showsInPlayer(left(null)))
    }

    @Test
    fun theWarningsAreAtFiveMinutesAndOneMinute() {
        assertNull(TimeView.warningLine(left(301)))
        assertEquals(300L, TimeView.warningLine(left(300)))
        assertEquals(300L, TimeView.warningLine(left(61)))
        assertEquals(60L, TimeView.warningLine(left(60)))
        assertEquals(60L, TimeView.warningLine(left(0)))
        assertNull(TimeView.warningLine(MyTime(allowed = true, limited = false, remainingSeconds = 10)))
    }

    @Test
    fun eachWarningShowsOnceAsTheAnswerCrossesIt() {
        val w = TimeWarnings()
        assertNull(w.crossed(left(400)))
        assertEquals(300L, w.crossed(left(290)))
        assertNull("the same line again", w.crossed(left(200)))
        assertEquals(60L, w.crossed(left(55)))
        assertNull(w.crossed(left(20)))
        // a parent gave more time: back above the lines is no warning, and they can come again
        assertNull(w.crossed(left(250)))
        assertNull(w.crossed(left(1000)))
        assertEquals(300L, w.crossed(left(300)))
    }

    @Test
    fun aWarningNamesTheTimeLeftNotTheLineCrossed() {
        // crossed between two polls: the line's own words
        assertEquals(300L, TimeView.warningSeconds(300, left(300)))
        assertEquals(300L, TimeView.warningSeconds(300, left(290)))
        assertEquals(60L, TimeView.warningSeconds(60, left(52)))
        // a video opened with two minutes left is under the five-minute line: it says two minutes
        assertEquals(120L, TimeView.warningSeconds(300, left(120)))
        // and with thirty seconds left, not "1 minute"
        assertEquals(30L, TimeView.warningSeconds(60, left(30)))
        assertEquals("no countdown named: the line", 300L, TimeView.warningSeconds(300, null))
    }

    @Test
    fun aPlayerOpenedInsideTheLastMinuteWarnsOfTheMinute() {
        assertEquals(60L, TimeWarnings().crossed(left(30)))
    }

    @Test
    fun theHomeIsAskedSoonerNearTheEnd() {
        assertEquals(30_000L, TimeView.pollMs(left(361)))
        assertEquals(10_000L, TimeView.pollMs(left(360)))
        assertEquals(30_000L, TimeView.pollMs(null))
        assertEquals(30_000L, TimeView.pollMs(MyTime(allowed = false, limited = true, remainingSeconds = 0)))
    }

    @Test
    fun aTimeAnswerThatRefusesIsARefusal() {
        val next = Instant.parse("2026-11-12T07:00:00Z")
        val r = TimeView.refusal(MyTime(allowed = false, limited = true, reason = TimeReason.OUTSIDE_SLOT, nextAllowedAt = next))!!
        assertEquals(TimeReason.OUTSIDE_SLOT, r.reason)
        assertEquals(next, r.nextAllowedAt)
        assertTrue(TimeView.outsideSlot(r.reason))
        assertNull(TimeView.refusal(left(10)))
        assertNull(TimeView.refusal(null))
        assertFalse(TimeView.outsideSlot(TimeReason.LIMIT_REACHED))
        assertFalse(TimeView.outsideSlot(TimeReason.NO_HEARTBEAT))
    }

    @Test
    fun hoursAndMinutesRoundDown() {
        assertEquals(TimeView.Parts(0, 0), TimeView.parts(59))
        assertTrue(TimeView.parts(59).underMinute)
        assertEquals(TimeView.Parts(0, 1), TimeView.parts(60))
        assertEquals(TimeView.Parts(0, 14), TimeView.parts(899))
        assertEquals(TimeView.Parts(1, 5), TimeView.parts(3900))
        assertEquals(TimeView.Parts(2, 0), TimeView.parts(7200))
        assertEquals(TimeView.Parts(0, 0), TimeView.parts(-5))
    }

    @Test
    fun theNextTimeIsTheTimeAloneTodayAndWithItsDayOtherwise() {
        val zone = ZoneId.of("UTC")
        val now = Instant.parse("2026-11-11T16:00:00Z") // a Wednesday
        val today = TimeView.nextWords(Instant.parse("2026-11-11T19:30:00Z"), Locale.ENGLISH, zone, now)
        assertTrue(today, today.contains("7:30"))
        assertFalse(today, today.contains("Wednesday"))
        val tomorrow = TimeView.nextWords(Instant.parse("2026-11-12T07:00:00Z"), Locale.ENGLISH, zone, now)
        assertTrue(tomorrow, tomorrow.startsWith("Thursday") && tomorrow.contains("7:00"))
        val ar = TimeView.nextWords(Instant.parse("2026-11-12T07:00:00Z"), Locale.forLanguageTag("ar"), zone, now)
        assertTrue(ar, ar.startsWith("الخميس"))
    }

    @Test
    fun aPositionReadsAsAClock() {
        assertEquals("0:00", TimeView.clock(0))
        assertEquals("12:05", TimeView.clock(725_000))
        assertEquals("1:02:03", TimeView.clock(3_723_000))
    }
}
