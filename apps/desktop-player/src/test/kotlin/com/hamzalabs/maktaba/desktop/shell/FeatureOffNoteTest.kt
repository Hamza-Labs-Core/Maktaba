package com.hamzalabs.maktaba.desktop.shell

import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.runComposeUiTest
import com.hamzalabs.maktaba.core.api.Feature
import com.hamzalabs.maktaba.desktop.InPlayer
import com.hamzalabs.maktaba.desktop.Lang
import com.hamzalabs.maktaba.desktop.TestHome
import org.junit.Assert.assertEquals
import org.junit.Test

/** Story 55.02 T05: the sentence and the Components button, in each language. */
@OptIn(ExperimentalTestApi::class)
class FeatureOffNoteTest {
    @Test
    fun anAdminReadsWhyAndOpensComponents() = TestHome().use { fake ->
        val opened = mutableListOf<String>()
        runComposeUiTest {
            setContent { InPlayer(fake.home(admin = true)) { FeatureOffNote(Feature.LIVE_TV, "https://home.test", open = { opened += it }) } }
            onNodeWithText("Live TV is off on this home.").assertExists()
            onNodeWithText("Turn it on in Components").performClick()
            assertEquals(listOf("https://home.test/settings/components"), opened)
        }
    }

    @Test
    fun inArabic() = TestHome().use { fake ->
        runComposeUiTest {
            setContent { InPlayer(fake.home(admin = true), Lang.Ar) { FeatureOffNote(Feature.LIVE_TV, "https://home.test") } }
            onNodeWithText("البث المباشر متوقف على الجهاز الرئيسي.").assertExists()
            onNodeWithText("شغّل من صفحة المكوّنات").assertExists()
            onNodeWithTag("feature-turn-on").assertExists()
        }
    }
}
