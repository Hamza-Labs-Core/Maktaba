package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.guest.Guests
import com.hamzalabs.maktaba.core.parental.KeptLimit
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File

/** Story 50.27: Watch a broadcast hides on a device under a parental limit; every state has its own words. */
class GuestsTest {
    @Test
    fun theEntryHidesWhenASignInOnThisDeviceIsLimited() {
        val dir = kotlin.io.path.createTempDirectory("parental").toFile()
        try {
            assertTrue("no mark: nobody was limited here", Guests.allowedOnDevice(dir))
            assertTrue(Guests.allowedOnDevice(File(dir, "missing")))
            KeptLimit.InFile(File(dir, "a.limit")).write(KeptLimit.Mark("sig", limited = false))
            assertTrue(Guests.allowedOnDevice(dir))
            KeptLimit.InFile(File(dir, "b.limit")).write(KeptLimit.Mark("sig", limited = true))
            assertFalse(Guests.allowedOnDevice(dir))
        } finally {
            dir.deleteRecursively()
        }
    }

    @Test
    fun everyKeyHasItsOwnWords() {
        val words = Guests.keys.associateWith(Guests::words)
        // only site.error falls back on the general words
        assertEquals(R.string.guest_error, words.getValue("site.error"))
        assertEquals(Guests.keys.size, words.values.toSet().size)
        assertEquals(R.string.guest_error, Guests.words("something.new"))
        assertNotEquals(R.string.guest_error, Guests.words("broadcast.homeOffline"))
    }
}
