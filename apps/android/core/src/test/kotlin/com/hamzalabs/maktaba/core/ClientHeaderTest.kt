package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.api.ClientHeader
import org.junit.Assert.assertEquals
import org.junit.Test

/** Every app names itself on every request (Epic 44): app, version, platform, device. */
class ClientHeaderTest {
    @Test
    fun theAppAndDeviceComeFromThePackage() {
        assertEquals("player/1.3.0 (android; phone)", ClientHeader.make("com.hamzalabs.maktaba.player", "1.3.0"))
        assertEquals("player/1.3.0 (android; tablet)", ClientHeader.make("com.hamzalabs.maktaba.player.tablet", "1.3.0"))
        assertEquals("remote/0.1.0 (android; foldable)", ClientHeader.make("com.hamzalabs.maktaba.remote.foldable", "0.1.0"))
        assertEquals("remote/dev (android; phone)", ClientHeader.make("com.hamzalabs.maktaba.remote", null))
        assertEquals("player/dev (android; phone)", ClientHeader.make("com.hamzalabs.maktaba.player", " "))
    }

    @Test
    fun beforeInitItIsNative() {
        // an app that has not started yet (a unit test) still signs in for tokens
        assertEquals("native", ClientHeader.value)
    }
}
