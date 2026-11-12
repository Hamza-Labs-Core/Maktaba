package com.hamzalabs.maktaba.core

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.Instant

/** The demo library's reader (story 60.09): the route table, its pinned clock, its art. */
class DemoLibraryTest {
    @Test
    fun timesAreFilledFromThePinnedClock() {
        val clock = Instant.parse("2026-06-15T12:00:00Z")
        assertEquals(
            """"2026-06-15T12:00:00Z","2026-06-15T11:58:00Z","2026-06-15T12:13:00Z"""",
            DemoLibrary.fillTimes(""""{{now}}","{{now-120}}","{{now+780}}"""", clock),
        )
    }

    @Test
    fun eachLanguageAnswersItsOwnTitles() {
        val en = DemoLibrary.load("en").answer("GET", "/api/videos/v-sintel")!!
        val ar = DemoLibrary.load("ar").answer("GET", "/api/videos/v-sintel")!!
        assertEquals(200, en.status)
        assertTrue(String(en.body).contains("\"Sintel\""))
        assertTrue(String(ar.body).contains("سينتل"))
        assertFalse("every time filled", String(DemoLibrary.load("en").answer("GET", "/api/channels/now")!!.body).contains("{{"))
    }

    @Test
    fun anImageRouteAnswersItsJpeg() {
        val poster = DemoLibrary.load("en").answer("GET", "/api/videos/v-sintel/poster")
        assertNotNull(poster)
        assertEquals("image/jpeg", poster!!.contentType)
        // a JPEG starts with FF D8
        assertEquals(0xFF.toByte(), poster.body[0])
        assertEquals(0xD8.toByte(), poster.body[1])
    }

    @Test
    fun aNamedSegmentMatchesAnyOneSegmentAndNothingElseIsAnswered() {
        val demo = DemoLibrary.load("en")
        assertNotNull(demo.answer("GET", "/api/videos/v-spring/chapters"))
        assertNull(demo.answer("GET", "/api/nothing/here"))
        assertNull(demo.answer("DELETE", "/api/videos/v-sintel"))
    }

    @Test
    fun theSignedInAccountIsTheFixtures() {
        val demo = DemoLibrary.load("en")
        assertEquals("haddad", demo.username)
        assertEquals("Living room", demo.homeName)
    }
}
