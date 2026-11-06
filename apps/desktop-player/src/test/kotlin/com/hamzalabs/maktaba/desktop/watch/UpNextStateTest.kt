package com.hamzalabs.maktaba.desktop.watch

import com.hamzalabs.maktaba.desktop.PlayRequest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/** Story 55.08 T05: each tick is a second. */
class UpNextStateTest {
    private val next = PlayRequest("v2", "Episode 2", startSec = 0)

    @Test
    fun atTheEndItCountsDownThenPlaysTheNext() {
        val s = UpNextState(leadSec = 20, countSec = 3)
        s.reset(next)
        assertNull(s.tick(10_000, 100_000, ended = false))
        assertNull(s.secondsLeft)
        // 20 s before the end: the card
        assertNull(s.tick(80_000, 100_000, ended = false))
        assertEquals(3, s.secondsLeft)
        assertNull(s.tick(81_000, 100_000, ended = false))
        assertNull(s.tick(82_000, 100_000, ended = false))
        assertEquals(1, s.secondsLeft)
        assertEquals(next, s.tick(83_000, 100_000, ended = false))
        assertNull("once", s.tick(84_000, 100_000, ended = true))
    }

    @Test
    fun aSeekBackBeforeTheLeadTakesTheCardAway() {
        val s = UpNextState(leadSec = 20, countSec = 3)
        s.reset(next)
        assertNull(s.tick(80_000, 100_000, ended = false))
        assertEquals(3, s.secondsLeft)
        // back to the middle: no card, and no countdown running on
        assertNull(s.tick(30_000, 100_000, ended = false))
        assertNull(s.secondsLeft)
        repeat(5) { assertNull(s.tick(31_000L + it * 1_000, 100_000, ended = false)) }
        assertNull(s.secondsLeft)
        // at the lead again: the card again, from the start
        assertNull(s.tick(81_000, 100_000, ended = false))
        assertEquals(3, s.secondsLeft)
    }

    @Test
    fun cancelKeepsTheVideo() {
        val s = UpNextState(countSec = 2)
        s.reset(next)
        s.tick(0, 0, ended = true)
        s.cancel()
        repeat(5) { assertNull(s.tick(0, 0, ended = true)) }
        assertNull(s.secondsLeft)
    }

    @Test
    fun nothingWithoutANextOrWithAutoplayOff() {
        val s = UpNextState()
        s.reset(null)
        assertNull(s.tick(0, 0, ended = true))
        s.reset(next)
        assertNull(s.tick(0, 0, ended = true, autoplay = false))
        assertNull(s.secondsLeft)
        assertEquals(next, s.playNow())
    }
}
