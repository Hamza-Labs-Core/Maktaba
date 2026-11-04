package com.hamzalabs.maktaba.tv

import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.core.api.capabilitiesGate
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith

/**
 * Story 51.11's build switch: the app's session follows its build (`-Pmaktaba.capabilitiesGate`,
 * kept in BuildConfig), and a build that does not say leaves it on.
 */
@RunWith(AndroidJUnit4::class)
class CapabilitiesGateTest {
    @Test
    fun theSessionFollowsTheBuildSwitch() {
        val app = ApplicationProvider.getApplicationContext<MaktabaTVApp>()
        assertEquals(capabilitiesGate(BuildConfig.CAPABILITIES_GATE), app.session.capabilitiesGate)
        if (BuildConfig.CAPABILITIES_GATE.isEmpty()) assertTrue("unset: on", app.session.capabilitiesGate)
    }
}
