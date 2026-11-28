package com.hamzalabs.maktaba.desktop.mpv

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Story 13.22: the newest load's events, in every order the threads may come: the caller's
 * (another load), the one that reads each entry's id, and mpv's events.
 */
class NewestLoadTest {
    private val heardNothing = NewestLoad.Heard()

    @Test
    fun anOpeningIsGivenOnceWhetherItsIdIsReadBeforeOrAfter() {
        // the id first, then the event
        val a = NewestLoad()
        assertEquals(heardNothing, a.read(0, 1))
        a.starts(1)
        assertTrue(a.opens())
        assertFalse("twice", a.opens())
        // the event first, then the id
        val b = NewestLoad()
        b.starts(1)
        assertFalse("its id is not known", b.opens())
        assertEquals(NewestLoad.Heard(opened = true), b.read(0, 1))
        assertFalse("twice", b.opens())
        // between the two: the event given on, and the id read again has nothing more to give
        val c = NewestLoad()
        c.read(0, 1)
        c.starts(1)
        assertTrue(c.opens())
        assertFalse("twice", c.read(0, 1).opened)
    }

    @Test
    fun onlyTheNewestLoadsIdIsKept() {
        val l = NewestLoad()
        // two more loads before the first id is read
        assertEquals(1, l.next())
        assertEquals(2, l.next())
        assertEquals(heardNothing, l.read(0, 1))
        assertEquals(heardNothing, l.read(1, 2))
        l.starts(2)
        assertFalse("a replaced stream's opening", l.opens())
        assertFalse("a replaced stream's picture", l.restarts())
        assertTrue(l.holding)
        assertEquals(heardNothing, l.read(2, 3))
        l.starts(3)
        assertTrue(l.opens())
        assertFalse("its picture has not started", l.shows())
        assertTrue(l.restarts())
        assertFalse(l.holding)
        assertTrue(l.shows())
    }

    @Test
    fun anEndHeardBeforeTheIdIsTheNewestsOnly() {
        val l = NewestLoad()
        l.next()
        l.next()
        val old = NewestLoad.End(2, MpvConst.END_ERROR, -13)
        val own = NewestLoad.End(3, MpvConst.END_EOF, 0)
        assertFalse(l.ends(old))
        assertFalse(l.ends(own))
        // an older load's id neither takes the ends nor settles the entry
        assertEquals(heardNothing, l.read(1, 2))
        assertEquals(listOf(own), l.read(2, 3).ends)
        assertFalse(l.ends(old))
        assertTrue(l.ends(own))
        // the next load drops what is kept for none
        l.next()
        assertFalse(l.ends(own))
        l.next()
        assertEquals(heardNothing, l.read(4, 9))
    }

    @Test
    fun aPictureThatStartedBeforeItsIdWasReadEndsTheHold() {
        val l = NewestLoad()
        l.starts(1)
        l.opens()
        assertFalse(l.restarts())
        assertTrue(l.holding)
        assertEquals(NewestLoad.Heard(opened = true, restarted = true), l.read(0, 1))
        assertFalse(l.holding)
        assertTrue(l.played(1))
        // another load holds again, whatever the stream before it says
        l.next()
        assertTrue(l.holding)
        assertFalse(l.restarts())
        assertTrue(l.holding)
    }

    @Test
    fun anMpvThatGivesNoIdsCountsEveryEvent() {
        val l = NewestLoad()
        assertEquals(heardNothing, l.read(0, null))
        assertTrue(l.opens())
        assertTrue(l.restarts())
        assertTrue(l.ends(NewestLoad.End(0, MpvConst.END_EOF, 0)))
        l.next()
        l.read(1, null)
        assertTrue("the second stream's opening", l.opens())
    }
}
