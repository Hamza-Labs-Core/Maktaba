package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.session.FoundHome
import com.hamzalabs.maktaba.core.session.foundHome
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import java.net.InetAddress

class DiscoveryTest {
    private val mac = InetAddress.getByName("192.168.50.177")

    /** As the live home announces itself (`dns-sd -L`, v0.3.20). */
    private val homeTxt = mapOf(
        "api" to "/api", "home" to "28e21b45", "id" to "28e21b45", "join" to "0", "name" to "Mahmouds-Mac-Studio",
        "path" to "/", "protocol" to "1", "role" to "home", "schema_rev" to "152", "version" to "v0.3.20", "web" to "8088",
    )

    @Test
    fun aHomeIsReachedAtItsWebPort() {
        assertEquals(
            FoundHome("Mahmouds-Mac-Studio", "http://192.168.50.177:8088", "28e21b45", "v0.3.20"),
            foundHome("Maktaba (Mahmouds-Mac-Studio)", mac, 8080, homeTxt),
        )
    }

    @Test
    fun aMachineThatWorksForAHomeIsNotOne() {
        assertNull(foundHome("Maktaba (windows-pc)", mac, 8780, homeTxt + ("role" to "node")))
    }

    @Test
    fun anOlderHomeSaysLess() {
        val h = foundHome("Maktaba (attic)", mac, 8080, mapOf("version" to "v0.2.0", "api" to "/api"))!!
        assertEquals("attic", h.name)
        assertEquals("the service's port when it names no web port", "http://192.168.50.177:8080", h.url)
        assertNull(h.id)
    }

    @Test
    fun anIpv6AddressIsBracketed() {
        val h = foundHome("Maktaba (x)", InetAddress.getByName("fe80::1%5"), 8080, homeTxt)!!
        assertEquals("no zone, in brackets", "http://[fe80:0:0:0:0:0:0:1]:8088", h.url)
    }

    @Test
    fun noAddressOrPortIsNoHome() {
        assertNull(foundHome("Maktaba (x)", null, 8080, homeTxt))
        assertNull(foundHome("Maktaba (x)", mac, 0, homeTxt - "web"))
    }
}
