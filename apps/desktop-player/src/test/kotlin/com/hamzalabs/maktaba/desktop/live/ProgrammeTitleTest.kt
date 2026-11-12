package com.hamzalabs.maktaba.desktop.live

import com.hamzalabs.maktaba.core.api.Programme
import com.hamzalabs.maktaba.desktop.Lang
import com.hamzalabs.maktaba.desktop.Texts
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Epic 57 story 57.10 on the desktop Player's channel bar: a programme above the account's limit
 * shows as "Not available", the next one as the current one (the home sends its time only).
 */
class ProgrammeTitleTest {
    private val news = Programme(start = "2026-11-11T20:00:00Z", stop = "2026-11-11T21:00:00Z", title = "The News")
    private val hidden = Programme(start = "2026-11-11T20:00:00Z", stop = "2026-11-11T21:00:00Z", title = "", restricted = true)

    @Test
    fun theNextProgrammeAboveTheLimitSaysNotAvailable() {
        val en = Texts(Lang.En)
        val next = nextLine(hidden, en)
        assertTrue(next, next.endsWith("Not available"))
        assertFalse("not a line that ends in nothing", next.trim() == en["live.next", "", ""].trim())
        assertTrue(nextLine(news, en).endsWith("The News"))
        assertEquals("Not available", programmeTitle(hidden, en))
        assertEquals("The News", programmeTitle(news, en))
    }

    @Test
    fun inArabicToo() {
        val ar = Texts(Lang.Ar)
        assertTrue(nextLine(hidden, ar).contains("غير متاح"))
        assertEquals("غير متاح", programmeTitle(hidden, ar))
    }
}
