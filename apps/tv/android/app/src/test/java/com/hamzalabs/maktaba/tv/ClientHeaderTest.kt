package com.hamzalabs.maktaba.tv

import com.hamzalabs.maktaba.tv.data.api.ClientHeader
import org.junit.Assert.assertEquals
import org.junit.Test

/** The Android TV app names itself on every request (Epic 44). */
class ClientHeaderTest {

    @Test
    fun `names the tv app with its version`() {
        assertEquals("tv/1.0.0 (androidtv; tv)", ClientHeader.make("1.0.0"))
        assertEquals("tv/dev (androidtv; tv)", ClientHeader.make(null))
        assertEquals("tv/dev (androidtv; tv)", ClientHeader.make(""))
    }

    @Test
    fun `is the tv app before init`() {
        assertEquals("tv/dev (androidtv; tv)", ClientHeader.value)
    }
}
