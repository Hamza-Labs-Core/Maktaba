package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.api.ErrorKind
import com.hamzalabs.maktaba.core.api.Problem
import com.hamzalabs.maktaba.core.api.errorKind
import org.junit.Assert.assertEquals
import org.junit.Test

/** Epic 50 story 50.19: Maktaba Cloud's relay refusing a plan's play, read from its answers. */
class RelayPlansTest {
    @Test
    fun theRelaysRefusals() {
        val needs = Problem.parse("""{"error":"relay_needs_plan","message":"remote play needs a plan"}""")
        assertEquals("relay_needs_plan", needs?.error)
        assertEquals(ErrorKind.RelayNeedsPlan, errorKind(402, needs))
        assertEquals(ErrorKind.RelayStreamLimit, errorKind(429, Problem.parse("""{"error":"stream_limit","message":"2 devices"}""")))
        val busy = Problem.parse("""{"type":"https://maktaba.tv/problems/home-busy","title":"busy","status":503,"code":"home_busy"}""")
        assertEquals(ErrorKind.RelayHomeBusy, errorKind(503, busy))
        // other 429s and 503s stay busy, another 402 is the server's
        assertEquals(ErrorKind.Busy, errorKind(429, Problem.parse("""{"error":"rate_limited"}""")))
        assertEquals(ErrorKind.Busy, errorKind(503, Problem.parse("""{"code":"paused"}""")))
        assertEquals(ErrorKind.Server, errorKind(402, Problem.parse("""{"error":"payment"}""")))
    }
}
