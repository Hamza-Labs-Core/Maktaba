package com.hamzalabs.maktaba.desktop.watch

import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.runComposeUiTest
import com.hamzalabs.maktaba.core.api.Capabilities
import com.hamzalabs.maktaba.desktop.InPlayer
import com.hamzalabs.maktaba.desktop.PlayRequest
import com.hamzalabs.maktaba.desktop.Playback
import com.hamzalabs.maktaba.desktop.Players
import com.hamzalabs.maktaba.desktop.STALL_MS
import com.hamzalabs.maktaba.desktop.TestHome
import com.hamzalabs.maktaba.desktop.Video
import com.hamzalabs.maktaba.desktop.VideoEvents
import com.hamzalabs.maktaba.desktop.ControlsActions
import com.hamzalabs.maktaba.desktop.ControlsBar
import com.hamzalabs.maktaba.core.playback.QualityOption
import com.hamzalabs.maktaba.desktop.mpv.MPV_PROFILE
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import org.junit.Assert.assertEquals
import org.junit.Test
import java.util.Collections

/**
 * Story 13.20 on the Watch screen's bar: over a file played as it is by a player that draws its own
 * subtitles, the Subtitles menu lists the home's sidecar, then the file's tracks marked In the
 * file (and Picture), and a choice reaches the player.
 */
@OptIn(ExperimentalTestApi::class)
class WatchSubtitlesTest {
    private val chosen: MutableList<SubtitleOption?> = Collections.synchronizedList(mutableListOf())

    private val drawing = object : Players {
        override val profile = MPV_PROFILE
        override val drawsSubtitles = true
        override suspend fun open(url: String, fromMs: Long, autoplay: Boolean, events: VideoEvents): Video {
            events.subtitleTracks(listOf(FileSubtitle("1", null, "ara", "subrip"), FileSubtitle("2", null, "fra", "hdmv_pgs_subtitle")))
            return object : Video {
                override fun toggle() {}
                override fun pause() {}
                override fun seek(ms: Long) {}
                override fun sound(volume: Float, muted: Boolean) {}
                override fun dispose() {}
                override fun selectSubtitle(option: SubtitleOption?): Boolean {
                    chosen += option
                    return true
                }
            }
        }
    }

    @Test
    fun theMenuListsTheHomesThenTheFilesAndAChoiceReachesThePlayer() = TestHome().use { fake ->
        fake.answer("GET /api/stream/capabilities", """{"client_profiles":["desktop-mpv"]}""")
        fake.answer("POST /api/stream/sessions", """{"session_id":"s1","mode":"direct","direct_url":"/stream/s1.mkv","stream_sig":"sig"}""")
        fake.answer("GET /api/videos/A/subtitles", """{"items":[{"id":1,"language":"en","format":"srt","source":"external"}]}""")
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default.limitedParallelism(1))
        val playback = Playback(fake.client, scope, scope, signedOut = {}, engine = { drawing }, log = {})
        try {
            runComposeUiTest {
                setContent {
                    InPlayer(fake.home()) {
                        // the Watch screen's bar, with this playback's menu (its picture is JavaFX's
                        // or libmpv's, which do not run off screen)
                        ControlsBar(
                            playing = true, positionMs = 0, durationMs = 60_000, chapters = emptyList(), volume = 1f, muted = false,
                            features = Capabilities.AllOn, homeUrl = "http://home", fullScreen = false,
                            subtitles = playback.subtitleOptions, subtitle = playback.subtitle,
                            audioOptions = emptyList(), audio = null, qualities = emptyList(), quality = QualityOption.Auto,
                            on = ControlsActions(subtitle = playback::chooseSubtitle),
                        )
                    }
                }
                runOnIdle { playback.play(PlayRequest("A", "A", startSec = 0)) }
                waitUntil(timeoutMillis = STALL_MS) { onAllNodes(hasTestTag("subtitles")).fetchSemanticsNodes().isNotEmpty() && playback.subtitleOptions.size == 3 }
                onNodeWithTag("subtitles").performClick()
                onNodeWithText("English").assertExists()
                onNodeWithText("Arabic").assertExists()
                onNodeWithText("In the file · Picture").assertExists()
                onNodeWithTag("subtitles-1").performClick()
                waitForIdle()
                assertEquals("1", chosen.single()?.trackId)
            }
        } finally {
            playback.close()
            scope.cancel()
        }
    }
}
