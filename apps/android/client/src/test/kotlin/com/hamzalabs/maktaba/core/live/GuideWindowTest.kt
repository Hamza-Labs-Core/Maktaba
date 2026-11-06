package com.hamzalabs.maktaba.core.live

import com.hamzalabs.maktaba.core.api.Programme
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import java.time.Instant
import java.time.ZoneId

/** The guide's time maths both Players draw from (story 55.07 T01). */
class GuideWindowTest {
    private val zone = ZoneId.of("Asia/Riyadh")
    private fun ms(s: String) = Instant.parse(s).toEpochMilli()

    @Test
    fun theWindowStartsAtTheHalfHourBeforeNowsHalfHour() {
        // 23:40 in Riyadh (20:40 UTC): from 23:00, three hours
        val w = GuideWindow.around(ms("2026-11-06T20:40:00Z"), zone, hours = 3)
        assertEquals(ms("2026-11-06T20:00:00Z"), w.startMs)
        assertEquals(180L, w.minutes)
        assertEquals(40f, w.nowLine(ms("2026-11-06T20:40:00Z"))!!, 0.001f)
    }

    @Test
    fun aProgrammeAcrossMidnightIsCutToTheWindow() {
        val w = GuideWindow.around(ms("2026-11-06T20:40:00Z"), zone, hours = 3)
        val p = Programme(start = "2026-11-06T19:30:00Z", stop = "2026-11-06T21:30:00Z", title = "across midnight")
        assertEquals(0f to 90f, w.cut(p))
        val late = Programme(start = "2026-11-06T22:30:00Z", stop = "2026-11-07T01:00:00Z", title = "late")
        assertEquals(150f to 180f, w.cut(late))
        assertNull("outside", w.cut(Programme(start = "2026-11-07T02:00:00Z", stop = "2026-11-07T03:00:00Z")))
        assertNull("no times", w.cut(Programme(start = "", stop = "")))
    }

    @Test
    fun laterAndEarlierMoveTheWindowWhileTheNowLineStaysAtNow() {
        val now = ms("2026-11-06T20:40:00Z")
        val w = GuideWindow.around(now, zone, hours = 2)
        val later = w.shifted(2)
        assertEquals(w.startMs + 2 * GuideWindow.HOUR_MS, later.startMs)
        assertNull("now is before the later window", later.nowLine(now))
        assertEquals(w, later.shifted(-2))
    }

    @Test
    fun todayAndTomorrowFollowTheZonesDay() {
        val now = ms("2026-11-06T20:40:00Z") // 23:40 in Riyadh
        val w = GuideWindow.around(now, zone, hours = 2)
        assertEquals(0, w.dayFrom(now, zone))
        // two hours later it is past midnight in Riyadh: tomorrow
        assertEquals(1, w.shifted(2).dayFrom(now, zone))
        assertEquals(1, GuideWindow.around(now, zone, 3, dayOffset = 1).dayFrom(now, zone))
    }
}
