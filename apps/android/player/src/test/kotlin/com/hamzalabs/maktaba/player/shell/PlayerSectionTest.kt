package com.hamzalabs.maktaba.player.shell

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** FR-5 and the release rule: the five sections, and only merged ones reachable. */
class PlayerSectionTest {
    @Test
    fun theFiveSectionsInTheirOrder() {
        assertEquals(
            listOf("Home", "Libraries", "LiveTv", "Search", "Profile"),
            PlayerSection.entries.map { it.name },
        )
    }

    @Test
    fun enabledKeepsTheSectionsOrderWithLibrariesAlwaysThere() {
        val enabled = PlayerSection.enabled
        assertTrue("today's browser stays reachable", PlayerSection.Libraries in enabled)
        assertEquals("one line each, no repeats", enabled.distinct(), enabled)
        assertEquals("in the navigation's order", PlayerSection.entries.filter { it in enabled }, enabled)
    }

    @Test
    fun eachSectionHasItsOwnRoot() {
        assertEquals(PlayerSection.entries.size, PlayerSection.entries.map { it.root }.toSet().size)
        PlayerSection.entries.forEach { assertEquals(it, PlayerSection.of(it.root)) }
        assertNull("a pushed screen is no section", PlayerSection.of(Route.Video("v1")))
    }
}
