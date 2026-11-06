package com.hamzalabs.maktaba.desktop

import com.hamzalabs.maktaba.core.MapStore
import com.hamzalabs.maktaba.core.MemoryTokens
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Story 55.02 T01, T02 and T07 (issue #638): the session is made with this build's gate. CI also
 * runs this test with `-Pmaktaba.capabilitiesGate=off`, where the build's gate is off.
 */
class CapabilitiesGateTest {
    @Test
    fun theSessionHasTheBuildsGate() {
        val session = playerSession(MapStore(), CoroutineScope(Dispatchers.Unconfined)) { MemoryTokens() }
        assertEquals(buildCapabilitiesGate(BuildInfo.CAPABILITIES_GATE), session.capabilitiesGate)
        // a build made without the property (a release) has the gate on
        if (BuildInfo.CAPABILITIES_GATE.isEmpty()) assertTrue(session.capabilitiesGate)
        if (BuildInfo.CAPABILITIES_GATE == "off") assertFalse(session.capabilitiesGate)
    }

    @Test
    fun theSwitchReadsLikeTheAndroidPlayers() {
        assertTrue(buildCapabilitiesGate(""))
        assertTrue(buildCapabilitiesGate("on"))
        assertFalse(buildCapabilitiesGate("off"))
        assertFalse(buildCapabilitiesGate("false"))
        assertFalse(buildCapabilitiesGate("0"))
    }
}
