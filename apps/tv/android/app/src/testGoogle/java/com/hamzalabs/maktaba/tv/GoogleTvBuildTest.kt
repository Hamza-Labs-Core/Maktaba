package com.hamzalabs.maktaba.tv

import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.core.api.ClientHeader
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith

/** Story 58.05: the Google Play build stays the Android TV app it was (`androidtv`). */
@RunWith(AndroidJUnit4::class)
class GoogleTvBuildTest {
    @Test
    fun theGoogleBuildIsAnAndroidTv() {
        assertEquals("google", BuildConfig.FLAVOR)
        assertEquals(ClientHeader.ANDROID_TV, BuildConfig.TV_PLATFORM)
        ApplicationProvider.getApplicationContext<MaktabaTVApp>()
        assertTrue(ClientHeader.value, ClientHeader.value.endsWith("(androidtv; tv)"))
    }
}
