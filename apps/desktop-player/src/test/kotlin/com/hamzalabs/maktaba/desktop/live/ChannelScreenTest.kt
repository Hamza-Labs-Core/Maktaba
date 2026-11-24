package com.hamzalabs.maktaba.desktop.live

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.runComposeUiTest
import com.hamzalabs.maktaba.desktop.InPlayer
import com.hamzalabs.maktaba.desktop.STALL_MS
import com.hamzalabs.maktaba.desktop.TestHome
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Story 13.22 on the channel screen, with the engine's players faked: the channel plays in the
 * engine's player through the stream proxy, Next channel loads into the same player, Pause
 * pauses it, Back to live loads the same address again, and leaving disposes it.
 */
@OptIn(ExperimentalTestApi::class)
class ChannelScreenTest {
    @Test
    fun aChannelPlaysInTheEnginesPlayerAndChangesInTheSameOne() = TestHome().use { f ->
        f.answer("GET /api/tv/info", "{}")
        f.answer("GET /api/channels/now", """{"channels":[{"channel":{"id":"c1","number":1,"name":"Quran"}},
            {"channel":{"id":"c2","number":2,"name":"News"}}]}""")
        val players = RecordingPlayers()
        val home = f.home()
        var shown by mutableStateOf(true)
        runComposeUiTest {
            setContent { InPlayer(home) { if (shown) ChannelScreen("c1", players) } }
            waitUntil(timeoutMillis = STALL_MS) { onAllNodes(hasTestTag("video-surface")).fetchSemanticsNodes().isNotEmpty() }
            waitUntil(timeoutMillis = STALL_MS) { onAllNodes(hasText("Channel 1, Quran")).fetchSemanticsNodes().isNotEmpty() }
            val first = players.calls.single()
            // the stream proxy's loopback address, which carries the token on to the home
            assertTrue(first, first.startsWith("open http://127.0.0.1:") && first.endsWith("/stream/channel/c1/index.m3u8"))
            onNodeWithTag("channel-next").performClick()
            waitUntil(timeoutMillis = STALL_MS) { players.calls.size >= 2 }
            onNodeWithTag("channel-pause").performClick()
            onNodeWithTag("back-to-live").performClick()
            waitUntil(timeoutMillis = STALL_MS) { players.calls.size >= 4 }
            val c2 = first.removePrefix("open ").replace("/c1/", "/c2/")
            assertEquals(listOf(first, "loadfile $c2 replace", "pause", "loadfile $c2 replace"), players.calls.toList())
            waitUntil(timeoutMillis = STALL_MS) { onAllNodes(hasText("Channel 2, News")).fetchSemanticsNodes().isNotEmpty() }
            shown = false
            waitUntil(timeoutMillis = STALL_MS) { "dispose 1" in players.calls }
        }
        assertEquals("one player for the screen", 1, players.events.size)
    }
}
