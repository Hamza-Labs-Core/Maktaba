package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.api.BUSY_RETRY_SEC
import com.hamzalabs.maktaba.core.api.busyRetryMs
import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * Epic 59 story 59.05: the wait before asking again for a stream the home had no transcode slot
 * for, shared by every Android player (the phone Player and Android TV): the home's
 * `Retry-After`, or 10 s, kept between 1 s and 60 s.
 */
class BusyRetryTest {
    @Test
    fun theHomesRetryAfterIsTheWait() {
        assertEquals(10_000L, busyRetryMs(10))
        assertEquals(25_000L, busyRetryMs(25))
    }

    @Test
    fun noRetryAfterWaitsTenSeconds() {
        assertEquals(10L, BUSY_RETRY_SEC)
        assertEquals(10_000L, busyRetryMs(null))
    }

    @Test
    fun theWaitStaysBetweenOneSecondAndAMinute() {
        assertEquals("never asked again at once", 1_000L, busyRetryMs(0))
        assertEquals("never left waiting for an hour", 60_000L, busyRetryMs(3_600))
    }
}
