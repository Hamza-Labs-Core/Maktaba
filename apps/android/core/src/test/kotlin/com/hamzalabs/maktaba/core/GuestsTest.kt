package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.guest.GuestAccess
import com.hamzalabs.maktaba.core.guest.Guests
import com.hamzalabs.maktaba.core.guest.guestAccess
import com.hamzalabs.maktaba.core.parental.KeptLimit
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Test
import java.io.File

/** Story 50.27: Watch a broadcast stays shut on a device under a parental limit; every state has its own words. */
class GuestsTest {
    /** Home broadcasting is off for now: the build's switch keeps Watch a broadcast out of every app. */
    @Test
    fun broadcastingIsOffInThisBuild() {
        assertEquals(false, com.hamzalabs.maktaba.core.guest.GuestBroadcast.BUILT)
        assertEquals(null, com.hamzalabs.maktaba.core.guest.GuestBroadcast.override)
        assertEquals(false, com.hamzalabs.maktaba.core.guest.GuestBroadcast.ENABLED)
        try {
            com.hamzalabs.maktaba.core.guest.GuestBroadcast.override = true
            assertEquals(true, com.hamzalabs.maktaba.core.guest.GuestBroadcast.ENABLED)
        } finally {
            com.hamzalabs.maktaba.core.guest.GuestBroadcast.override = null
        }
    }

    @Test
    fun theDevicesKeptLimitsAreReadFailingClosed() {
        val dir = kotlin.io.path.createTempDirectory("parental").toFile()
        try {
            assertEquals("no mark: nobody was limited here", emptyList<KeptLimit.Mark?>(), Guests.marks(dir))
            assertEquals(emptyList<KeptLimit.Mark?>(), Guests.marks(File(dir, "missing")))
            KeptLimit.InFile(File(dir, "a.limit")).write(KeptLimit.Mark("sig", limited = false))
            assertEquals(GuestAccess.Allowed, guestAccess(Guests.marks(dir), known = null, limited = false))
            KeptLimit.InFile(File(dir, "b.limit")).write(KeptLimit.Mark("sig", limited = true))
            assertEquals(GuestAccess.Limited, guestAccess(Guests.marks(dir), known = null, limited = false))
            File(dir, "b.limit").delete()
            // a mark that is there but cannot be read: counted as a limit
            File(dir, "c.limit").writeText("")
            assertEquals(listOf(null), Guests.marks(dir).filter { it == null })
            assertEquals(GuestAccess.Limited, guestAccess(Guests.marks(dir), known = null, limited = false))
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
