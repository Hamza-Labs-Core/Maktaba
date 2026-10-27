package com.hamzalabs.maktaba.desktop

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class UpdatesTest {
    @Test
    fun eachComputerReadsItsOwnPlayerKeys() {
        assertEquals(listOf("windows-x86_64-player-msi", "windows-x86_64-player"), Updates.platformKeys("Windows 11", "amd64", false))
        assertEquals(listOf("linux-x86_64-player-deb", "linux-x86_64-player"), Updates.platformKeys("Linux", "amd64", false))
        assertEquals(listOf("linux-aarch64-player-rpm"), Updates.platformKeys("Linux", "aarch64", true))
        // no player feed for a Mac (the Apple Player is apps/apple) or an unknown architecture
        assertEquals(emptyList<String>(), Updates.platformKeys("Mac OS X", "aarch64", false))
        assertEquals(emptyList<String>(), Updates.platformKeys("Linux", "riscv64", false))
    }

    @Test
    fun neverTakesAServerBundle() {
        // a feed with only the server apps' keys has nothing for a player
        val server = PlayerFeed("0.3.35", platforms = mapOf("windows-x86_64" to FeedEntry("s", "https://x/Maktaba_0.3.35_x64-setup.exe")))
        assertNull(Updates.entryFor(server, Updates.platformKeys("Windows 11", "amd64", false)))
        val player = PlayerFeed("0.3.35", platforms = mapOf("linux-x86_64-player" to FeedEntry("s", "https://x/p.deb")))
        assertEquals("https://x/p.deb", Updates.entryFor(player, Updates.platformKeys("Linux", "x86_64", false))?.url)
    }

    @Test
    fun comparesVersions() {
        assertTrue(Updates.isNewer("0.3.35", "0.3.34"))
        assertTrue(Updates.isNewer("v1.0.0", "0.9.9"))
        assertTrue(Updates.isNewer("0.3.35", "0.3.35-rc.1"))
        assertTrue(Updates.isNewer("0.3.35-rc.2", "0.3.35-rc.1"))
        assertTrue(Updates.isNewer("0.3.35", "0.0.0-dev"))
        assertFalse(Updates.isNewer("0.3.35", "0.3.35"))
        assertFalse(Updates.isNewer("0.3.34", "0.3.35"))
        assertFalse(Updates.isNewer("0.3.35-rc.1", "0.3.35"))
        assertFalse(Updates.isNewer("garbage", "0.3.35"))
    }
}
