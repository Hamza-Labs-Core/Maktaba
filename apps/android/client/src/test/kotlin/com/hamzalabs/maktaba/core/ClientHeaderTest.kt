package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.api.ClientHeader
import org.junit.Assert.assertEquals
import org.junit.Test

/** Every app names itself on every request (Epic 44): app, version, platform, device. */
class ClientHeaderTest {
    @Test
    fun theAppComesFromThePackageAndTheDeviceFromTheScreen() {
        assertEquals("player/1.3.0 (android; phone)", ClientHeader.make("com.hamzalabs.maktaba.player", "1.3.0"))
        assertEquals("player/1.3.0 (android; tablet)", ClientHeader.make("com.hamzalabs.maktaba.player", "1.3.0", ClientHeader.TABLET))
        assertEquals("remote/0.1.0 (android; foldable)", ClientHeader.make("com.hamzalabs.maktaba.remote", "0.1.0", ClientHeader.FOLDABLE))
        assertEquals("remote/dev (android; phone)", ClientHeader.make("com.hamzalabs.maktaba.remote", null))
        assertEquals("player/dev (android; phone)", ClientHeader.make("com.hamzalabs.maktaba.player", " "))
        // the Android TV app is a TV whatever its screen says
        assertEquals("tv/1.0.2 (androidtv; tv)", ClientHeader.make("com.hamzalabs.maktaba.tv", "1.0.2", ClientHeader.TABLET))
    }

    @Test
    fun oneAppForEveryScreen() {
        assertEquals(ClientHeader.PHONE, ClientHeader.deviceFor(smallestWidthDp = 411, hasHinge = false))
        assertEquals(ClientHeader.PHONE, ClientHeader.deviceFor(smallestWidthDp = 599, hasHinge = false))
        assertEquals(ClientHeader.TABLET, ClientHeader.deviceFor(smallestWidthDp = 600, hasHinge = false))
        assertEquals(ClientHeader.TABLET, ClientHeader.deviceFor(smallestWidthDp = 800, hasHinge = false))
        // a foldable is one folded (a phone's width) and open (a tablet's)
        assertEquals(ClientHeader.FOLDABLE, ClientHeader.deviceFor(smallestWidthDp = 360, hasHinge = true))
        assertEquals(ClientHeader.FOLDABLE, ClientHeader.deviceFor(smallestWidthDp = 673, hasHinge = true))
    }

    @Test
    fun beforeInitItIsNative() {
        // an app that has not started yet (a unit test) still signs in for tokens
        assertEquals("native", ClientHeader.value)
    }
}
