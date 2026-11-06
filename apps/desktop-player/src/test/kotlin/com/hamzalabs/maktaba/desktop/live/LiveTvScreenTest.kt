package com.hamzalabs.maktaba.desktop.live

import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.runComposeUiTest
import com.hamzalabs.maktaba.core.api.Feature
import com.hamzalabs.maktaba.core.api.LiveApi
import com.hamzalabs.maktaba.desktop.InPlayer
import com.hamzalabs.maktaba.desktop.Lang
import com.hamzalabs.maktaba.desktop.TestHome
import com.hamzalabs.maktaba.desktop.shell.Route
import org.junit.Assert.assertEquals
import org.junit.Test

/** Story 55.07 T07, and Now opening a channel. */
@OptIn(ExperimentalTestApi::class)
class LiveTvScreenTest {
    @Test
    fun anAdminWithLiveTvOffReadsTheNoteCentred() = TestHome().use { f ->
        f.features.value = TestHome.caps(Feature.LIVE_TV, admin = true)
        val home = f.home(admin = true)
        runComposeUiTest {
            setContent { InPlayer(home, Lang.Ar) { LiveTvContent(LiveTvState(LiveApi(f.client)), home, f.features.value) } }
            onNodeWithText("البث المباشر متوقف على الجهاز الرئيسي.").assertExists()
            onNodeWithTag("feature-turn-on").performClick()
            assertEquals(listOf("${home.webUrl}/settings/components"), f.opened)
        }
        assertEquals("nothing is asked while it is off", 0, f.asked("GET /api/channels/now").size)
    }

    @Test
    fun nowListsTheChannelsAndOneOpens() = TestHome().use { f ->
        f.answer("GET /api/channels/now", """{"channels":[{"channel":{"id":"c1","number":1,"name":"Quran"},
            "current":{"start":"2026-11-06T18:00:00Z","stop":"2026-11-06T18:40:00Z","title":"Tafsir"},
            "next":{"start":"2026-11-06T18:40:00Z","stop":"2026-11-06T19:00:00Z","title":"Adhan"}}]}""")
        val home = f.home()
        runComposeUiTest {
            setContent { InPlayer(home) { LiveTvContent(LiveTvState(LiveApi(f.client)), home, f.features.value) } }
            waitUntil(timeoutMillis = 5_000) { onAllNodes(hasTestTag("now-c1")).fetchSemanticsNodes().isNotEmpty() }
            onNodeWithText("Channel 1, Quran").assertExists()
            onNodeWithText("Tafsir").assertExists()
            onNodeWithTag("now-c1").performClick()
            assertEquals(Route.Channel("c1"), home.nav.current)
        }
    }
}
