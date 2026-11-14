package com.hamzalabs.maktaba.tv

import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.core.api.ClientHeader
import com.hamzalabs.maktaba.core.compat.CompatApp
import com.hamzalabs.maktaba.core.ui.updateAppHow
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import com.hamzalabs.maktaba.core.R as CoreR

/**
 * Story 58.05: the Amazon build is a Fire TV app. It names itself `firetv` on every request, asks
 * for the `firetv` streaming profile, and its Update this app page sends the person to the
 * Amazon Appstore. Runs in `testAmazonDebugUnitTest` only.
 */
@RunWith(AndroidJUnit4::class)
class FireTvBuildTest {
    @Test
    fun theAmazonBuildIsAFireTv() {
        assertEquals("amazon", BuildConfig.FLAVOR)
        assertEquals(ClientHeader.FIRE_TV, BuildConfig.TV_PLATFORM)
        // the app started (MaktabaTVApp.onCreate) and set its header from the build
        ApplicationProvider.getApplicationContext<MaktabaTVApp>()
        assertTrue(ClientHeader.value, ClientHeader.value.startsWith("tv/") && ClientHeader.value.endsWith("(firetv; tv)"))
        val app = CompatApp.fromClientHeader(ClientHeader.value)
        assertEquals("tv/firetv", app.key)
        assertEquals(CoreR.string.compat_update_app_firetv, updateAppHow(app.platform))
    }
}
